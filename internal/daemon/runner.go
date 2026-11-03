package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Runner executes a single pipeline command and returns its output.
// pipeTruncated is the truncation state of the INCOMING pipe value --
// decided by whichever runner produced it, read here only so ClaudeRunner
// can thread it into StagePayload.PipelineOutputTruncated when it fences
// pipe for the worker. truncated reports whether THIS call's own output
// was cut at the runner's own cap (currently only CLIRunner's 1MB stdout
// cap) -- the one place that decides it. The caller (Executor.Run) carries
// both as local variables alongside the pipe value itself, never on a
// shared Pipeline field: round 4 of beadle-4gm1 found that a single
// resettable p.PipeTruncated field described whichever stage ran most
// recently, not necessarily the one whose pipe value a later stage was
// about to read, because Executor.Run's own reset-before-stage-i raced the
// one reader of stage i-1's truncation state. State now travels WITH the
// pipe value it describes, as a pair of local variables, never through a
// field with its own reset schedule. ClaudeRunner always returns
// truncated=false: a Claude worker's own output has no cap of its own to
// report (WorkerSpawner.Run); Pipeline.BodyTruncated/BodySanitized -- the
// body's own state, decided once in Executor.Run -- travel through
// StagePayload independently of this return value and are unaffected by
// this parameter.
type Runner interface {
	Run(ctx context.Context, e *Executor, p *Pipeline, idx int, cmd *Command, call CommandCall, pipe string, pipeTruncated bool) (output string, truncated bool, err error)
}

// ClaudeRunner executes a command via a Claude Code worker session.
type ClaudeRunner struct {
	Spawner   Spawner
	Missions  MissionCreator
	Templates *MissionTemplate
	Registry  map[string]MCPServerConfig
}

