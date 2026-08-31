package daemon

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
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
	// Use dd to produce >1MB of output and verify truncation is capped and
	// marked -- a downstream reader (including an LLM worker reading the
	// mission contract's context field) must be able to tell the value was
	// cut rather than silently trusting a partial value as complete.
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
	assert.Equal(t, 1<<20+len(truncationMarker), len(result))
	assert.True(t, strings.HasSuffix(result, truncationMarker))
}

// TestCLIRunner_OutputCap_LogsTruncation is the regression test for
// beadle-k1g defect 1: truncating the 1MB stdout cap used to be completely
// silent, with the io.ReadAll error discarded too. It must now log the
// command name and the number of bytes actually dropped.
func TestCLIRunner_OutputCap_LogsTruncation(t *testing.T) {
	_, wl := setupWhitelist(t, "dd")
	runner := &CLIRunner{Whitelist: wl}
	logger, buf := testLoggerCapture()

	cmd := &Command{
		Name:         "test-cap-logged",
		Runner:       "cli",
		Mode:         "process",
		Binary:       "dd",
		FixedArgs:    []string{"if=/dev/zero", "bs=1048577", "count=1"},
		OutputSchema: "text",
		Timeout:      "5s",
	}
	call := CommandCall{Command: "test-cap-logged", Args: map[string]any{}}
	p := testPipeline()

	_, err := runner.Run(context.Background(), &Executor{Logger: logger}, p, 0, cmd, call, "")
	require.NoError(t, err)

	line := logLineContaining(t, buf, "cli command stdout truncated")
	assert.Contains(t, line, "level=WARN")
	assert.Contains(t, line, "test-cap-logged")
	assert.Contains(t, line, "discarded_bytes=1")
}

// errAfter is an io.Reader that yields data once, then always fails with
// err. It stands in for a stdout pipe whose underlying read fails partway
// through -- something the real dd/cat/echo binaries used elsewhere in this
// file cannot be made to trigger deterministically.
type errAfter struct {
	data []byte
	err  error
}

func (r *errAfter) Read(p []byte) (int, error) {
	if len(r.data) > 0 {
		n := copy(p, r.data)
		r.data = r.data[n:]
		return n, nil
	}
	return 0, r.err
}

// TestReadCapped_PropagatesReadError is the regression test for beadle-k1g
// defect 1's second half: prior to the fix, CLIRunner.Run discarded the
// io.ReadAll error outright (`output, _ := io.ReadAll(...)`), returning
// whatever partial data had been read as if the command had succeeded.
func TestReadCapped_PropagatesReadError(t *testing.T) {
	wantErr := errors.New("pipe read boom")
	r := &errAfter{data: []byte("partial output"), err: wantErr}

	data, extra, err := readCapped(r, 1<<20)

	require.ErrorIs(t, err, wantErr)
	assert.Equal(t, "partial output", string(data))
	assert.Zero(t, extra)
}

// TestRunOutput_LogsStderrOnReadError is the regression test for the
// Copilot finding on beadle-k1g's PR #260: the read-error early return
// added by that fix skipped the stderr logging below it, dropping the most
// diagnostic thing available at the moment stdout capture fails. stderr
// must be logged exactly once, on every path, before any return.
func TestRunOutput_LogsStderrOnReadError(t *testing.T) {
	logger, buf := testLoggerCapture()
	stderrBuf := &cappedWriter{max: 1 << 20}
	_, writeErr := stderrBuf.Write([]byte("boom: disk full"))
	require.NoError(t, writeErr)

	e := &Executor{Logger: logger}
	cmd := &Command{Name: "test-stderr-on-read-error", Binary: "false"}
	r := &errAfter{err: errors.New("pipe read boom")}

	_, err := runOutput(r, func() error { return nil }, stderrBuf, e, cmd)
	require.Error(t, err)

	line := logLineContaining(t, buf, "boom: disk full")
	assert.Contains(t, line, "test-stderr-on-read-error")
}

