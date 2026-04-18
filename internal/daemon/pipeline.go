package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/punt-labs/beadle/internal/email"
)

// Pipeline tracks the execution state of a planned command sequence.
type Pipeline struct {
	Version   int           `json:"version"`
	ID        string        `json:"id"`
	CreatedAt time.Time     `json:"created_at"`
	Email     EmailMeta     `json:"email"`
	Commands  []CommandCall `json:"commands"`
	ElseCmd   *CommandCall  `json:"else_cmd"`
	Current   int           `json:"current"`
	Results   []string      `json:"results"`
	Status    string        `json:"status"`
	Error     string        `json:"error"`
	WriteSet  []string      `json:"write_set"`
}

// Spawner runs a Claude Code worker session for a mission.
type Spawner interface {
	Run(ctx context.Context, missionID, mcpConfigPath, systemPromptPath string, envOverrides map[string]string) (WorkerResult, error)
}

// Executor plans, validates, and runs a command pipeline for an incoming email.
type Executor struct {
	Planner  Planner
	Commands map[string]*Command
	Runners  map[string]Runner
	Store    *PipelineStore
	Logger   *slog.Logger
}

// Run plans and executes a pipeline for the given email.
func (e *Executor) Run(ctx context.Context, meta EmailMeta, body string) (*Pipeline, error) {
	p := &Pipeline{
		Version:   1,
		ID:        uuid.New().String(),
		CreatedAt: time.Now(),
		Email:     meta,
		Status:    "running",
	}
	e.save(p)

	calls, err := e.Planner.Plan(ctx, meta, body)
	if err != nil {
		p.Status = "failed"
		p.Error = fmt.Sprintf("plan: %v", err)
		e.save(p)
		e.fireElse(p)
		return p, fmt.Errorf("plan pipeline %s: %w", p.ID, err)
	}
	if len(calls) == 0 {
		p.Status = "failed"
		p.Error = "plan returned empty command list"
		e.save(p)
		e.fireElse(p)
		return p, fmt.Errorf("pipeline %s: empty plan", p.ID)
	}

	// Validate all commands before execution.
	for i, call := range calls {
		cmd, ok := e.Commands[call.Command]
		if !ok {
			p.Status = "failed"
			p.Error = fmt.Sprintf("stage %d: unknown command %q", i, call.Command)
			e.save(p)
			e.fireElse(p)
			return p, fmt.Errorf("pipeline %s stage %d: unknown command %q", p.ID, i, call.Command)
		}
		if err := ValidateArgs(cmd, call.Args); err != nil {
			p.Status = "failed"
			p.Error = fmt.Sprintf("stage %d: %v", i, err)
			e.save(p)
			e.fireElse(p)
			return p, fmt.Errorf("pipeline %s stage %d: %w", p.ID, i, err)
		}
	}

	p.Commands = calls
	p.WriteSet = e.unionWriteSets(calls)
	p.Results = make([]string, 0, len(calls))

	// Pre-compile output schemas.
	schemas := make(map[string]*jsonschema.Schema, len(calls))
	for _, call := range calls {
		cmd := e.Commands[call.Command]
		schema, err := CompileSchema(cmd.OutputSchema)
		if err != nil {
			p.Status = "failed"
			p.Error = fmt.Sprintf("compile schema for %s: %v", call.Command, err)
			e.save(p)
			e.fireElse(p)
			return p, fmt.Errorf("pipeline %s: compile schema for %s: %w", p.ID, call.Command, err)
		}
		schemas[call.Command] = schema
	}

	// Initialize the pipe with email metadata JSON.
	pipeData, err := json.Marshal(map[string]string{
		"message_id":  meta.MessageID,
		"from":        meta.From,
		"subject":     meta.Subject,
		"trust_level": "trusted",
	})
	if err != nil {
		p.Status = "failed"
		p.Error = fmt.Sprintf("marshal pipe: %v", err)
		e.save(p)
		return p, fmt.Errorf("pipeline %s: marshal pipe: %w", p.ID, err)
	}
	pipe := string(pipeData)

	// Execute sequentially.
	for i, call := range calls {
		p.Current = i
		e.save(p)

		cmd := e.Commands[call.Command]
		runner, ok := e.Runners[cmd.Runner]
		if !ok {
			p.Status = "failed"
			p.Error = fmt.Sprintf("stage %d (%s): unknown runner %q", i, call.Command, cmd.Runner)
			e.save(p)
			e.fireElse(p)
			return p, fmt.Errorf("pipeline %s stage %d (%s): unknown runner %q", p.ID, i, call.Command, cmd.Runner)
		}
		result, err := runner.Run(ctx, e, p, i, cmd, call, pipe)
		if err != nil {
			p.Status = "failed"
			p.Error = fmt.Sprintf("stage %d (%s): %v", i, call.Command, err)
			e.save(p)
			e.fireElse(p)
			return p, fmt.Errorf("pipeline %s stage %d (%s): %w", p.ID, i, call.Command, err)
		}

		if cmd.Mode == "process" {
			if err := ValidateOutput(schemas[call.Command], result); err != nil {
				e.Logger.Warn("output schema validation failed",
					"pipeline", p.ID, "stage", i,
					"command", call.Command, "error", err)
				p.Status = "failed"
				p.Error = fmt.Sprintf("stage %d (%s): output validation: %v", i, call.Command, err)
				e.save(p)
				e.fireElse(p)
				return p, fmt.Errorf("pipeline %s stage %d (%s): output validation: %w", p.ID, i, call.Command, err)
			}
			pipe = result
		}
		// passthrough: pipe unchanged, result logged only

		p.Results = append(p.Results, result)
		e.save(p)
	}

	// Auto-append reply to originator with current pipe value.
	if replyCmd, ok := e.Commands["reply"]; ok {
		replyCall := CommandCall{
			Command: "reply",
			Args: map[string]any{
				"to": email.ExtractEmailAddress(p.Email.From),
			},
		}
		if err := ValidateArgs(replyCmd, replyCall.Args); err == nil {
			runner := e.Runners[replyCmd.Runner]
			replyResult, err := runner.Run(ctx, e, p, len(p.Commands), replyCmd, replyCall, pipe)
			if err != nil {
				e.Logger.Warn("auto-reply failed", "pipeline", p.ID, "error", err)
			} else {
				p.Results = append(p.Results, replyResult)
			}
		}
	} else {
		e.Logger.Warn("reply command not in registry, skipping auto-reply", "pipeline", p.ID)
	}

	p.Status = "completed"
	e.save(p)
	return p, nil
}

