package daemon

import (
	"bytes"
	"context"
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
	contract := buildStageContract(p.Email, cmd, call, pipe)

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
	defer os.Remove(mcpPath)

	promptPath, err := r.Templates.BuildSystemPrompt(missionID)
	if err != nil {
		return "", fmt.Errorf("build system prompt: %w", err)
	}
	defer os.Remove(promptPath)

	envOverrides := resolveEnvVars(cmd.EnvVars)

	wr, err := r.Spawner.Run(ctx, missionID, mcpPath, promptPath, envOverrides)
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
func (r *CLIRunner) Run(ctx context.Context, e *Executor, p *Pipeline, idx int, cmd *Command, call CommandCall, pipe string) (string, error) {
	if len(cmd.Steps) > 0 {
		return r.runCompound(ctx, e, cmd, pipe)
	}

	resolvedPath, err := r.Whitelist.Resolve(cmd.Binary)
	if err != nil {
		return "", err
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

	timeout := 30 * time.Second
	if cmd.Timeout != "" {
		if d, err := time.ParseDuration(cmd.Timeout); err == nil {
			timeout = d
		}
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	c := exec.CommandContext(ctx, resolvedPath, args...)
	c.Stdin = strings.NewReader(pipe)
	c.Env = minimalEnv(r.Whitelist.Dirs, cmd.EnvVars)

	stderrBuf := &cappedWriter{max: 1 << 20}
	c.Stderr = stderrBuf

	stdoutPipe, err := c.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("stdout pipe: %w", err)
	}
	if err := c.Start(); err != nil {
		return "", fmt.Errorf("start %s: %w", cmd.Binary, err)
	}

	output, _ := io.ReadAll(io.LimitReader(stdoutPipe, 1<<20))
	io.Copy(io.Discard, stdoutPipe) // drain remainder so Wait() won't hang

	if err := c.Wait(); err != nil {
		if stderrBuf.buf.Len() > 0 {
			e.Logger.Info("cli command stderr", "command", cmd.Name, "stderr", stderrBuf.buf.String())
		}
		return "", fmt.Errorf("cli %s: %w", cmd.Binary, err)
	}

	if stderrBuf.buf.Len() > 0 {
		e.Logger.Info("cli command stderr", "command", cmd.Name, "stderr", stderrBuf.buf.String())
	}

	return string(output), nil
}

// runCompound chains multiple binaries via io.Pipe, running all steps
// concurrently under a shared context timeout.
func (r *CLIRunner) runCompound(ctx context.Context, e *Executor, cmd *Command, pipe string) (string, error) {
	timeout := 30 * time.Second
	if cmd.Timeout != "" {
		if d, err := time.ParseDuration(cmd.Timeout); err == nil {
			timeout = d
		}
	}
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
	env := minimalEnv(r.Whitelist.Dirs, cmd.EnvVars)

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
		}
		// Last step's stdout is captured below.

		cmds[i] = c
	}

	// Capture last step's stdout.
	lastStdout, err := cmds[n-1].StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("step[%d] stdout pipe: %w", n-1, err)
	}

	// Start all commands.
	for i, c := range cmds {
		if err := c.Start(); err != nil {
			cancel()
			// Close all pipe endpoints so started processes unblock.
			for j := 0; j < n-1; j++ {
				pipeWriters[j].Close()
				pipeReaders[j].Close()
			}
			// Wait on already-started processes (best-effort cleanup).
			for j := 0; j < i; j++ {
				cmds[j].Wait()
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
				pipeWriters[i].Close()
			}
		}(i)
	}

	// Read final output while goroutines are running.
	output, _ := io.ReadAll(io.LimitReader(lastStdout, 1<<20))
	io.Copy(io.Discard, lastStdout) // drain remainder so Wait() won't hang
	wg.Wait()

	// Log per-step stderr.
	for i, buf := range stderrBufs {
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
	return string(output), nil
}

// cappedWriter is a bytes.Buffer that silently stops accepting data
// after max bytes have been written.
type cappedWriter struct {
	buf bytes.Buffer
	max int
}

func (w *cappedWriter) Write(p []byte) (int, error) {
	remaining := w.max - w.buf.Len()
	if remaining > 0 {
		if len(p) < remaining {
			remaining = len(p)
		}
		w.buf.Write(p[:remaining])
	}
	return len(p), nil
}

// minimalEnv builds an explicit environment for subprocess execution.
// It includes PATH (from whitelist dirs), HOME, USER, and any declared
// env vars the command definition allows.
func minimalEnv(dirs []string, declaredVars []string) []string {
	env := []string{
		"PATH=" + strings.Join(dirs, ":"),
		"HOME=" + os.Getenv("HOME"),
		"USER=" + os.Getenv("USER"),
	}
	for _, name := range declaredVars {
		if v, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+v)
		}
	}
	return env
}
