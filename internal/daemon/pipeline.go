package daemon

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/punt-labs/beadle/internal/email"
)

// Pipeline tracks the execution state of a planned command sequence.
type Pipeline struct {
	Version   int       `json:"version"`
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	Email     EmailMeta `json:"email"`
	// Body is the triggering email's body, carried so a stage can act on the
	// actual message content instead of being handed a shell and left to go
	// fetch it itself (beadle-ivtd). It is sanitized (sanitizeText) and
	// capped at maxContractFieldRunes exactly once, when the pipeline is
	// created (see Run) -- the one place that decides this state, per the
	// round-3 design rule (beadle-4gm1): a downstream reader of Body must
	// never re-sanitize or re-cap it and infer BodyTruncated/BodySanitized
	// from the bytes, both because that duplicates work and because a
	// second round of sanitization or capping on already-processed text
	// produces a different, wrong answer (NULs already stripped shift where
	// a rune-count cap lands; an already-capped value is never over length
	// a second time, so a naive "capped != value" check downstream would
	// always read false even when the ORIGINAL body needed truncating).
	// Body itself never carries a truncation marker -- capRunes returns
	// whether it cut the value as a separate bool, so there is no marker
	// text to ever strip back off -- BodyTruncated is the sole statement of
	// that fact, read by stagePayloadText (BuildSystemPromptForTools,
	// templates.go) when it fences this value.
	Body          string        `json:"body"`
	BodyTruncated bool          `json:"body_truncated"`
	BodySanitized bool          `json:"body_sanitized"`
	Commands      []CommandCall `json:"commands"`
	ElseCmd       *CommandCall  `json:"else_cmd"`
	Current       int           `json:"current"`
	Results       []string      `json:"results"`
	Status        string        `json:"status"`
	// Error may be set even when Status is "completed" -- it records a
	// non-fatal auto-reply failure. Test Status for success, not Error.
	Error    string   `json:"error"`
	WriteSet []string `json:"write_set"`
}

