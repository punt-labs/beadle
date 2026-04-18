package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupWhitelist creates a temp dir with symlinks to real system binaries.
// The whitelist includes both the symlink dir and all resolved target dirs
// so that symlink resolution passes the whitelist check.
func setupWhitelist(t *testing.T, binaries ...string) (string, *BinaryWhitelist) {
	t.Helper()
	dir := t.TempDir()

	resolvedDirs := map[string]bool{dir: true}
	for _, name := range binaries {
		paths := []string{"/usr/bin/" + name, "/bin/" + name}
		var src string
		for _, p := range paths {
			if _, err := os.Stat(p); err == nil {
				src = p
				break
			}
		}
		if src == "" {
			t.Skipf("binary %q not found in /usr/bin or /bin", name)
		}
		dst := filepath.Join(dir, name)
		require.NoError(t, os.Symlink(src, dst))

		resolved, err := filepath.EvalSymlinks(src)
		require.NoError(t, err)
		resolvedDirs[filepath.Dir(resolved)] = true
	}

	dirs := make([]string, 0, len(resolvedDirs))
	for d := range resolvedDirs {
		dirs = append(dirs, d)
	}
	return dir, &BinaryWhitelist{Dirs: dirs}
}

func testPipeline() *Pipeline {
	return &Pipeline{
		ID:    "test-pipe",
		Email: EmailMeta{MessageID: "1", From: "test@test.com", Subject: "Test"},
	}
}

func TestBinaryWhitelist_Resolve(t *testing.T) {
	dir, wl := setupWhitelist(t, "echo", "cat")

	t.Run("found", func(t *testing.T) {
		path, err := wl.Resolve("echo")
		require.NoError(t, err)
		assert.Contains(t, path, "echo")
		// Resolved path should be absolute.
		assert.True(t, filepath.IsAbs(path))
	})

	t.Run("not found", func(t *testing.T) {
		_, err := wl.Resolve("nonexistent-binary-xyz")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found in whitelist")
	})

	t.Run("symlink outside whitelist", func(t *testing.T) {
		// Create a symlink that resolves outside the whitelist.
		outsideDir := t.TempDir()
		outsideBin := filepath.Join(outsideDir, "outside")
		require.NoError(t, os.WriteFile(outsideBin, []byte("#!/bin/sh\necho ok"), 0o755))

		link := filepath.Join(dir, "sneaky")
		require.NoError(t, os.Symlink(outsideBin, link))

		_, err := wl.Resolve("sneaky")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "outside the whitelist")
	})
}

func TestCLIRunner_Echo(t *testing.T) {
	_, wl := setupWhitelist(t, "echo")
	runner := &CLIRunner{Whitelist: wl}

	cmd := &Command{
		Name:         "test-echo",
		Runner:       "cli",
		Mode:         "process",
		Binary:       "echo",
		FixedArgs:    []string{"hello"},
		OutputSchema: "text",
		Timeout:      "5s",
	}
	call := CommandCall{Command: "test-echo", Args: map[string]any{}}
	p := testPipeline()

	result, err := runner.Run(context.Background(), &Executor{Logger: testLogger()}, p, 0, cmd, call, "")
	require.NoError(t, err)
	assert.Equal(t, "hello\n", result)
}

func TestCLIRunner_CatStdin(t *testing.T) {
	_, wl := setupWhitelist(t, "cat")
	runner := &CLIRunner{Whitelist: wl}

	cmd := &Command{
		Name:         "test-cat",
		Runner:       "cli",
		Mode:         "process",
		Binary:       "cat",
		OutputSchema: "text",
		Timeout:      "5s",
	}
	call := CommandCall{Command: "test-cat", Args: map[string]any{}}
	p := testPipeline()

	result, err := runner.Run(context.Background(), &Executor{Logger: testLogger()}, p, 0, cmd, call, "pipe data")
	require.NoError(t, err)
	assert.Equal(t, "pipe data", result)
}