// Run creates a mission from the stage contract, spawns a Claude worker,
// and returns the output. truncated is always false: a Claude worker's
// output has no cap of its own to report (WorkerSpawner.Run), unlike
// CLIRunner's 1MB stdout cap.
func (r *ClaudeRunner) Run(ctx context.Context, e *Executor, p *Pipeline, idx int, cmd *Command, call CommandCall, pipe string, pipeTruncated bool) (string, bool, error) {
	// Checked first, before any mission or config file is built: a
	// permanent deployment misconfiguration (the var was never set in the
	// unit/plist that starts beadle-daemon) fails identically on every
	// run, so there is no reason to pay for mission creation and MCP
	// config generation before finding out. Failing the stage here -- not
	// logging and continuing -- is the fix for beadle-qtei: a recipe's
	// declared env_vars is a contract, and a declared dependency that is
	// absent must fail the stage on that basis alone. This does not lean
	// on the downstream service noticing and erroring instead -- context7
	// itself returns 200 on initialize/tools-list whether the request
	// carries a valid Bearer token, the literal header string, or no
	// Authorization header at all, so a missing CONTEXT7_API_KEY produces
	// no remote error for the pipeline to catch; it would just run
	// against whatever unauthenticated access the endpoint gives, with
	// nothing to signal the difference. The caller (Executor.Run,
	// pipeline.go) already marks the pipeline failed and replies to the
	// sender via fireElse on any error this returns, so a missing key now
	// actually reaches the sender instead of silently degrading to model
	// recall. The same reasoning is why BuildMCPConfig, below, also runs
	// before createMissionFromContract: a missing MCP server binary
	// (docs/worker-mcp-path.md, DES-037) is just as permanent a
	// misconfiguration, and checking it first means a daemon with no
	// provisioned MCP binaries never creates an orphaned ethos mission for
	// it to discover later.
	envOverrides, missingEnv := resolveEnvVars(cmd.EnvVars)
	if len(missingEnv) > 0 {
		return "", false, fmt.Errorf("command %q declares env var(s) %v, absent from the "+
			"daemon's environment -- set them in the unit/plist that starts "+
			"beadle-daemon; running without them would silently degrade this "+
			"command", cmd.Name, missingEnv)
	}

	servers := cmd.MCPServers
	if servers == nil {
		servers = []string{"ethos", "beadle-email"}
	}
	mcpPath, err := r.Templates.BuildMCPConfig(servers, r.Registry)
	if err != nil {
		return "", false, fmt.Errorf("build mcp config: %w", err)
	}
	defer func() {
		if rmErr := os.Remove(mcpPath); rmErr != nil {
			e.Logger.Warn("remove mcp config temp file", "path", mcpPath, "error", rmErr)
		}
	}()

	contract := buildStageContract(p.Email, cmd, p.ID, idx)

	missionID, err := createMissionFromContract(r.Templates.TmpDir, contract)
	if err != nil {
		return "", false, fmt.Errorf("create stage mission: %w", err)
	}

	e.Logger.Info("stage mission created",
		"pipeline", p.ID, "stage", idx,
		"command", call.Command, "mission", missionID)

	// The fence shows sanitized text; the pipe itself stays raw. Decided here,
	// at the one place the pipe is rendered.
	fencedPipe, pipeSanitized := sanitizeText(pipe)
	if pipeSanitized {
		e.Logger.Warn("stage output sanitized before stage input",
			"pipeline", p.ID, "stage", idx)
	}
	payload := StagePayload{
		Args:                    buildStageArgs(e, p, idx, cmd.Name, call.Args),
		PipelineOutput:          fencedPipe,
		PipelineOutputTruncated: pipeTruncated,
		PipelineOutputSanitized: pipeSanitized,
		EmailBody:               p.Body,
		EmailBodyTruncated:      p.BodyTruncated,
		EmailBodySanitized:      p.BodySanitized,
	}
	promptPath, err := r.Templates.BuildSystemPromptForTools(missionID, cmd.Tools, payload)
	if err != nil {
		return "", false, fmt.Errorf("build system prompt: %w", err)
	}
	defer func() {
		// promptPath carries sender content (the STAGE INPUT block) -- a
		// failed removal leaves that content on disk, worth a Warn naming
		// the path so an operator can clean it up, not a silently
		// swallowed error.
		if rmErr := os.Remove(promptPath); rmErr != nil {
			e.Logger.Warn("remove system prompt temp file", "path", promptPath, "error", rmErr)
		}
	}()

	wr, err := r.Spawner.Run(ctx, missionID, mcpPath, promptPath, cmd.Tools, envOverrides)
	if err != nil {
		return "", false, fmt.Errorf("spawn worker: %w", err)
	}
	if wr.IsError {
		// wr.Output is model output derived from the email-borne stage input:
		// untrusted, and this error is logged and persisted, so cap it.
		return "", false, fmt.Errorf("worker error (exit %d): %s", wr.ExitCode, truncateLog(wr.Output, 500))
	}

	closeOut, closeErr := exec.CommandContext(ctx, "ethos", "mission", "close", missionID).CombinedOutput()
	if closeErr != nil {
		e.Logger.Warn("close stage mission", "mission", missionID, "error", closeErr, "output", string(closeOut))
	}

	e.Logger.Info("stage completed",
		"pipeline", p.ID, "stage", idx,
		"command", call.Command, "mission", missionID)

	return wr.Output, false, nil
}

// buildStageArgs sanitizes and caps each of args' values at
// maxContractFieldRunes -- the one decision point for this field, deliberate
// here rather than left to stagePayloadText itself (see StagePayload's doc
// comment on why round 5 moved it): CLIRunner.Run reads call.Args raw,
// unprocessed, so args are adversarial input exactly like the pipe and the
// body, not trusted contract content, and every other such field is already
// decided by the runner that owns it before stagePayloadText ever sees it.
// Returned args are sorted by name for deterministic output. Warns (with
// pipeline/stage/command attribution, never an arg's own value) when any arg
// was truncated or sanitized, the same record-the-decision discipline
// Executor.Run applies to the email body.
func buildStageArgs(e *Executor, p *Pipeline, idx int, cmdName string, args map[string]any) []StageArg {
	if len(args) == 0 {
		return nil
	}

	names := make([]string, 0, len(args))
	for name := range args {
		names = append(names, name)
	}
	sort.Strings(names)

	out := make([]StageArg, len(names))
	var truncatedNames, sanitizedNames []string
	for i, name := range names {
		sanitized, argSanitized := sanitizeText(fmt.Sprint(args[name]))
		capped, argTruncated := capRunes(sanitized, maxContractFieldRunes)
		out[i] = StageArg{Name: name, Value: capped, Truncated: argTruncated, Sanitized: argSanitized}
		if argTruncated {
			truncatedNames = append(truncatedNames, name)
		}
		if argSanitized {
			sanitizedNames = append(sanitizedNames, name)
		}
	}

	if len(truncatedNames) > 0 || len(sanitizedNames) > 0 {
		e.Logger.Warn("stage arg sanitized or truncated before stage input",
			append(pipelineLogArgs(p, idx), "command", cmdName,
				"truncated_args", truncatedNames, "sanitized_args", sanitizedNames)...)
	}

	return out
}