// TestRunOutput_LogsStderrOnWaitError is TestRunOutput_LogsStderrOnReadError
// for the sibling early return: c.Wait() failing. This path already logged
// stderr before the fix, but the two branches duplicated the same log call
// -- now unified into runOutput's single log site -- so this guards against
// a future edit reintroducing the split and dropping one copy.
func TestRunOutput_LogsStderrOnWaitError(t *testing.T) {
	logger, buf := testLoggerCapture()
	stderrBuf := &cappedWriter{max: 1 << 20}
	_, writeErr := stderrBuf.Write([]byte("exit status 1"))
	require.NoError(t, writeErr)

	e := &Executor{Logger: logger}
	cmd := &Command{Name: "test-stderr-on-wait-error", Binary: "false"}
	r := strings.NewReader("")

	_, err := runOutput(r, func() error { return errors.New("wait boom") }, stderrBuf, e, cmd)
	require.Error(t, err)

	line := logLineContaining(t, buf, "exit status 1")
	assert.Contains(t, line, "test-stderr-on-wait-error")
}

// TestReadCapped_ReportsExtraOnlyWhenTruncated confirms extra is 0 (no
// truncation) when the reader has less data than the cap, and > 0 (real
// truncation) when it has more -- the distinction TestCLIRunner_OutputCap
// depends on to decide whether to append truncationMarker.
func TestReadCapped_ReportsExtraOnlyWhenTruncated(t *testing.T) {
	t.Run("under cap", func(t *testing.T) {
		data, extra, err := readCapped(strings.NewReader("short"), 1<<20)
		require.NoError(t, err)
		assert.Equal(t, "short", string(data))
		assert.Zero(t, extra)
	})

	t.Run("over cap", func(t *testing.T) {
		data, extra, err := readCapped(strings.NewReader("abcdef"), 4)
		require.NoError(t, err)
		assert.Equal(t, "abcd", string(data))
		assert.Equal(t, int64(2), extra)
	})
}

// TestCLIRunner_InvalidTimeoutLogged is the regression test for beadle-k1g
// defect 5: an unparsable cmd.Timeout ("5min" is not a valid Go duration --
// "5m" is) used to fall back to the 30s default with no record of why. This
// is a permanent misconfiguration in the command file, so it must log at
// Error: retrying the same signed command file will fail identically forever.
func TestCLIRunner_InvalidTimeoutLogged(t *testing.T) {
	_, wl := setupWhitelist(t, "echo")
	runner := &CLIRunner{Whitelist: wl}
	logger, buf := testLoggerCapture()

	cmd := &Command{
		Name:         "test-bad-timeout",
		Runner:       "cli",
		Mode:         "process",
		Binary:       "echo",
		FixedArgs:    []string{"hi"},
		OutputSchema: "text",
		Timeout:      "5min", // invalid: Go wants "5m"
	}
	call := CommandCall{Command: "test-bad-timeout", Args: map[string]any{}}
	p := testPipeline()

	result, err := runner.Run(context.Background(), &Executor{Logger: logger}, p, 0, cmd, call, "")
	require.NoError(t, err)
	assert.Equal(t, "hi\n", result)

	line := logLineContaining(t, buf, "invalid timeout in command definition")
	assert.Contains(t, line, "level=ERROR")
	assert.Contains(t, line, "test-bad-timeout")
	assert.Contains(t, line, "5min")
}