func TestCLIRunner_NonzeroExit(t *testing.T) {
	_, wl := setupWhitelist(t, "false")
	runner := &CLIRunner{Whitelist: wl}

	cmd := &Command{
		Name:         "test-false",
		Runner:       "cli",
		Mode:         "process",
		Binary:       "false",
		OutputSchema: "text",
		Timeout:      "5s",
	}
	call := CommandCall{Command: "test-false", Args: map[string]any{}}
	p := testPipeline()

	_, err := runner.Run(context.Background(), &Executor{Logger: testLogger()}, p, 0, cmd, call, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cli false")
}

func TestCLIRunner_BinaryNotInWhitelist(t *testing.T) {
	wl := &BinaryWhitelist{Dirs: []string{t.TempDir()}}
	runner := &CLIRunner{Whitelist: wl}

	cmd := &Command{
		Name:         "test-nope",
		Runner:       "cli",
		Mode:         "process",
		Binary:       "echo",
		OutputSchema: "text",
	}
	call := CommandCall{Command: "test-nope", Args: map[string]any{}}
	p := testPipeline()

	_, err := runner.Run(context.Background(), &Executor{Logger: testLogger()}, p, 0, cmd, call, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found in whitelist")
}

func TestCLIRunner_ArgAssembly(t *testing.T) {
	// Use echo to verify arg ordering: fixed_args, named flags, positional.
	_, wl := setupWhitelist(t, "echo")
	runner := &CLIRunner{Whitelist: wl}

	cmd := &Command{
		Name:      "test-args",
		Runner:    "cli",
		Mode:      "process",
		Binary:    "echo",
		FixedArgs: []string{"-n"},
		Args: []CommandArg{
			{Name: "flag", Type: "string"},
			{Name: "first", Type: "string", Position: 1},
			{Name: "second", Type: "string", Position: 2},
		},
		OutputSchema: "text",
		Timeout:      "5s",
	}
	call := CommandCall{
		Command: "test-args",
		Args: map[string]any{
			"flag":   "val",
			"first":  "A",
			"second": "B",
		},
	}
	p := testPipeline()

	result, err := runner.Run(context.Background(), &Executor{Logger: testLogger()}, p, 0, cmd, call, "")
	require.NoError(t, err)
	// echo -n --flag=val A B
	assert.Equal(t, "--flag=val A B", result)
}

func TestCLIRunner_Timeout(t *testing.T) {
	_, wl := setupWhitelist(t, "sleep")
	runner := &CLIRunner{Whitelist: wl}

	cmd := &Command{
		Name:         "test-timeout",
		Runner:       "cli",
		Mode:         "process",
		Binary:       "sleep",
		FixedArgs:    []string{"60"},
		OutputSchema: "text",
		Timeout:      "100ms",
	}
	call := CommandCall{Command: "test-timeout", Args: map[string]any{}}
	p := testPipeline()

	start := time.Now()
	_, err := runner.Run(context.Background(), &Executor{Logger: testLogger()}, p, 0, cmd, call, "")
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.Less(t, elapsed, 5*time.Second)
}

func TestCLIRunner_OutputCap(t *testing.T) {
	// Use dd to produce >1MB of output and verify truncation.
	_, wl := setupWhitelist(t, "dd")
	runner := &CLIRunner{Whitelist: wl}

	cmd := &Command{
		Name:         "test-cap",
		Runner:       "cli",
		Mode:         "process",
		Binary:       "dd",
		FixedArgs:    []string{"if=/dev/zero", "bs=1048577", "count=1"},
		OutputSchema: "text",
		Timeout:      "5s",
	}
	call := CommandCall{Command: "test-cap", Args: map[string]any{}}
	p := testPipeline()

	result, err := runner.Run(context.Background(), &Executor{Logger: testLogger()}, p, 0, cmd, call, "")
	require.NoError(t, err)
	assert.Equal(t, 1<<20, len(result))
}

func TestCLIRunner_CompoundTwoSteps(t *testing.T) {
	_, wl := setupWhitelist(t, "echo", "cat")
	runner := &CLIRunner{Whitelist: wl}

	cmd := &Command{
		Name:   "test-compound",
		Runner: "cli",
		Mode:   "process",
		Steps: []Step{
			{Binary: "echo", FixedArgs: []string{"-n", "hello"}, Stdin: "pipe"},
			{Binary: "cat", Stdin: "stdout"},
		},
		OutputSchema: "text",
		Timeout:      "5s",
	}
	call := CommandCall{Command: "test-compound", Args: map[string]any{}}
	p := testPipeline()

	result, err := runner.Run(context.Background(), &Executor{Logger: testLogger()}, p, 0, cmd, call, "input data")
	require.NoError(t, err)
	assert.Equal(t, "hello", result)
}

func TestCLIRunner_CompoundThreeSteps(t *testing.T) {
	_, wl := setupWhitelist(t, "echo", "cat", "tr")
	runner := &CLIRunner{Whitelist: wl}

	cmd := &Command{
		Name:   "test-three",
		Runner: "cli",
		Mode:   "process",
		Steps: []Step{
			{Binary: "echo", FixedArgs: []string{"-n", "hello world"}, Stdin: "pipe"},
			{Binary: "cat", Stdin: "stdout"},
			{Binary: "tr", FixedArgs: []string{"a-z", "A-Z"}, Stdin: "stdout"},
		},
		OutputSchema: "text",
		Timeout:      "5s",
	}
	call := CommandCall{Command: "test-three", Args: map[string]any{}}
	p := testPipeline()

	result, err := runner.Run(context.Background(), &Executor{Logger: testLogger()}, p, 0, cmd, call, "")
	require.NoError(t, err)
	assert.Equal(t, "HELLO WORLD", result)
}

func TestCLIRunner_CompoundFirstStepFails(t *testing.T) {
	_, wl := setupWhitelist(t, "false", "cat")
	runner := &CLIRunner{Whitelist: wl}

	cmd := &Command{
		Name:   "test-fail-first",
		Runner: "cli",
		Mode:   "process",
		Steps: []Step{
			{Binary: "false", Stdin: "pipe"},
			{Binary: "cat", Stdin: "stdout"},
		},
		OutputSchema: "text",
		Timeout:      "5s",
	}
	call := CommandCall{Command: "test-fail-first", Args: map[string]any{}}
	p := testPipeline()

	_, err := runner.Run(context.Background(), &Executor{Logger: testLogger()}, p, 0, cmd, call, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "step[0]")
}

func TestCLIRunner_CompoundLastStepFails(t *testing.T) {
	_, wl := setupWhitelist(t, "echo", "false")
	runner := &CLIRunner{Whitelist: wl}

	cmd := &Command{
		Name:   "test-fail-last",
		Runner: "cli",
		Mode:   "process",
		Steps: []Step{
			{Binary: "echo", FixedArgs: []string{"hello"}, Stdin: "pipe"},
			{Binary: "false", Stdin: "stdout"},
		},
		OutputSchema: "text",
		Timeout:      "5s",
	}
	call := CommandCall{Command: "test-fail-last", Args: map[string]any{}}
	p := testPipeline()

	_, err := runner.Run(context.Background(), &Executor{Logger: testLogger()}, p, 0, cmd, call, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "step[1]")
}

func TestCLIRunner_CompoundTimeout(t *testing.T) {
	_, wl := setupWhitelist(t, "sleep", "cat")
	runner := &CLIRunner{Whitelist: wl}

	cmd := &Command{
		Name:   "test-timeout-compound",
		Runner: "cli",
		Mode:   "process",
		Steps: []Step{
			{Binary: "sleep", FixedArgs: []string{"60"}, Stdin: "pipe"},
			{Binary: "cat", Stdin: "stdout"},
		},
		OutputSchema: "text",
		Timeout:      "100ms",
	}
	call := CommandCall{Command: "test-timeout-compound", Args: map[string]any{}}
	p := testPipeline()

	start := time.Now()
	_, err := runner.Run(context.Background(), &Executor{Logger: testLogger()}, p, 0, cmd, call, "")
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.Less(t, elapsed, 5*time.Second)
}

func TestCLIRunner_EnvIsolation(t *testing.T) {
	// Set a sentinel env var and verify the CLI subprocess does NOT see it.
	t.Setenv("BEADLE_TEST_SENTINEL", "leaked")

	_, wl := setupWhitelist(t, "env")
	runner := &CLIRunner{Whitelist: wl}

	cmd := &Command{
		Name:         "test-env",
		Runner:       "cli",
		Mode:         "process",
		Binary:       "env",
		OutputSchema: "text",
		Timeout:      "5s",
	}
	call := CommandCall{Command: "test-env", Args: map[string]any{}}
	p := testPipeline()

	result, err := runner.Run(context.Background(), &Executor{Logger: testLogger()}, p, 0, cmd, call, "")
	require.NoError(t, err)

	assert.NotContains(t, result, "BEADLE_TEST_SENTINEL", "subprocess must not inherit daemon env")
	assert.Contains(t, result, "PATH=", "subprocess must have PATH")
	assert.Contains(t, result, "HOME=", "subprocess must have HOME")
	assert.Contains(t, result, "USER=", "subprocess must have USER")
}

func TestCLIRunner_EnvDeclaredVars(t *testing.T) {
	// Verify that declared env_vars are passed through.
	t.Setenv("BEADLE_ALLOWED_VAR", "included")
	t.Setenv("BEADLE_BLOCKED_VAR", "excluded")

	_, wl := setupWhitelist(t, "env")
	runner := &CLIRunner{Whitelist: wl}

	cmd := &Command{
		Name:         "test-env-declared",
		Runner:       "cli",
		Mode:         "process",
		Binary:       "env",
		EnvVars:      []string{"BEADLE_ALLOWED_VAR"},
		OutputSchema: "text",
		Timeout:      "5s",
	}
	call := CommandCall{Command: "test-env-declared", Args: map[string]any{}}
	p := testPipeline()

	result, err := runner.Run(context.Background(), &Executor{Logger: testLogger()}, p, 0, cmd, call, "")
	require.NoError(t, err)

	assert.Contains(t, result, "BEADLE_ALLOWED_VAR=included", "declared var must be present")
	assert.NotContains(t, result, "BEADLE_BLOCKED_VAR", "undeclared var must not leak")
}

func TestCLIRunner_CompoundPipeStdin(t *testing.T) {
	// Verify that step[0] receives the pipe data on stdin.
	_, wl := setupWhitelist(t, "cat", "tr")
	runner := &CLIRunner{Whitelist: wl}

	cmd := &Command{
		Name:   "test-pipe-stdin",
		Runner: "cli",
		Mode:   "process",
		Steps: []Step{
			{Binary: "cat", Stdin: "pipe"},
			{Binary: "tr", FixedArgs: []string{"a-z", "A-Z"}, Stdin: "stdout"},
		},
		OutputSchema: "text",
		Timeout:      "5s",
	}
	call := CommandCall{Command: "test-pipe-stdin", Args: map[string]any{}}
	p := testPipeline()

	result, err := runner.Run(context.Background(), &Executor{Logger: testLogger()}, p, 0, cmd, call, "pipe data")
	require.NoError(t, err)
	assert.Equal(t, "PIPE DATA", result)
}

func TestCLIRunner_ArgsFromPipe(t *testing.T) {
	_, wl := setupWhitelist(t, "echo")
	runner := &CLIRunner{Whitelist: wl}

	cmd := &Command{
		Name:   "test-pipe-args",
		Runner: "cli",
		Mode:   "process",
		Binary: "echo",
		FixedArgs: []string{"-n"},
		Args: []CommandArg{
			{Name: "title", Type: "string"},
			{Name: "type", Type: "string"},
		},
		OutputSchema: "text",
		Timeout:      "5s",
	}
	call := CommandCall{Command: "test-pipe-args", Args: map[string]any{}}
	p := testPipeline()

	pipe := `{"title": "Fix auth", "type": "task"}`
	result, err := runner.Run(context.Background(), &Executor{Logger: testLogger()}, p, 0, cmd, call, pipe)
	require.NoError(t, err)
	assert.Contains(t, result, "--title=Fix auth")
	assert.Contains(t, result, "--type=task")
}

func TestCLIRunner_ArgsPlannerOverridesPipe(t *testing.T) {
	_, wl := setupWhitelist(t, "echo")
	runner := &CLIRunner{Whitelist: wl}

	cmd := &Command{
		Name:   "test-override",
		Runner: "cli",
		Mode:   "process",
		Binary: "echo",
		FixedArgs: []string{"-n"},
		Args: []CommandArg{
			{Name: "title", Type: "string"},
		},
		OutputSchema: "text",
		Timeout:      "5s",
	}
	call := CommandCall{
		Command: "test-override",
		Args:    map[string]any{"title": "Override"},
	}
	p := testPipeline()

	pipe := `{"title": "From pipe"}`
	result, err := runner.Run(context.Background(), &Executor{Logger: testLogger()}, p, 0, cmd, call, pipe)
	require.NoError(t, err)
	assert.Contains(t, result, "--title=Override")
	assert.NotContains(t, result, "From pipe")
}

func TestCLIRunner_ArgsFromPipe_InvalidJSON(t *testing.T) {
	_, wl := setupWhitelist(t, "echo")
	runner := &CLIRunner{Whitelist: wl}

	cmd := &Command{
		Name:   "test-bad-json",
		Runner: "cli",
		Mode:   "process",
		Binary: "echo",
		FixedArgs: []string{"-n"},
		Args: []CommandArg{
			{Name: "title", Type: "string"},
		},
		OutputSchema: "text",
		Timeout:      "5s",
	}
	call := CommandCall{
		Command: "test-bad-json",
		Args:    map[string]any{"title": "FromArgs"},
	}
	p := testPipeline()

	// Pipe is not valid JSON — should not crash, args from call.Args only.
	pipe := "this is not json"
	result, err := runner.Run(context.Background(), &Executor{Logger: testLogger()}, p, 0, cmd, call, pipe)
	require.NoError(t, err)
	assert.Contains(t, result, "--title=FromArgs")
}