// BinaryWhitelist resolves and validates binary paths.
type BinaryWhitelist struct {
	Dirs []string // allowed directories (absolute paths)
}

// Resolve finds binary in the whitelist directories and returns the
// resolved absolute path. Returns an error if the binary is not found
// or resolves outside the whitelist.
func (w *BinaryWhitelist) Resolve(name string) (string, error) {
	for _, dir := range w.Dirs {
		candidate := filepath.Join(dir, name)
		info, err := os.Stat(candidate)
		if err != nil {
			continue
		}
		if info.IsDir() {
			continue
		}
		if info.Mode()&0o111 == 0 {
			continue
		}
		resolved, err := filepath.EvalSymlinks(candidate)
		if err != nil {
			return "", fmt.Errorf("resolve symlink %s: %w", candidate, err)
		}
		resolvedDir := filepath.Dir(resolved)
		allowed := false
		for _, d := range w.Dirs {
			if resolvedDir == d {
				allowed = true
				break
			}
		}
		if !allowed {
			return "", fmt.Errorf("binary %q resolves to %s which is outside the whitelist", name, resolved)
		}
		return resolved, nil
	}
	return "", fmt.Errorf("binary %q not found in whitelist dirs %v", name, w.Dirs)
}

// CLIRunner executes a command by running a whitelisted binary directly.
type CLIRunner struct {
	Whitelist *BinaryWhitelist
}

// Run executes a single-binary CLI command and returns its stdout plus
// whether THIS call's own output was truncated at the 1MB cap -- the one
// place that decides it (see Runner's doc comment). CLIRunner never fences
// the incoming pipe's own truncation state (pipeTruncated) into anything a
// worker reads -- only ClaudeRunner's system prompt does -- but it is still
// attributed in the log lines below: a stage that fails to parse pipe as
// JSON, or that passes a truncated pipe straight through to a CLI binary,
// is a consequence of the cap upstream, not a bug in this stage, and the
// log line must say so rather than leave a reader to guess. p and idx are
// carried through to tag those same logs with which pipeline and stage
// produced them -- a daemon running several pipelines concurrently needs
// that correlation to tell one stage's output apart from another's in the
// log stream.
//
// call.Args are deliberately passed to the target binary uncapped and
// unsanitized, unlike ClaudeRunner.Run's args (buildStageArgs, which
// sanitizes and caps every value at maxContractFieldRunes before it ever
// reaches a worker's system prompt): a CLI binary is an operator-authored,
// signature-verified command (DES-034/DES-035) that reads its args as plain
// process arguments, not as untrusted text folded into a prompt an LLM
// reads and might be steered by -- there is no prompt-injection surface
// here for sanitizing or capping to close, and the binary's own argument
// parsing already bounds what it accepts.
func (r *CLIRunner) Run(ctx context.Context, e *Executor, p *Pipeline, idx int, cmd *Command, call CommandCall, pipe string, pipeTruncated bool) (string, bool, error) {
	if len(cmd.Steps) > 0 {
		return r.runCompound(ctx, e, p, idx, cmd, pipe)
	}

	resolvedPath, err := r.Whitelist.Resolve(cmd.Binary)
	if err != nil {
		return "", false, err
	}

	// Best-effort parse of pipe as JSON so declared args can fall back
	// to fields from the previous stage's output.
	var pipeFields map[string]any
	if trimmed := strings.TrimSpace(pipe); len(trimmed) > 0 && trimmed[0] == '{' {
		if err := json.Unmarshal([]byte(pipe), &pipeFields); err != nil {
			// Transient: this pipe value came from the prior stage's output
			// (possibly itself truncated), not from fixed configuration, so
			// the same command can succeed on the next run. pipeFields stays
			// nil, so every arg meant to be filled from the pipe is omitted
			// below rather than filled with a stale or partial value.
			// pipe_truncated is logged alongside the parse error so a cut
			// pipe value that fails to parse as JSON is attributed to the
			// cap, not read as unrelated malformed JSON.
			e.Logger.Warn("pipe value looked like JSON but failed to parse, args will not be filled from it",
				append(pipelineLogArgs(p, idx), "command", cmd.Name, "error", err, "pipe_truncated", pipeTruncated)...)
			pipeFields = nil
		}
	}

	args := make([]string, len(cmd.FixedArgs))
	copy(args, cmd.FixedArgs)

	type posArg struct {
		pos int
		val string
	}
	var positional []posArg
	var named []string

	for _, decl := range cmd.Args {
		val, ok := call.Args[decl.Name]
		// TODO(beadle-vjo): pipe-derived args bypass ValidateArgs type constraints.
		// Prior stage schema validation covers this for now. Add runtime validation
		// when arg types are enforced at execution time.
		if !ok && pipeFields != nil {
			val, ok = pipeFields[decl.Name]
		}
		if !ok {
			continue
		}
		s := fmt.Sprint(val)
		if decl.Position > 0 {
			positional = append(positional, posArg{decl.Position, s})
		} else {
			named = append(named, fmt.Sprintf("--%s=%s", decl.Name, s))
		}
	}
	sort.Slice(positional, func(i, j int) bool {
		return positional[i].pos < positional[j].pos
	})

	args = append(args, named...)
	for _, pa := range positional {
		args = append(args, pa.val)
	}

	env, err := envForCommand(cmd, r.Whitelist.Dirs)
	if err != nil {
		return "", false, err
	}

	timeout := parseTimeout(e, cmd)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	c := exec.CommandContext(ctx, resolvedPath, args...)
	c.Stdin = strings.NewReader(pipe)
	c.Env = env

	stderrBuf := &cappedWriter{max: 1 << 20}
	c.Stderr = stderrBuf

	stdoutPipe, err := c.StdoutPipe()
	if err != nil {
		return "", false, fmt.Errorf("stdout pipe: %w", err)
	}
	if err := c.Start(); err != nil {
		return "", false, fmt.Errorf("start %s: %w", cmd.Binary, err)
	}

	return runOutput(stdoutPipe, c.Wait, stderrBuf, e, cmd, p, idx)
}