// TestCLIRunner_CompoundInvalidTimeoutLogged is TestCLIRunner_InvalidTimeoutLogged
// for the compound (multi-step) path, which parses cmd.Timeout independently.
func TestCLIRunner_CompoundInvalidTimeoutLogged(t *testing.T) {
	_, wl := setupWhitelist(t, "echo", "cat")
	runner := &CLIRunner{Whitelist: wl}
	logger, buf := testLoggerCapture()

	cmd := &Command{
		Name:   "test-compound-bad-timeout",
		Runner: "cli",
		Mode:   "process",
		Steps: []Step{
			{Binary: "echo", FixedArgs: []string{"-n", "hello"}, Stdin: "pipe"},
			{Binary: "cat", Stdin: "stdout"},
		},
		OutputSchema: "text",
		Timeout:      "5min",
	}
	call := CommandCall{Command: "test-compound-bad-timeout", Args: map[string]any{}}
	p := testPipeline()

	result, err := runner.Run(context.Background(), &Executor{Logger: logger}, p, 0, cmd, call, "")
	require.NoError(t, err)
	assert.Equal(t, "hello", result)

	line := logLineContaining(t, buf, "invalid timeout in command definition")
	assert.Contains(t, line, "level=ERROR")
	assert.Contains(t, line, "test-compound-bad-timeout")
}

// TestCLIRunner_MissingDeclaredEnvVarLogged is the regression test for
// beadle-k1g defect 3: a declared env_vars entry absent from the daemon's
// own environment used to be skipped with no record anywhere. This is a
// permanent misconfiguration (the deployment, not the request, is missing
// the var), so it must log at Error.
func TestCLIRunner_MissingDeclaredEnvVarLogged(t *testing.T) {
	_, wl := setupWhitelist(t, "env")
	runner := &CLIRunner{Whitelist: wl}
	logger, buf := testLoggerCapture()

	cmd := &Command{
		Name:         "test-missing-env",
		Runner:       "cli",
		Mode:         "process",
		Binary:       "env",
		EnvVars:      []string{"BEADLE_K1G_DEFINITELY_UNSET_VAR"},
		OutputSchema: "text",
		Timeout:      "5s",
	}
	call := CommandCall{Command: "test-missing-env", Args: map[string]any{}}
	p := testPipeline()

	_, err := runner.Run(context.Background(), &Executor{Logger: logger}, p, 0, cmd, call, "")
	require.NoError(t, err)

	line := logLineContaining(t, buf, "BEADLE_K1G_DEFINITELY_UNSET_VAR")
	assert.Contains(t, line, "level=ERROR")
	assert.Contains(t, line, "test-missing-env")
}

// TestCLIRunner_CompoundMissingDeclaredEnvVarLogged is
// TestCLIRunner_MissingDeclaredEnvVarLogged for the compound path.
func TestCLIRunner_CompoundMissingDeclaredEnvVarLogged(t *testing.T) {
	_, wl := setupWhitelist(t, "env", "cat")
	runner := &CLIRunner{Whitelist: wl}
	logger, buf := testLoggerCapture()

	cmd := &Command{
		Name:    "test-compound-missing-env",
		Runner:  "cli",
		Mode:    "process",
		EnvVars: []string{"BEADLE_K1G_DEFINITELY_UNSET_VAR"},
		Steps: []Step{
			{Binary: "env", Stdin: "pipe"},
			{Binary: "cat", Stdin: "stdout"},
		},
		OutputSchema: "text",
		Timeout:      "5s",
	}
	call := CommandCall{Command: "test-compound-missing-env", Args: map[string]any{}}
	p := testPipeline()

	_, err := runner.Run(context.Background(), &Executor{Logger: logger}, p, 0, cmd, call, "")
	require.NoError(t, err)

	line := logLineContaining(t, buf, "BEADLE_K1G_DEFINITELY_UNSET_VAR")
	assert.Contains(t, line, "level=ERROR")
	assert.Contains(t, line, "test-compound-missing-env")
}