// buildStageContract generates a mission contract string for one pipeline stage.
func buildStageContract(meta EmailMeta, cmd *Command, call CommandCall, pipe string) string {
	pipeValue := "none"
	if pipe != "" {
		pipeValue = escapeYAMLPipe(pipe)
	}

	argsYAML := ""
	for k, v := range call.Args {
		argsYAML += fmt.Sprintf("      %s: %s\n", k, escapeYAMLValue(fmt.Sprint(v)))
	}

	return fmt.Sprintf(`leader: claude
worker: bwk
evaluator:
  handle: mdm
inputs:
  trigger:
    type: email
    message_id: %s
    from: %s
    subject: %s
  args:
%s  pipeline_output: %s
write_set:
  - %s
success_criteria:
  - %s
budget:
  rounds: %d
  reflection_after_each: %v
`,
		escapeYAMLValue(meta.MessageID),
		escapeYAMLValue(meta.From),
		escapeYAMLValue(meta.Subject),
		argsYAML,
		pipeValue,
		writeSetYAML(cmd.WriteSet),
		escapeYAMLValue(cmd.Prompt),
		cmd.Budget.Rounds,
		cmd.Budget.ReflectionAfterEach,
	)
}

// writeSetYAML formats a write_set slice as YAML list items.
func writeSetYAML(ws []string) string {
	if len(ws) == 0 {
		return "daemon output"
	}
	s := ws[0]
	for _, w := range ws[1:] {
		s += "\n  - " + w
	}
	return s
}

// resolveEnvVars converts a list of "KEY" names to a map of KEY=value
// from the current process environment. Missing vars are silently skipped.
func resolveEnvVars(names []string) map[string]string {
	if len(names) == 0 {
		return nil
	}
	m := make(map[string]string, len(names))
	for _, name := range names {
		if v, ok := os.LookupEnv(name); ok {
			m[name] = v
		}
	}
	if len(m) == 0 {
		return nil
	}
	return m
}

// unionWriteSets collects unique write_set entries across all commands.
func (e *Executor) unionWriteSets(calls []CommandCall) []string {
	seen := make(map[string]bool)
	var ws []string
	for _, call := range calls {
		cmd, ok := e.Commands[call.Command]
		if !ok {
			continue
		}
		for _, w := range cmd.WriteSet {
			if !seen[w] {
				seen[w] = true
				ws = append(ws, w)
			}
		}
	}
	return ws
}

// save persists the pipeline state if a Store is configured.
func (e *Executor) save(p *Pipeline) {
	if e.Store == nil {
		return
	}
	if err := e.Store.Save(p); err != nil {
		e.Logger.Error("persist pipeline state", "pipeline", p.ID, "error", err)
	}
}

// truncateLog returns s truncated to max bytes, appending "..." if truncated.
func truncateLog(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

// fireElse logs the pipeline error and sends a reply to the originator
// with a fixed-text error message. If the reply command is not registered
// or sending fails, the error is logged but does not propagate.
func (e *Executor) fireElse(p *Pipeline) {
	e.Logger.Error("pipeline failed, else handler",
		"pipeline", p.ID,
		"error", p.Error,
		"email_from", p.Email.From,
		"email_subject", truncateLog(p.Email.Subject, 200))

	replyCmd, ok := e.Commands["reply"]
	if !ok {
		return
	}

	elsePipe := "Your request could not be completed. Reference: pipeline-" + p.ID

	replyCall := CommandCall{
		Command: "reply",
		Args: map[string]any{
			"to": email.ExtractEmailAddress(p.Email.From),
		},
	}
	if err := ValidateArgs(replyCmd, replyCall.Args); err != nil {
		return
	}

	runner, ok := e.Runners[replyCmd.Runner]
	if !ok {
		return
	}

	ctx := context.Background()
	_, err := runner.Run(ctx, e, p, -1, replyCmd, replyCall, elsePipe)
	if err != nil {
		e.Logger.Warn("else reply failed", "pipeline", p.ID, "error", err)
	}
}
