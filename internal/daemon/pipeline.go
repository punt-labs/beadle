package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/google/uuid"
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
	Planner   Planner
	Commands  map[string]*Command
	Missions  MissionCreator
	Spawner   Spawner
	Templates *MissionTemplate
	Registry  map[string]MCPServerConfig
	Store     *PipelineStore
	Logger    *slog.Logger
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

	// Execute sequentially.
	for i, call := range calls {
		p.Current = i
		e.save(p)

		cmd := e.Commands[call.Command]
		result, err := e.executeStage(ctx, p, i, cmd, call)
		if err != nil {
			p.Status = "failed"
			p.Error = fmt.Sprintf("stage %d (%s): %v", i, call.Command, err)
			e.save(p)
			e.fireElse(p)
			return p, fmt.Errorf("pipeline %s stage %d (%s): %w", p.ID, i, call.Command, err)
		}
		p.Results = append(p.Results, result)
		e.save(p)
	}

	p.Status = "completed"
	e.save(p)
	return p, nil
}

// executeStage creates a mission and spawns a worker for one pipeline stage.
func (e *Executor) executeStage(ctx context.Context, p *Pipeline, idx int, cmd *Command, call CommandCall) (string, error) {
	var prevOutput string
	if idx > 0 {
		prevOutput = p.Results[idx-1]
	}

	contract := buildStageContract(p.Email, cmd, call, prevOutput)

	missionID, err := e.Missions.Create(EmailMeta{
		MessageID: p.Email.MessageID,
		From:      p.Email.From,
		Subject:   fmt.Sprintf("[pipeline %s stage %d] %s", p.ID, idx, call.Command),
	})
	if err != nil {
		return "", fmt.Errorf("create mission: %w", err)
	}

	e.Logger.Info("stage mission created",
		"pipeline", p.ID, "stage", idx,
		"command", call.Command, "mission", missionID)

	// Build MCP config from the command's mcp_servers.
	servers := cmd.MCPServers
	if len(servers) == 0 {
		servers = []string{"ethos", "beadle-email"}
	}
	mcpPath, err := e.Templates.BuildMCPConfig(servers, e.Registry)
	if err != nil {
		return "", fmt.Errorf("build mcp config: %w", err)
	}
	defer os.Remove(mcpPath)

	promptPath, err := e.Templates.BuildSystemPrompt(missionID)
	if err != nil {
		return "", fmt.Errorf("build system prompt: %w", err)
	}
	defer os.Remove(promptPath)

	// Resolve env overrides from command's env_vars.
	envOverrides := resolveEnvVars(cmd.EnvVars)

	wr, err := e.Spawner.Run(ctx, missionID, mcpPath, promptPath, envOverrides)
	if err != nil {
		return "", fmt.Errorf("spawn worker: %w", err)
	}
	if wr.IsError {
		return "", fmt.Errorf("worker error (exit %d): %s", wr.ExitCode, wr.Output)
	}

	e.Logger.Info("stage completed",
		"pipeline", p.ID, "stage", idx,
		"command", call.Command, "mission", missionID)

	_ = contract // contract string is passed to MissionCreator via meta; kept for future use
	return wr.Output, nil
}

// buildStageContract generates a mission contract string for one pipeline stage.
func buildStageContract(meta EmailMeta, cmd *Command, call CommandCall, prevOutput string) string {
	prev := "none"
	if prevOutput != "" {
		prev = escapeYAMLValue(prevOutput)
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
%s  previous_output: %s
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
		prev,
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

// fireElse logs the pipeline error. Sending a reply email is future work.
func (e *Executor) fireElse(p *Pipeline) {
	e.Logger.Error("pipeline failed, else handler",
		"pipeline", p.ID,
		"error", p.Error,
		"email_from", p.Email.From,
		"email_subject", truncateLog(p.Email.Subject, 200))
}