// runOutput reads stdout (capped) and waits for the owning process to
// exit, logging any captured stderr exactly once regardless of outcome --
// stderr is often the most diagnostic thing available when the stdout read
// or the process itself fails, so it must not be skipped on an early
// return. wait must not be called until the stdout read completes: Wait
// closes the pipe once the process exits, and reading concurrently with or
// after that races (see exec.Cmd.StdoutPipe's doc comment).
//
// truncated is returned here, not stored on a shared field: this output
// never carries truncationMarker (round 3 of beadle-4gm1 removed it from
// every fenced value), so the return value is the only way the caller
// (CLIRunner.Run, then Executor.Run) can tell this value was cut. Round 4
// moved it off Pipeline entirely, after a shared resettable field proved
// to describe the wrong stage's output under a two-stage pipeline. p and
// idx are logging context only -- which pipeline and stage this truncation
// happened in -- and are never read for anything else; p may be nil (a
// caller with no pipeline in scope), in which case the pipeline/stage
// fields are simply omitted from the log line rather than panicking on a
// nil dereference.
func runOutput(stdoutPipe io.Reader, wait func() error, stderrBuf *cappedWriter, e *Executor, cmd *Command, p *Pipeline, idx int) (output string, truncated bool, err error) {
	data, extra, readErr := readCapped(stdoutPipe, 1<<20)
	waitErr := wait()

	if stderrBuf.buf.Len() > 0 {
		e.Logger.Info("cli command stderr",
			append(pipelineLogArgs(p, idx), "command", cmd.Name, "stderr", truncateLog(stderrBuf.buf.String(), 500))...)
	}

	if waitErr != nil {
		return "", false, fmt.Errorf("cli %s: %w", cmd.Binary, waitErr)
	}
	if readErr != nil {
		return "", false, fmt.Errorf("read stdout for %s: %w", cmd.Binary, readErr)
	}

	if extra > 0 {
		e.Logger.Warn("cli command stdout truncated at cap",
			append(pipelineLogArgs(p, idx), "command", cmd.Name, "captured_bytes", len(data), "discarded_bytes", extra)...)
		truncated = true
	}

	return string(data), truncated, nil
}

// pipelineLogArgs returns the "pipeline"/"stage" slog key-value pair for p
// and idx, or nil when p is nil -- the one caller with no pipeline in scope
// (a direct runOutput/runCompound test, or a future call site with none to
// give). slog accepts a nil []any as zero extra args, so callers can always
// append this unconditionally.
func pipelineLogArgs(p *Pipeline, idx int) []any {
	if p == nil {
		return nil
	}
	return []any{"pipeline", p.ID, "stage", idx}
}