// TestCLIRunner_ArgsFromPipe_MalformedJSON is the regression test for
// beadle-k1g defect 4: a pipe value that looks like JSON (starts with '{')
// but fails to parse -- e.g. truncated by defect 1's 1MB cap -- used to be
// swallowed by `_ = json.Unmarshal(...)`, silently omitting every arg that
// should have been filled from the pipe with no record of why.
func TestCLIRunner_ArgsFromPipe_MalformedJSON(t *testing.T) {
	_, wl := setupWhitelist(t, "echo")
	runner := &CLIRunner{Whitelist: wl}
	logger, buf := testLoggerCapture()

	cmd := &Command{
		Name:      "test-malformed-json",
		Runner:    "cli",
		Mode:      "process",
		Binary:    "echo",
		FixedArgs: []string{"-n"},
		Args: []CommandArg{
			{Name: "title", Type: "string"},
		},
		OutputSchema: "text",
		Timeout:      "5s",
	}
	call := CommandCall{Command: "test-malformed-json", Args: map[string]any{}}
	p := testPipeline()

	// Starts with '{' so the pipe-as-JSON path is taken, but the JSON is
	// truncated mid-value and fails to parse.
	pipe := `{"title": "Fix auth`
	result, err := runner.Run(context.Background(), &Executor{Logger: logger}, p, 0, cmd, call, pipe)
	require.NoError(t, err)
	assert.Empty(t, result) // no args filled: title was never resolved

	line := logLineContaining(t, buf, "pipe value looked like JSON but failed to parse")
	assert.Contains(t, line, "level=WARN")
	assert.Contains(t, line, "test-malformed-json")
}

// TestCappedWriter_Dropped_Beyond32BitRange is the regression test for the
// Copilot finding on beadle-k1g's PR #260: total (and max) must be int64,
// not int, because total's entire job is detecting truncation -- on a
// 32-bit build an int-typed total could wrap past math.MaxInt32 and
// dropped() would return 0 or negative, silently suppressing exactly the
// truncation warning and marker it exists to trigger. The shipped
// platforms (darwin/linux, arm64/amd64) are all 64-bit, where Go's int
// already matches int64, so this scenario cannot actually occur in
// production today and this test cannot fail on this host either way --
// it documents and locks in the wider type rather than reproducing a
// live bug.
func TestCappedWriter_Dropped_Beyond32BitRange(t *testing.T) {
	w := &cappedWriter{max: 1 << 20}
	w.total = int64(math.MaxInt32) + 1<<20 + 100

	assert.Equal(t, int64(math.MaxInt32)+100, w.dropped())
}

func TestCLIRunner_CompoundOutputCap(t *testing.T) {
	// The last step emits >1MiB. The compound path captures it in a
	// cappedWriter, so the result truncates at 1MiB without hanging —
	// equivalent to the single-process LimitReader cap. It must also carry
	// a visible truncation marker (beadle-k1g defect 2): cappedWriter used
	// to drop the excess with no trace, in the result or in a log line.
	_, wl := setupWhitelist(t, "echo", "dd")
	runner := &CLIRunner{Whitelist: wl}
	logger, buf := testLoggerCapture()

	cmd := &Command{
		Name:   "test-compound-cap",
		Runner: "cli",
		Mode:   "process",
		Steps: []Step{
			{Binary: "echo", FixedArgs: []string{"-n", "seed"}, Stdin: "pipe"},
			{Binary: "dd", FixedArgs: []string{"if=/dev/zero", "bs=1048577", "count=1"}, Stdin: "stdout"},
		},
		OutputSchema: "text",
		Timeout:      "5s",
	}
	call := CommandCall{Command: "test-compound-cap", Args: map[string]any{}}
	p := testPipeline()

	result, err := runner.Run(context.Background(), &Executor{Logger: logger}, p, 0, cmd, call, "input data")
	require.NoError(t, err)
	assert.Equal(t, 1<<20+len(truncationMarker), len(result))
	assert.True(t, strings.HasSuffix(result, truncationMarker))

	line := logLineContaining(t, buf, "compound command stdout truncated")
	assert.Contains(t, line, "level=WARN")
	assert.Contains(t, line, "test-compound-cap")
	assert.Contains(t, line, "discarded_bytes=1")
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
		Name:      "test-pipe-args",
		Runner:    "cli",
		Mode:      "process",
		Binary:    "echo",
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
		Name:      "test-override",
		Runner:    "cli",
		Mode:      "process",
		Binary:    "echo",
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
		Name:      "test-bad-json",
		Runner:    "cli",
		Mode:      "process",
		Binary:    "echo",
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