// Spawner runs a Claude Code worker session for a mission.
type Spawner interface {
	Run(ctx context.Context, missionID, mcpConfigPath, systemPromptPath string, tools []string, envOverrides map[string]string) (WorkerResult, error)
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
	// Sanitize and cap body exactly once, here -- the one place that
	// decides Pipeline.Body's truncation and sanitization state (see
	// Pipeline.Body's doc comment). capRunes never appends a marker, so
	// there is nothing to strip back off: BodyTruncated is the sole
	// statement of that fact for every downstream reader (stagePayloadText,
	// via BuildSystemPromptForTools) to fence on.
	//
	// A whitespace-only body (e.g. "\r\n" -- the trailing MIME boundary
	// CRLF ParseMIME returns for a multipart/signed message whose signed
	// part carries no real text) is normalized to "" here, the one place
	// that decides it: stagePayloadText used to assume an empty and a
	// whitespace-only body "render the same way downstream," but it only tests
	// EmailBody != "" -- "\r\n" is not "", so it rendered as a 2-rune
	// fence instead of the "(none)" marker the comment claimed. Deciding
	// it here, before sanitize/cap, means a trimmed-to-empty body also
	// carries no truncation or sanitization state: there is nothing left
	// to have truncated or sanitized once the decision is "no body text."
	//
	// Not an error: a subject-only email, or a multipart/signed message
	// whose signed part is nothing but the MIME boundary's CRLF, simply has
	// no body text. It is logged below (once p.ID exists) so the daemon log
	// explains why this pipeline's system prompt renders "email body: (none)"
	// -- without the line, that render looks like a bug to anyone reading the
	// log without also reading the source message.
	bodyEmpty := strings.TrimSpace(body) == ""
	if bodyEmpty {
		body = ""
	}
	sanitizedBody, bodySanitized := sanitizeText(body)
	cappedBody, bodyTruncated := capRunes(sanitizedBody, maxContractFieldRunes)

	p := &Pipeline{
		Version:       1,
		ID:            uuid.New().String(),
		CreatedAt:     time.Now(),
		Email:         meta,
		Body:          cappedBody,
		BodyTruncated: bodyTruncated,
		BodySanitized: bodySanitized,
		Status:        "running",
	}
	e.save(p)

	if bodyEmpty {
		e.Logger.Warn("email body empty or whitespace-only", "pipeline", p.ID)
	}

	// Logged once, right after the one place that decided it, so the
	// decision has a record independent of whatever a later stage renders
	// from it -- no body content in the line, since the whole reason for
	// sanitizing and capping is that this text is adversarial.
	if bodyTruncated || bodySanitized {
		e.Logger.Warn("email body sanitized or truncated before stage input",
			"pipeline", p.ID,
			"truncated", bodyTruncated,
			"sanitized", bodySanitized,
			"original_runes", utf8.RuneCountInString(body),
			"kept_runes", utf8.RuneCountInString(cappedBody))
	}

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
		e.fireElse(p)
		return p, fmt.Errorf("pipeline %s: marshal pipe: %w", p.ID, err)
	}
	pipe := string(pipeData)
	// pipeTruncated travels WITH pipe, as a local pair, never through a
	// field on p: round 4 of beadle-4gm1 found that a shared, resettable
	// Pipeline.PipeTruncated field described whichever stage ran most
	// recently, not necessarily the one whose pipe value the current stage
	// is about to read -- the reset at the top of stage i raced the one
	// read of stage i-1's truncation state inside runner.Run at the bottom
	// of stage i. Carrying both as locals, reassigned together only where
	// pipe itself is reassigned, makes that race structurally impossible:
	// there is no field left to reset on a schedule that can get out of
	// sync with the value it describes.
	pipeTruncated := false

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
		result, resultTruncated, err := runner.Run(ctx, e, p, i, cmd, call, pipe, pipeTruncated)
		if err != nil {
			p.Status = "failed"
			p.Error = fmt.Sprintf("stage %d (%s): %v", i, call.Command, err)
			e.save(p)
			e.fireElse(p)
			return p, fmt.Errorf("pipeline %s stage %d (%s): %w", p.ID, i, call.Command, err)
		}

		if cmd.Mode == "process" {
			if err := ValidateOutput(schemas[call.Command], result); err != nil {
				// The validator can quote the offending value, which is stage
				// output derived from the email: cap it before it is logged or
				// stored.
				msg := truncateLog(err.Error(), 200)
				if resultTruncated {
					msg = "output truncated at the 1MB cap, then output validation: " + msg
				}
				e.Logger.Warn("output schema validation failed",
					"pipeline", p.ID, "stage", i,
					"command", call.Command, "error", msg, "truncated", resultTruncated)
				p.Status = "failed"
				p.Error = fmt.Sprintf("stage %d (%s): output validation: %s", i, call.Command, msg)
				e.save(p)
				e.fireElse(p)
				return p, fmt.Errorf("pipeline %s stage %d (%s): output validation: %s", p.ID, i, call.Command, msg)
			}
			// The pipe stays RAW so a CLI stage downstream reads exactly the
			// bytes this one wrote; ClaudeRunner sanitizes at the fence.
			pipe = result
			pipeTruncated = resultTruncated
		}
		// passthrough: pipe (and its truncation state) unchanged, result
		// recorded like any other stage

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
		if err := ValidateArgs(replyCmd, replyCall.Args); err != nil {
			// Permanent misconfiguration: the reply command's signature never
			// changes, so this fails on every pipeline run, forever.
			e.Logger.Error("auto-reply args invalid", "pipeline", p.ID, "error", err)
			p.Error = fmt.Sprintf("auto-reply args invalid: %v", err)
		} else if runner, rok := e.Runners[replyCmd.Runner]; !rok {
			// Same: a runner registration gap does not clear itself between runs.
			e.Logger.Error("auto-reply runner not registered", "pipeline", p.ID, "runner", replyCmd.Runner)
			p.Error = fmt.Sprintf("auto-reply runner not registered: %s", replyCmd.Runner)
		} else {
			// The auto-reply reads the FINAL pipe value and its carried
			// truncation state -- whatever the last "process" stage left
			// them as -- never a flag reset on its own schedule.
			replyResult, _, err := runner.Run(ctx, e, p, len(p.Commands), replyCmd, replyCall, pipe, pipeTruncated)
			if err != nil {
				e.Logger.Warn("auto-reply failed", "pipeline", p.ID, "error", err)
				p.Error = fmt.Sprintf("auto-reply failed: %v", err)
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

// buildStageContract generates a mission contract string for one pipeline
// stage. The contract carries no part of the stage's actual payload: not
// the triggering email's body, not this stage's call args, not the prior
// stage's pipeline output. All three are adversarial, email-derived free
// text, and the mission contract is a file ethos's own writers persist and
// may redact on a later read (ethos PR #539, DES-080) -- a worker reading
// its own contract via `ethos mission show` must not depend on an ethos
// version the daemon does not control. The payload instead travels through
// the per-spawn, untracked system prompt file (stagePayloadText,
// BuildSystemPromptForTools in templates.go), which ethos's mission-record
// writers never see. See docs/email-payload-handoff.md (DES-038) for the
// design and the rejected alternatives.
//
// context carries only a fixed, daemon-generated reference naming
// pipelineID and stageIdx, so a human reading the contract knows where the
// real content lives without the contract ever having held it.
//
// ethos's inputs schema recognizes only trigger, files, ticket, and
// references (see ethos's internal/mission/inputs.go) -- there is no
// inputs.args or inputs.pipeline_output, which is why that content was
// never a candidate for inputs in the first place.
func buildStageContract(meta EmailMeta, cmd *Command, pipelineID string, stageIdx int) string {
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
context: %s
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
		escapeYAMLValue(stageContextReference(pipelineID, stageIdx)),
		writeSetYAML(cmd.WriteSet),
		escapeYAMLPipe(cmd.Prompt),
		cmd.Budget.Rounds,
		cmd.Budget.ReflectionAfterEach,
	)
}

// stageContextReference is the fixed-shape text buildStageContract writes
// into the mission contract's context field in place of the stage's actual
// payload. It names pipelineID and stageIdx so a reader of the tracked
// contract knows which pipeline run and stage it belongs to; the real
// content -- stage args, prior pipeline output, the triggering email's
// body -- is in that stage's own per-spawn system prompt (stagePayloadText,
// BuildSystemPromptForTools), not here.
func stageContextReference(pipelineID string, stageIdx int) string {
	// fireElse runs its reply with idx -1: it is no stage of the plan.
	stage := fmt.Sprintf("stage %d", stageIdx)
	if stageIdx < 0 {
		stage = "failure reply"
	}
	return fmt.Sprintf(
		"stage input delivered via the per-spawn system prompt (pipeline %s, "+
			"%s); the triggering email's body, this stage's args, and the "+
			"prior stage's output are not stored here -- see this worker's own "+
			"appended system prompt (STAGE INPUT, after SECURITY) for the content",
		pipelineID, stage)
}

// stagePayloadText formats a stage's call args, the prior stage's pipeline
// output, and the triggering email's body as the content of the per-spawn
// system prompt's STAGE INPUT block (BuildSystemPromptForTools,
// templates.go) -- never the mission contract (see buildStageContract's doc
// comment). Args are sorted by key for deterministic output.
//
// Each field is wrapped in a begin/end fence stamped with nonce (one per
// render, generated by BuildSystemPromptForTools via genFenceNonce) and
// naming the field, its rune count, and whether it was truncated and/or
// sanitized -- never a bare "field: value" line. The payload is
// adversarial, email-derived text (see buildStageContract's doc comment):
// without the nonce, a body containing a line that reads
// "--- end email body ---" followed by a forged "--- begin pipeline
// output: 0 runes ---" could impersonate a daemon-generated field boundary
// a worker (or a naive human reader) might trust; the attacker cannot
// predict the nonce in advance, so a forged fence line simply fails to
// carry it and is distinguishable on sight (BuildSystemPromptForTools's
// STAGE INPUT header states this rule). A fence is also how "no content"
// (the empty marker "--- <nonce> <name>: (none) ---", emptyMarker) is told
// apart from "content that happens to be empty-looking": every line this
// function emits, fence or empty marker, carries the nonce, so a sender line
// that reads the same way but lacks it is plainly sender content.
//
// Stage args, like the pipe and the body, carry truncation/sanitization
// state decided upstream (see StagePayload's doc comment) -- every StageArg
// in payload.Args arrives already sanitized, capped, and pre-sorted by
// Name, by ClaudeRunner.Run, so stagePayloadText only fences it. Pipeline
// output and email body carry their states the same way in payload
// (PipelineOutputTruncated, EmailBodyTruncated, EmailBodySanitized): all
// three were already sanitized and/or capped upstream (ClaudeRunner.Run for
// args, CLIRunner.Run/runOutput/runCompound for the pipe's cap and
// Executor.Run for its sanitization, Executor.Run for the body), and
// re-deriving "truncated" from "capped != value" down here
// would read false even when the ORIGINAL value needed truncating, since an
// already-capped value is never over length a second time.
func stagePayloadText(payload StagePayload, nonce string) string {
	var parts []string

	if len(payload.Args) > 0 {
		argParts := make([]string, len(payload.Args))
		for i, a := range payload.Args {
			argParts[i] = fenceField(nonce, fmt.Sprintf("stage arg %s", a.Name), a.Value, a.Truncated, a.Sanitized)
		}
		parts = append(parts, argParts...)
	} else {
		parts = append(parts, emptyMarker(nonce, "stage args"))
	}

	if payload.PipelineOutput != "" {
		parts = append(parts, fenceField(nonce, "pipeline output", payload.PipelineOutput, payload.PipelineOutputTruncated, payload.PipelineOutputSanitized))
	} else {
		parts = append(parts, emptyMarker(nonce, "pipeline output"))
	}

	if payload.EmailBody != "" {
		parts = append(parts, fenceField(nonce, "email body", payload.EmailBody, payload.EmailBodyTruncated, payload.EmailBodySanitized))
	} else {
		parts = append(parts, emptyMarker(nonce, "email body"))
	}

	return strings.Join(parts, "\n")
}

// emptyMarker renders the "no content" line for an empty field, stamped with
// nonce like every other daemon-emitted line in the STAGE INPUT block, so a
// sender-planted "name: (none)" line is never mistaken for it.
func emptyMarker(nonce, name string) string {
	return fmt.Sprintf("--- %s %s: (none) ---", nonce, name)
}

// fenceField wraps the already-sanitized-and-capped value in a nonce-
// stamped "--- begin/end <nonce> <name> ---" fence. The rune count in the
// begin line is utf8.RuneCountInString(value) -- exactly the content
// between the begin and end lines, nothing else -- and the "truncated"/
// "sanitized" states named alongside it are passed in, never recomputed
// from value itself (see stagePayloadText's doc comment for why). capRunes
// never appends a truncation marker to value, so the fence header is the
// sole statement of truncation.
func fenceField(nonce, name, value string, truncated, sanitized bool) string {
	var states []string
	if sanitized {
		states = append(states, "sanitized")
	}
	if truncated {
		states = append(states, "truncated")
	}
	state := ""
	if len(states) > 0 {
		state = ", " + strings.Join(states, ", ")
	}

	return fmt.Sprintf("--- begin %s %s: %d runes%s ---\n%s\n--- end %s %s ---",
		nonce, name, utf8.RuneCountInString(value), state, value, nonce, name)
}

// genFenceNonce returns a fresh, random 8-byte value hex-encoded (16
// characters), for BuildSystemPromptForTools to stamp into every fence line
// of one system-prompt render (fenceField, stagePayloadText) -- see
// BuildSystemPromptForTools's doc comment for why a per-render nonce closes
// the fence-forgery gap a fixed fence string leaves open. A crypto/rand
// failure is returned as an error, never silently swallowed into a
// weaker or fixed fallback: a fence a sender could predict is not a fence.
func genFenceNonce() (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// sanitizeText strips NUL bytes and replaces invalid UTF-8 with the Unicode
// replacement character, reporting whether either change fired.
func sanitizeText(s string) (out string, changed bool) {
	stripped := strings.ReplaceAll(s, "\x00", "")
	valid := strings.ToValidUTF8(stripped, "�")
	return valid, valid != s
}

// capRunes truncates s to at most n runes, never appending a marker --
// truncated reports whether s was actually cut, the sole statement of that
// fact for every caller. A marker-in-the-value design was tried and
// removed (round 3 of beadle-4gm1): capRunes used to append one, and every
// caller then had to strip it back off before storing or fencing the
// value, because a value under the cap that happened to end with the exact
// marker text would otherwise look truncated when it was not. Returning
// the bool directly makes that failure mode structurally impossible --
// there is no marker text left to collide with.
func capRunes(s string, n int) (out string, truncated bool) {
	if utf8.RuneCountInString(s) <= n {
		return s, false
	}
	return string([]rune(s)[:n]), true
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

// resolveEnvVars converts a list of "KEY" names to a map of KEY=value from
// the current process environment, and reports which declared names had no
// value to resolve. The caller logs missing names with whatever pipeline
// and stage context it has -- resolveEnvVars stays a pure function so the
// "no vars declared" and "all declared vars absent" cases stay
// distinguishable to its caller instead of both collapsing to a nil map.
func resolveEnvVars(names []string) (env map[string]string, missing []string) {
	for _, name := range names {
		if v, ok := os.LookupEnv(name); ok {
			if env == nil {
				env = make(map[string]string, len(names))
			}
			env[name] = v
		} else {
			missing = append(missing, name)
		}
	}
	return env, missing
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

// truncateLog returns s truncated to limit bytes, appending "..." if truncated.
func truncateLog(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "..."
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
		e.Logger.Warn("reply command not in registry, skipping else reply", "pipeline", p.ID)
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
		e.Logger.Warn("else reply args invalid", "pipeline", p.ID, "error", err)
		return
	}

	runner, ok := e.Runners[replyCmd.Runner]
	if !ok {
		e.Logger.Warn("else reply runner not registered", "pipeline", p.ID, "runner", replyCmd.Runner)
		return
	}

	ctx := context.Background()
	// elsePipe is a fixed, daemon-authored string -- never truncated,
	// regardless of what the failed stage's own pipe value was doing.
	_, _, err := runner.Run(ctx, e, p, -1, replyCmd, replyCall, elsePipe, false)
	if err != nil {
		// The pipeline is already terminally failed by the time fireElse
		// runs -- this is the last chance to tell the sender why, not a
		// transient hiccup -- so this is logged at the same level as the
		// sibling permanent-failure lines above ("auto-reply args
		// invalid", "auto-reply runner not registered"), not Warn.
		e.Logger.Error("else reply failed", "pipeline", p.ID, "error", err)
	}
}