// readCapped reads at most n bytes from r into data, then drains and
// discards whatever remains so a subsequent Wait on the owning exec.Cmd
// does not block waiting for r to close. extra reports how many bytes were
// discarded during the drain, so the caller can tell a real truncation
// (extra > 0) from a reader that simply had less than n bytes to give.
// A read error during the capped read is returned as err; the drain's own
// error, if any, is not -- best-effort cleanup, not the operation's result.
func readCapped(r io.Reader, n int) (data []byte, extra int64, err error) {
	data, err = io.ReadAll(io.LimitReader(r, int64(n)))
	if err != nil {
		_, _ = io.Copy(io.Discard, r)
		return data, 0, err
	}
	extra, _ = io.Copy(io.Discard, r)
	return data, extra, nil
}

// parseTimeout returns cmd's configured timeout, or 30s if none is set.
// An unparsable Timeout string is a permanent misconfiguration -- it will
// fail to parse identically on every run until the command file is
// corrected -- so it is logged at Error and the 30s default is used.
func parseTimeout(e *Executor, cmd *Command) time.Duration {
	const def = 30 * time.Second
	if cmd.Timeout == "" {
		return def
	}
	d, err := time.ParseDuration(cmd.Timeout)
	if err != nil {
		e.Logger.Error("invalid timeout in command definition, using default",
			"command", cmd.Name, "timeout", cmd.Timeout, "default", def, "error", err)
		return def
	}
	return d
}

// envForCommand builds the subprocess environment for cmd, failing closed
// when a declared env var is absent from the daemon's own environment:
// running the command without it would silently degrade behavior (see
// ClaudeRunner.Run's identical check above), and a missing MCP auth key
// that fails invisibly is exactly the failure mode that motivated it.
func envForCommand(cmd *Command, dirs []string) ([]string, error) {
	env, missing := minimalEnv(dirs, cmd.EnvVars)
	if len(missing) > 0 {
		return nil, fmt.Errorf("command %q declares env var(s) %v, absent from the "+
			"daemon's environment -- set them in the unit/plist that starts "+
			"beadle-daemon; running without them would silently degrade this "+
			"command", cmd.Name, missing)
	}
	return env, nil
}

