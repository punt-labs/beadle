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
type Runner interface {
	Run(ctx context.Context, e *Executor, p *Pipeline, idx int, cmd *Command, call CommandCall, pipe string) (string, error)
}

// ClaudeRunner executes a command via a Claude Code worker session.
type ClaudeRunner struct {
	Spawner   Spawner
	Missions  MissionCreator
	Templates *MissionTemplate
	Registry  map[string]MCPServerConfig
}

// Run creates a mission from the stage contract, spawns a Claude worker, and returns the output.
func (r *ClaudeRunner) Run(ctx context.Context, e *Executor, p *Pipeline, idx int, cmd *Command, call CommandCall, pipe string) (string, error) {
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
	// recall.
	envOverrides, missingEnv := resolveEnvVars(cmd.EnvVars)
	if len(missingEnv) > 0 {
		return "", fmt.Errorf("command %q declares env var(s) %v, absent from the "+
			"daemon's environment -- set them in the unit/plist that starts "+
			"beadle-daemon; running without them would silently degrade this "+
			"command", cmd.Name, missingEnv)
	}

	contract := buildStageContract(p.Email, cmd, call, pipe, p.Body)

	missionID, err := createMissionFromContract(r.Templates.TmpDir, contract)
	if err != nil {
		return "", fmt.Errorf("create stage mission: %w", err)
	}

	e.Logger.Info("stage mission created",
		"pipeline", p.ID, "stage", idx,
		"command", call.Command, "mission", missionID)

	servers := cmd.MCPServers
	if len(servers) == 0 {
		servers = []string{"ethos", "beadle-email"}
	}
	mcpPath, err := r.Templates.BuildMCPConfig(servers, r.Registry)
	if err != nil {
		return "", fmt.Errorf("build mcp config: %w", err)
	}
	defer func() { _ = os.Remove(mcpPath) }()

	promptPath, err := r.Templates.BuildSystemPrompt(missionID)
	if err != nil {
		return "", fmt.Errorf("build system prompt: %w", err)
	}
	defer func() { _ = os.Remove(promptPath) }()

	wr, err := r.Spawner.Run(ctx, missionID, mcpPath, promptPath, cmd.Tools, envOverrides)
	if err != nil {
		return "", fmt.Errorf("spawn worker: %w", err)
	}
	if wr.IsError {
		return "", fmt.Errorf("worker error (exit %d): %s", wr.ExitCode, wr.Output)
	}

	closeOut, closeErr := exec.CommandContext(ctx, "ethos", "mission", "close", missionID).CombinedOutput()
	if closeErr != nil {
		e.Logger.Warn("close stage mission", "mission", missionID, "error", closeErr, "output", string(closeOut))
	}

	e.Logger.Info("stage completed",
		"pipeline", p.ID, "stage", idx,
		"command", call.Command, "mission", missionID)

	return wr.Output, nil
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

// Run executes a single-binary CLI command and returns its stdout.
func (r *CLIRunner) Run(ctx context.Context, e *Executor, _ *Pipeline, _ int, cmd *Command, call CommandCall, pipe string) (string, error) {
	if len(cmd.Steps) > 0 {
		return r.runCompound(ctx, e, cmd, pipe)
	}

	resolvedPath, err := r.Whitelist.Resolve(cmd.Binary)
	if err != nil {
		return "", err
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
			e.Logger.Warn("pipe value looked like JSON but failed to parse, args will not be filled from it",
				"command", cmd.Name, "error", err)
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
		return "", err
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
		return "", fmt.Errorf("stdout pipe: %w", err)
	}
	if err := c.Start(); err != nil {
		return "", fmt.Errorf("start %s: %w", cmd.Binary, err)
	}

	return runOutput(stdoutPipe, c.Wait, stderrBuf, e, cmd)
}

// runOutput reads stdout (capped) and waits for the owning process to
// exit, logging any captured stderr exactly once regardless of outcome --
// stderr is often the most diagnostic thing available when the stdout read
// or the process itself fails, so it must not be skipped on an early
// return. wait must not be called until the stdout read completes: Wait
// closes the pipe once the process exits, and reading concurrently with or
// after that races (see exec.Cmd.StdoutPipe's doc comment).
func runOutput(stdoutPipe io.Reader, wait func() error, stderrBuf *cappedWriter, e *Executor, cmd *Command) (string, error) {
	output, extra, readErr := readCapped(stdoutPipe, 1<<20)
	waitErr := wait()

	if stderrBuf.buf.Len() > 0 {
		e.Logger.Info("cli command stderr", "command", cmd.Name, "stderr", stderrBuf.buf.String())
	}

	if waitErr != nil {
		return "", fmt.Errorf("cli %s: %w", cmd.Binary, waitErr)
	}
	if readErr != nil {
		return "", fmt.Errorf("read stdout for %s: %w", cmd.Binary, readErr)
	}

	if extra > 0 {
		e.Logger.Warn("cli command stdout truncated at cap",
			"command", cmd.Name, "captured_bytes", len(output), "discarded_bytes", extra)
		output = append(output, []byte(truncationMarker)...)
	}

	return string(output), nil
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
// concurrently under a shared context timeout.
func (r *CLIRunner) runCompound(ctx context.Context, e *Executor, cmd *Command, pipe string) (string, error) {
	timeout := parseTimeout(e, cmd)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	n := len(cmd.Steps)

	// Resolve all binaries before starting any goroutine.
	resolved := make([]string, n)
	for i, step := range cmd.Steps {
		p, err := r.Whitelist.Resolve(step.Binary)
		if err != nil {
			return "", fmt.Errorf("step[%d]: %w", i, err)
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
		return "", err
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
			return "", fmt.Errorf("step[%d] start %s: %w", i, cmd.Steps[i].Binary, err)
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
		return "", firstErr
	}

	result := lastStdout.buf.String()
	if dropped := lastStdout.dropped(); dropped > 0 {
		e.Logger.Warn("compound command stdout truncated at cap",
			"command", cmd.Name, "captured_bytes", len(result), "discarded_bytes", dropped)
		result += truncationMarker
	}
	return result, nil
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