// runCompound chains multiple binaries via io.Pipe, running all steps
// concurrently under a shared context timeout. truncated is returned true
// when the last step's output hits the 1MB cap, the same
// single-decision-point rule runOutput follows for the single-binary path.
// p and idx are logging context only; see pipelineLogArgs.
func (r *CLIRunner) runCompound(ctx context.Context, e *Executor, p *Pipeline, idx int, cmd *Command, pipe string) (string, bool, error) {
	timeout := parseTimeout(e, cmd)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	n := len(cmd.Steps)

	// Resolve all binaries before starting any goroutine.
	resolved := make([]string, n)
	for i, step := range cmd.Steps {
		p, err := r.Whitelist.Resolve(step.Binary)
		if err != nil {
			return "", false, fmt.Errorf("step[%d]: %w", i, err)
		}
		resolved[i] = p
	}

	// Create io.Pipe pairs between adjacent steps.
	pipeReaders := make([]*io.PipeReader, n-1)
	pipeWriters := make([]*io.PipeWriter, n-1)
	for i := 0; i < n-1; i++ {
		pipeReaders[i], pipeWriters[i] = io.Pipe()
	}

	// Build commands.
	env, err := envForCommand(cmd, r.Whitelist.Dirs)
	if err != nil {
		return "", false, err
	}

	// Capture the last step's stdout in a capped buffer rather than via
	// StdoutPipe. StdoutPipe's Wait closes the read end once the process
	// exits, so reading it concurrently with cmds[n-1].Wait() (below, in a
	// goroutine) races: under load the Wait can close the pipe before the
	// read finishes, yielding empty output. A cappedWriter never blocks and
	// is filled by the exec copy goroutine that Wait itself joins, so the
	// buffer is complete once wg.Wait returns.
	lastStdout := &cappedWriter{max: 1 << 20}

	cmds := make([]*exec.Cmd, n)
	stderrBufs := make([]*cappedWriter, n)
	for i, step := range cmd.Steps {
		c := exec.CommandContext(ctx, resolved[i], step.FixedArgs...)
		stderrBufs[i] = &cappedWriter{max: 1 << 20}
		c.Stderr = stderrBufs[i]
		c.Env = env

		if i == 0 {
			c.Stdin = strings.NewReader(pipe)
		} else {
			c.Stdin = pipeReaders[i-1]
		}

		if i < n-1 {
			c.Stdout = pipeWriters[i]
		} else {
			c.Stdout = lastStdout
		}

		cmds[i] = c
	}

	// Start all commands.
	for i, c := range cmds {
		if err := c.Start(); err != nil {
			cancel()
			// Close all pipe endpoints so started processes unblock.
			for j := 0; j < n-1; j++ {
				_ = pipeWriters[j].Close()
				_ = pipeReaders[j].Close()
			}
			// Wait on already-started processes (best-effort cleanup).
			for j := 0; j < i; j++ {
				_ = cmds[j].Wait()
			}
			return "", false, fmt.Errorf("step[%d] start %s: %w", i, cmd.Steps[i].Binary, err)
		}
	}

	// Wait for all steps in goroutines.
	var mu sync.Mutex
	var firstErr error
	var wg sync.WaitGroup

	for i := range cmds {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			err := cmds[i].Wait()
			if err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = fmt.Errorf("step[%d] (%s): %w", i, cmd.Steps[i].Binary, err)
					cancel()
				}
				mu.Unlock()
			}
			// Close our write end so the next step's read unblocks.
			if i < n-1 {
				_ = pipeWriters[i].Close()
			}
		}(i)
	}

	wg.Wait()

	// Log per-step stderr, warning separately if the cap dropped any of it.
	for i, buf := range stderrBufs {
		if dropped := buf.dropped(); dropped > 0 {
			e.Logger.Warn("compound step stderr truncated at cap",
				"command", cmd.Name, "step", i, "binary", cmd.Steps[i].Binary,
				"captured_bytes", buf.buf.Len(), "discarded_bytes", dropped)
		}
		if buf.buf.Len() > 0 {
			e.Logger.Info("compound step stderr",
				"command", cmd.Name,
				"step", i,
				"binary", cmd.Steps[i].Binary,
				"stderr", truncateLog(buf.buf.String(), 500))
		}
	}

	if firstErr != nil {
		return "", false, firstErr
	}

	result := lastStdout.buf.String()
	truncated := false
	if dropped := lastStdout.dropped(); dropped > 0 {
		e.Logger.Warn("compound command stdout truncated at cap",
			append(pipelineLogArgs(p, idx), "command", cmd.Name, "captured_bytes", len(result), "discarded_bytes", dropped)...)
		truncated = true
	}
	return result, truncated, nil
}

// cappedWriter is a bytes.Buffer that stops storing data after max bytes
// have been written, while still reporting the full length to its caller
// (exec needs Write to never return a short count, or it treats the short
// write as a fatal copy error and fails commands whose output merely
// exceeded the cap). total tracks every byte offered, so dropped can report
// exactly how much was cut instead of just that some was. Both are int64:
// total's whole job is detecting truncation, so it must not itself be able
// to wrap silently on a 32-bit int -- unreachable on the shipped platforms
// (darwin/linux, arm64/amd64, all 64-bit), but the cost of getting it right
// is nothing.
type cappedWriter struct {
	buf   bytes.Buffer
	max   int64
	total int64
}

func (w *cappedWriter) Write(p []byte) (int, error) {
	w.total += int64(len(p))
	remaining := w.max - int64(w.buf.Len())
	if remaining > 0 {
		n := int64(len(p))
		if n > remaining {
			n = remaining
		}
		w.buf.Write(p[:n])
	}
	return len(p), nil
}

// dropped returns how many bytes were offered beyond the cap.
func (w *cappedWriter) dropped() int64 {
	if w.total > w.max {
		return w.total - w.max
	}
	return 0
}

// minimalEnv builds an explicit environment for subprocess execution and
// reports which declared env vars had no value to resolve. It includes
// PATH (from whitelist dirs), HOME, USER, and any declared env vars the
// command definition allows and the daemon's environment actually has.
func minimalEnv(dirs []string, declaredVars []string) (env []string, missing []string) {
	env = []string{
		"PATH=" + strings.Join(dirs, ":"),
		"HOME=" + os.Getenv("HOME"),
		"USER=" + os.Getenv("USER"),
	}
	for _, name := range declaredVars {
		if v, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+v)
		} else {
			missing = append(missing, name)
		}
	}
	return env, missing
}
