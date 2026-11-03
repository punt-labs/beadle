package daemon

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/punt-labs/beadle/internal/testenv"
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
			// Every caller of setupWhitelist passes a base POSIX utility
			// (echo, cat, false, sleep, dd, env, tr) guaranteed present on
			// every supported platform (darwin/linux) and on this repo's
			// own CI runner -- never an optional tool a developer installs.
			// A missing one means a broken or non-standard system image,
			// not a dependency this test should skip around.
			t.Fatalf("binary %q not found in /usr/bin or /bin -- this is a base system utility expected on every supported platform", name)
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

// capturingSpawner is a minimal Spawner that records the system prompt
// file's content at the moment Run is called -- ClaudeRunner.Run removes
// both the mcp-config and system-prompt files via deferred os.Remove once
// Run returns, so this is the only window in which a package-daemon test
// (which cannot import internal/daemontest, see daemontest's package
// comment) can observe what ClaudeRunner.Run actually wrote.
type capturingSpawner struct {
	systemPromptContent []byte
	missionID           string
	result              WorkerResult
	err                 error
}

func (s *capturingSpawner) Run(_ context.Context, missionID, _, systemPromptPath string, _ []string, _ map[string]string) (WorkerResult, error) {
	s.missionID = missionID
	content, readErr := os.ReadFile(systemPromptPath)
	if readErr == nil {
		s.systemPromptContent = content
	}
	return s.result, s.err
}

// removingSpawner deletes the mcp-config and system-prompt files itself,
// during Run, before ClaudeRunner.Run's own deferred os.Remove calls fire
// on them afterward -- standing in for a race or a worker that cleans up
// its own temp files, so the test can force the "already gone" branch of
// each deferred Remove without any other way to observe it (the paths
// are otherwise invisible outside ClaudeRunner.Run itself).
type removingSpawner struct {
	result WorkerResult
}

func (s *removingSpawner) Run(_ context.Context, _, mcpConfigPath, systemPromptPath string, _ []string, _ map[string]string) (WorkerResult, error) {
	_ = os.Remove(mcpConfigPath)
	_ = os.Remove(systemPromptPath)
	return s.result, nil
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

	result, _, err := runner.Run(context.Background(), &Executor{Logger: testLogger()}, p, 0, cmd, call, "", false, false)
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

	result, _, err := runner.Run(context.Background(), &Executor{Logger: testLogger()}, p, 0, cmd, call, "pipe data", false, false)
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

	_, _, err := runner.Run(context.Background(), &Executor{Logger: testLogger()}, p, 0, cmd, call, "", false, false)
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

	_, _, err := runner.Run(context.Background(), &Executor{Logger: testLogger()}, p, 0, cmd, call, "", false, false)
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

	result, _, err := runner.Run(context.Background(), &Executor{Logger: testLogger()}, p, 0, cmd, call, "", false, false)
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
	_, _, err := runner.Run(context.Background(), &Executor{Logger: testLogger()}, p, 0, cmd, call, "", false, false)
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.Less(t, elapsed, 5*time.Second)
}

// TestCLIRunner_OutputCap verifies truncation at the 1MB cap is both
// capped and reported via Run's own truncated return value -- round 3 of
// beadle-4gm1 moved the truncation signal off an appended marker string;
// round 4 moved it off a shared Pipeline field (which raced the one reader
// of a different stage's truncation state) onto this return value, which
// Executor.Run carries forward as a local alongside the pipe value it
// describes.
func TestCLIRunner_OutputCap(t *testing.T) {
	// Use dd to produce >1MB of output and verify truncation is capped and
	// marked -- a downstream reader (including an LLM worker reading its
	// system prompt's fenced pipeline output field) must be able to tell
	// the value was cut rather than silently trusting a partial value as
	// complete.
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

	result, truncated, err := runner.Run(context.Background(), &Executor{Logger: testLogger()}, p, 0, cmd, call, "", false, false)
	require.NoError(t, err)
	assert.Equal(t, 1<<20, len(result), "the returned string itself is capped at exactly 1MB -- no marker appended")
	assert.NotContains(t, result, "…[truncated]", "no truncation marker must reach the returned string; the truncated return value is the sole statement of truncation")
	assert.True(t, truncated)
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

	_, _, err := runner.Run(context.Background(), &Executor{Logger: logger}, p, 0, cmd, call, "", false, false)
	require.NoError(t, err)

	line := logLineContaining(t, buf, "cli command stdout truncated")
	assert.Contains(t, line, "level=WARN")
	assert.Contains(t, line, "test-cap-logged")
	assert.Contains(t, line, "discarded_bytes=1")
	assert.Contains(t, line, "pipeline=test-pipe")
	assert.Contains(t, line, "stage=0")
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

	_, _, err := runOutput(r, func() error { return nil }, stderrBuf, e, cmd, nil, 0)
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

	_, _, err := runOutput(r, func() error { return errors.New("wait boom") }, stderrBuf, e, cmd, nil, 0)
	require.Error(t, err)

	line := logLineContaining(t, buf, "exit status 1")
	assert.Contains(t, line, "test-stderr-on-wait-error")
}

// TestRunOutput_StderrLogCarriesPipelineAndStage proves the "cli command
// stderr" Info line (logged when the command exits 0 but still wrote to
// stderr) is tagged with pipeline/stage via pipelineLogArgs, the same
// correlation the truncation Warn lines already carry -- a daemon running
// several pipelines concurrently needs it to tell one stage's stderr apart
// from another's in the log stream.
func TestRunOutput_StderrLogCarriesPipelineAndStage(t *testing.T) {
	logger, buf := testLoggerCapture()
	stderrBuf := &cappedWriter{max: 1 << 20}
	_, writeErr := stderrBuf.Write([]byte("warning: deprecated flag"))
	require.NoError(t, writeErr)

	e := &Executor{Logger: logger}
	cmd := &Command{Name: "test-stderr-attributed", Binary: "true"}
	p := testPipeline()

	_, _, err := runOutput(strings.NewReader(""), func() error { return nil }, stderrBuf, e, cmd, p, 2)
	require.NoError(t, err)

	line := logLineContaining(t, buf, "warning: deprecated flag")
	assert.Contains(t, line, "test-stderr-attributed")
	assert.Contains(t, line, "pipeline="+p.ID)
	assert.Contains(t, line, "stage=2")
}

// TestReadCapped_ReportsExtraOnlyWhenTruncated confirms extra is 0 (no
// truncation) when the reader has less data than the cap, and > 0 (real
// truncation) when it has more -- the distinction TestCLIRunner_OutputCap
// depends on to decide whether truncated is reported true.
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

	result, _, err := runner.Run(context.Background(), &Executor{Logger: logger}, p, 0, cmd, call, "", false, false)
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

	result, _, err := runner.Run(context.Background(), &Executor{Logger: logger}, p, 0, cmd, call, "", false, false)
	require.NoError(t, err)
	assert.Equal(t, "hello", result)

	line := logLineContaining(t, buf, "invalid timeout in command definition")
	assert.Contains(t, line, "level=ERROR")
	assert.Contains(t, line, "test-compound-bad-timeout")
}

// TestCLIRunner_MissingDeclaredEnvVarFailsStage is the regression test for
// a defect fixed in PR #263: a declared env_vars entry absent from the
// daemon's own environment used to be logged at Error and the command run
// anyway. That mattered because context7 does not enforce auth -- its
// endpoint returns 200 on initialize/tools-list for a valid Bearer token,
// the literal header string, or no Authorization header at all -- so
// docs-ask.yaml's context7 lookup would not error on a missing
// CONTEXT7_API_KEY; it would just run unauthenticated and return a
// fluent, wrong-looking-right answer with nothing to flag the
// difference. The fix does not depend on the remote service noticing: a
// declared env var is a contract, and this is a permanent
// misconfiguration (the deployment, not the request, is missing the
// var), so it must fail the stage on that basis alone; Executor.Run
// (pipeline.go) turns that into a failed pipeline and a reply to the
// sender via fireElse.
func TestCLIRunner_MissingDeclaredEnvVarFailsStage(t *testing.T) {
	_, wl := setupWhitelist(t, "env")
	runner := &CLIRunner{Whitelist: wl}
	logger, _ := testLoggerCapture()

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

	_, _, err := runner.Run(context.Background(), &Executor{Logger: logger}, p, 0, cmd, call, "", false, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "BEADLE_K1G_DEFINITELY_UNSET_VAR")
	assert.Contains(t, err.Error(), "test-missing-env")
}

// TestCLIRunner_CompoundMissingDeclaredEnvVarFailsStage is
// TestCLIRunner_MissingDeclaredEnvVarFailsStage for the compound path.
func TestCLIRunner_CompoundMissingDeclaredEnvVarFailsStage(t *testing.T) {
	_, wl := setupWhitelist(t, "env", "cat")
	runner := &CLIRunner{Whitelist: wl}
	logger, _ := testLoggerCapture()

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

	_, _, err := runner.Run(context.Background(), &Executor{Logger: logger}, p, 0, cmd, call, "", false, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "BEADLE_K1G_DEFINITELY_UNSET_VAR")
	assert.Contains(t, err.Error(), "test-compound-missing-env")
}

// TestClaudeRunner_MissingDeclaredEnvVarFailsStage is
// TestCLIRunner_MissingDeclaredEnvVarFailsStage for the claude runner --
// the site FIX 3 actually names (runner.go's ClaudeRunner.Run), and the
// one docs-ask.yaml's context7 lookup goes through. The check runs before
// any mission or MCP config is built, so a zero-value ClaudeRunner (no
// Templates, Missions, or Spawner configured) is enough to exercise it.
func TestClaudeRunner_MissingDeclaredEnvVarFailsStage(t *testing.T) {
	runner := &ClaudeRunner{}
	logger, _ := testLoggerCapture()

	cmd := &Command{
		Name:    "docs-ask",
		Runner:  "claude",
		EnvVars: []string{"BEADLE_K1G_DEFINITELY_UNSET_VAR"},
	}
	call := CommandCall{Command: "docs-ask", Args: map[string]any{}}
	p := testPipeline()

	_, _, err := runner.Run(context.Background(), &Executor{Logger: logger}, p, 0, cmd, call, "", false, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "BEADLE_K1G_DEFINITELY_UNSET_VAR")
	assert.Contains(t, err.Error(), "docs-ask")
}

// TestClaudeRunner_MissingMCPBinaryCreatesNoMission is the ordering
// regression test for round 2 of beadle-tsku: BuildMCPConfig must run
// BEFORE createMissionFromContract in ClaudeRunner.Run, so a daemon with
// an unprovisioned MCP server binary fails the stage without ever
// invoking `ethos mission create` for it. Proving this requires the real
// ethos CLI (a fake MissionCreator would prove only that the fake wasn't
// called, not that no OS-level mission was created) run against an
// isolated $HOME/$ETHOS_REPO_ROOT (testenv.IsolateEthos) so this assertion
// -- the isolated mission store has zero missions after the failing call
// -- cannot pass merely because some unrelated concurrent session's
// missions happen to be present.
//
// cmd carries a valid WriteSet, Prompt, and Budget -- the shape
// TestBuildStageContract_ValidatesAgainstRealEthosCLI proves the real CLI
// accepts -- specifically so that IF the ordering regressed (BuildMCPConfig
// moved back below createMissionFromContract), `ethos mission create` would
// actually succeed and leave a real, listable mission behind for this
// test's final assertion to catch; a contract the CLI would reject for an
// unrelated reason (e.g. an empty write_set) would make this pass for the
// wrong reason regardless of which statement ran first.
func TestClaudeRunner_MissingMCPBinaryCreatesNoMission(t *testing.T) {
	ethosPath := testenv.EthosOrFatal(t)
	testenv.IsolateEthos(t)

	runner := &ClaudeRunner{
		Templates: &MissionTemplate{TmpDir: t.TempDir()},
		Registry: map[string]MCPServerConfig{
			"ethos": {Command: "beadle-tsku-nonexistent-binary"},
		},
	}
	logger, _ := testLoggerCapture()

	cmd := &Command{
		Name:       "test-missing-mcp-binary",
		Runner:     "claude",
		MCPServers: []string{"ethos"},
		Prompt:     "Reply to the sender",
		WriteSet:   []string{fmt.Sprintf("internal/daemon/testdata/mission-regression-fixture-tsku-%s.txt", uuid.New().String())},
		Budget: struct {
			Rounds              int  `yaml:"rounds" json:"rounds"`
			ReflectionAfterEach bool `yaml:"reflection_after_each" json:"reflection_after_each"`
		}{Rounds: 1, ReflectionAfterEach: false},
	}
	call := CommandCall{Command: "test-missing-mcp-binary", Args: map[string]any{}}
	p := testPipeline()

	_, _, err := runner.Run(context.Background(), &Executor{Logger: logger}, p, 0, cmd, call, "", false, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ethos")
	assert.Contains(t, err.Error(), "beadle-tsku-nonexistent-binary")

	listCmd := exec.Command(ethosPath, "mission", "list", "--status", "all", "--json")
	out, listErr := listCmd.CombinedOutput()
	require.NoError(t, listErr, "ethos mission list: %s", string(out))
	assert.JSONEq(t, "[]", strings.TrimSpace(string(out)),
		"a missing MCP server binary must fail before any ethos mission is created, leaving the isolated mission store empty")
}

// TestClaudeRunner_PayloadReachesSystemPrompt is the argument-threading
// regression test for beadle-4gm1 (docs/email-payload-handoff.md,
// DES-038): ClaudeRunner.Run must pass call.Args, pipe, and p.Body into the
// system-prompt builder at the existing buildStageContract call site, so
// the stage's actual payload reaches the worker's system prompt through
// the real pipeline, not just in the templates_test.go unit tests that
// call BuildSystemPromptForTools directly. Renamed (round 3) from
// TestClaudeRunner_PayloadReachesSystemPromptNotContract: the contract
// half of that name's claim is proven by the distinct
// TestClaudeRunner_ContractCarriesNoPayload instead, which this test does
// not touch.
//
// MCPServers: []string{} (explicitly empty, not nil) sidesteps
// BuildMCPConfig's exec.LookPath resolution, so this test needs no real
// ethos/beadle-email binaries on PATH beyond the real `ethos` CLI that
// createMissionFromContract itself invokes (via testenv.EthosOrFatal/
// IsolateEthos, the same pattern TestClaudeRunner_MissingMCPBinaryCreatesNoMission
// uses).
func TestClaudeRunner_PayloadReachesSystemPrompt(t *testing.T) {
	testenv.EthosOrFatal(t)
	testenv.IsolateEthos(t)

	spawner := &capturingSpawner{result: WorkerResult{Output: "done"}}
	runner := &ClaudeRunner{
		Templates: &MissionTemplate{TmpDir: t.TempDir()},
		Spawner:   spawner,
	}
	logger, _ := testLoggerCapture()

	cmd := &Command{
		Name:       "test-payload-threading",
		Runner:     "claude",
		MCPServers: []string{},
		Prompt:     "Handle the stage",
		WriteSet:   []string{fmt.Sprintf("internal/daemon/testdata/mission-regression-fixture-4gm1-%s.txt", uuid.New().String())},
		Budget: struct {
			Rounds              int  `yaml:"rounds" json:"rounds"`
			ReflectionAfterEach bool `yaml:"reflection_after_each" json:"reflection_after_each"`
		}{Rounds: 1, ReflectionAfterEach: false},
	}
	call := CommandCall{Command: "test-payload-threading", Args: map[string]any{"region": "us-west-2"}}
	p := testPipeline()
	p.Body = "please handle this stage"

	_, _, err := runner.Run(context.Background(), &Executor{Logger: logger}, p, 0, cmd, call, `{"prior":"stage output"}`, false, false)
	require.NoError(t, err)

	require.NotEmpty(t, spawner.systemPromptContent, "system prompt file must have been captured")
	content := string(spawner.systemPromptContent)
	assert.Contains(t, content, "stage arg region",
		"call.Args must reach the system prompt")
	assert.Contains(t, content, "us-west-2",
		"call.Args's value must reach the system prompt")
	assert.Contains(t, content, `{"prior":"stage output"}`,
		"the pipe value (prior stage output) must reach the system prompt")
	assert.Contains(t, content, "please handle this stage",
		"p.Body (the triggering email's body) must reach the system prompt")
}

// TestClaudeRunner_LogsFailedTempFileRemoval proves ClaudeRunner.Run Warns,
// naming the path, when either deferred os.Remove (mcp-config,
// system-prompt) fails -- rather than the prior silent
// `_ = os.Remove(...)`. removingSpawner forces the "already gone" branch by
// deleting both files itself during Run, before ClaudeRunner.Run's own
// deferred removal fires on them.
func TestClaudeRunner_LogsFailedTempFileRemoval(t *testing.T) {
	testenv.EthosOrFatal(t)
	testenv.IsolateEthos(t)

	spawner := &removingSpawner{result: WorkerResult{Output: "done"}}
	runner := &ClaudeRunner{
		Templates: &MissionTemplate{TmpDir: t.TempDir()},
		Spawner:   spawner,
	}
	logger, buf := testLoggerCapture()

	cmd := &Command{
		Name:       "test-temp-file-removal",
		Runner:     "claude",
		MCPServers: []string{},
		Prompt:     "Handle the stage",
		WriteSet:   []string{fmt.Sprintf("internal/daemon/testdata/mission-regression-fixture-tmpfile-%s.txt", uuid.New().String())},
		Budget: struct {
			Rounds              int  `yaml:"rounds" json:"rounds"`
			ReflectionAfterEach bool `yaml:"reflection_after_each" json:"reflection_after_each"`
		}{Rounds: 1, ReflectionAfterEach: false},
	}
	call := CommandCall{Command: "test-temp-file-removal", Args: map[string]any{}}
	p := testPipeline()

	_, _, err := runner.Run(context.Background(), &Executor{Logger: logger}, p, 0, cmd, call, "", false, false)
	require.NoError(t, err)

	mcpLine := logLineContaining(t, buf, "remove mcp config temp file")
	assert.Contains(t, mcpLine, "level=WARN")
	assert.Contains(t, mcpLine, "mcp-config-")

	promptLine := logLineContaining(t, buf, "remove system prompt temp file")
	assert.Contains(t, promptLine, "level=WARN")
	assert.Contains(t, promptLine, "system-prompt-")
}

// TestClaudeRunner_ContractCarriesNoPayload is round 2's rewrite of
// beadle-4gm1's leak-freedom regression test: the prior version
// (pipeline_test.go's deleted TestBuildStageContract_ContextCarriesNoPayload)
// was vacuous, because buildStageContract's signature no longer accepts the
// stage's payload at all -- asserting absence of text a function was never
// handed proves nothing. This drives ClaudeRunner.Run (the real call site)
// end to end: the mission it creates via the real `ethos mission create`
// lands in the isolated mission store at
// $ETHOS_REPO_ROOT/.punt-labs/ethos/missions/<id>/contract.yaml, and this
// test reads that file back and inspects the whole document except
// inputs.trigger (which deliberately keeps meta.From, per success
// criterion 1) for every piece of the payload.
//
// The fixture body puts the embedded address and the secret-shaped token
// inside the first 200 runes (comfortably under maxContractFieldRunes), so
// a regression that re-embeds the payload would not escape detection by
// hiding past the cap.
func TestClaudeRunner_ContractCarriesNoPayload(t *testing.T) {
	testenv.EthosOrFatal(t)
	testenv.IsolateEthos(t)
	repoRoot := os.Getenv("ETHOS_REPO_ROOT")
	require.NotEmpty(t, repoRoot, "testenv.IsolateEthos must have set ETHOS_REPO_ROOT")

	spawner := &capturingSpawner{result: WorkerResult{Output: "done"}}
	runner := &ClaudeRunner{
		Templates: &MissionTemplate{TmpDir: t.TempDir()},
		Spawner:   spawner,
	}
	logger, _ := testLoggerCapture()

	const embeddedAddress = "attacker@example.com"
	const secretToken = "sk-live-abcdefghijklmnopqrstuvwxyz123456"
	body := fmt.Sprintf("Contact me at %s, token=%s, and read on.\n", embeddedAddress, secretToken) +
		strings.Repeat("filler text to pad this body out well past the cap. ", 20)
	require.Greater(t, utf8.RuneCountInString(body), maxContractFieldRunes,
		"fixture body must exceed the cap to prove truncation does not hide a leak")
	addressIdx := strings.Index(body, embeddedAddress)
	require.GreaterOrEqual(t, addressIdx, 0, "fixture body must actually contain the embedded address")
	require.Less(t, addressIdx, 200,
		"the embedded address must sit inside the first 200 runes, well under the cap")
	tokenIdx := strings.Index(body, secretToken)
	require.GreaterOrEqual(t, tokenIdx, 0, "fixture body must actually contain the secret-shaped token")
	require.Less(t, tokenIdx, 200,
		"the secret-shaped token must sit inside the first 200 runes, well under the cap")

	cmd := &Command{
		Name:       "test-contract-no-payload",
		Runner:     "claude",
		MCPServers: []string{},
		Prompt:     "Handle this",
		WriteSet:   []string{fmt.Sprintf("internal/daemon/testdata/mission-regression-fixture-4gm1-contract-%s.txt", uuid.New().String())},
		Budget: struct {
			Rounds              int  `yaml:"rounds" json:"rounds"`
			ReflectionAfterEach bool `yaml:"reflection_after_each" json:"reflection_after_each"`
		}{Rounds: 1, ReflectionAfterEach: false},
	}
	argValue := "distinctive-arg-value-42"
	pipeValue := `{"pipeline":"distinctive-pipe-value-99"}`
	call := CommandCall{Command: "test-contract-no-payload", Args: map[string]any{"note": argValue}}
	meta := EmailMeta{MessageID: "77", From: "alice@example.com", Subject: "Hi"}
	p := &Pipeline{ID: "test-pipe-contract", Email: meta, Body: body}

	_, _, err := runner.Run(context.Background(), &Executor{Logger: logger}, p, 0, cmd, call, pipeValue, false, false)
	require.NoError(t, err)
	require.NotEmpty(t, spawner.missionID, "ClaudeRunner.Run must have created a mission")

	contractPath := filepath.Join(repoRoot, ".punt-labs", "ethos", "missions", spawner.missionID, "contract.yaml")
	contractBytes, err := os.ReadFile(contractPath)
	require.NoError(t, err, "stored contract file must exist at the real ethos mission store path")
	contract := string(contractBytes)

	var doc map[string]any
	require.NoError(t, yaml.Unmarshal(contractBytes, &doc))
	trigger, ok := doc["inputs"].(map[string]any)["trigger"].(map[string]any)
	require.True(t, ok, "inputs.trigger must be a map")
	// escapeYAMLValue(meta.From) is the double-quoted YAML scalar
	// buildStageContract wrote into the raw file; a YAML decoder resolves
	// that quoted scalar to its plain value, which is exactly meta.From
	// here (no characters escapeYAMLValue needs to escape or cap). Proving
	// trigger["from"] decodes back to meta.From proves escapeYAMLValue's
	// round trip, not just that some string happens to equal meta.From.
	//
	// Two forms are accepted because the store is written by the real ethos
	// on PATH: ethos v4.16.0 keeps the address verbatim, while a build with
	// DES-080 redaction stores "[redacted-email]". inputs.trigger.from is
	// provenance, not payload, and DES-038 (docs/email-payload-handoff.md)
	// allows either. Everything outside inputs.trigger stays strict below.
	assert.Contains(t, []any{meta.From, "[redacted-email]"}, trigger["from"],
		"inputs.trigger.from must be meta.From (round-tripped through YAML) or ethos's DES-080 redaction -- DES-038, success criterion 1")
	delete(doc, "inputs")

	rest, err := yaml.Marshal(doc)
	require.NoError(t, err)
	restStr := string(rest)

	for _, line := range strings.Split(body, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		assert.NotContains(t, restStr, line,
			"the stored contract (outside inputs.trigger) must not carry any line of the triggering email's body")
	}
	assert.NotContains(t, restStr, embeddedAddress,
		"the stored contract (outside inputs.trigger) must not carry the body's embedded address")
	assert.NotContains(t, restStr, secretToken,
		"the stored contract (outside inputs.trigger) must not carry the secret-shaped token")
	assert.NotContains(t, restStr, argValue,
		"the stored contract (outside inputs.trigger) must not carry the stage's call arg value")
	assert.NotContains(t, restStr, pipeValue,
		"the stored contract (outside inputs.trigger) must not carry the pipe value")
	assert.NotContains(t, restStr, meta.From,
		"the stored contract (outside inputs.trigger) must not carry meta.From by any other path")

	// Sanity: the whole raw file (inputs.trigger included) still carries
	// none of the three payload components -- only meta.From there is
	// expected and already proven above via escapeYAMLValue.
	assert.NotContains(t, contract, embeddedAddress, "double-check against the raw file too")
	assert.NotContains(t, contract, secretToken, "double-check against the raw file too")
	assert.NotContains(t, contract, argValue, "double-check against the raw file too")
	assert.NotContains(t, contract, pipeValue, "double-check against the raw file too")

	assert.Equal(t, stageContextReference(p.ID, 0), doc["context"],
		"context must equal the fixed reference string")
}

// TestExecutor_TruncatedPipeReachesClaudeStagePayload is round 4's
// end-to-end proof that the claude stage's StagePayload (and therefore the
// worker's actual fenced system prompt) reflects the carried
// pipeTruncated local, not just that mockClaudeRunner recorded it: a
// passthrough CLIRunner stage's stdout hits the 1MB cap, then a real
// ClaudeRunner stage runs against a real ethos CLI and a capturingSpawner,
// and its written system prompt must fence the pipeline output as
// truncated.
func TestExecutor_TruncatedPipeReachesClaudeStagePayload(t *testing.T) {
	testenv.EthosOrFatal(t)
	testenv.IsolateEthos(t)

	_, wl := setupWhitelist(t, "dd", "tr")
	spawner := &capturingSpawner{result: WorkerResult{Output: "done"}}
	claude := &ClaudeRunner{
		Templates: &MissionTemplate{TmpDir: t.TempDir()},
		Spawner:   spawner,
	}

	cmds := testCommands()
	cmds["cap"] = capCommand("text")
	cmds["greet"].MCPServers = []string{} // sidestep BuildMCPConfig's exec.LookPath for ethos/beadle-email

	exec := &Executor{
		Planner: &StubPlanner{Result: []CommandCall{
			{Command: "cap", Args: map[string]any{}},
			{Command: "greet", Args: map[string]any{}},
		}},
		Commands: cmds,
		Runners:  map[string]Runner{"cli": &CLIRunner{Whitelist: wl}, "claude": claude},
		Logger:   testLogger(),
	}

	meta := EmailMeta{MessageID: "1", From: "jim@test.com", Subject: "T"}
	p, err := exec.Run(context.Background(), meta, "body")
	require.NoError(t, err)
	require.Equal(t, "completed", p.Status)

	require.NotEmpty(t, spawner.systemPromptContent, "greet's system prompt must have been captured")
	content := string(spawner.systemPromptContent)
	// The cap stage's own output is dd writing 1MiB of zero bytes, capped
	// at exactly 1<<20 bytes by runOutput's readCapped -- every byte of
	// which is one rune, so the fence header's rune count is the cap
	// itself, not a guessed or hard-coded figure.
	wantRunes := 1 << 20
	wantHeader := fmt.Sprintf("pipeline output: %d runes, truncated ---", wantRunes)
	assert.Contains(t, content, wantHeader,
		"the pipeline output field's fence header must name the cap stage's own capped rune count and say truncated")
}

// TestCLIRunner_ArgsFromPipe_MalformedJSON is the regression test for
// beadle-k1g defect 4: a pipe value that looks like JSON (starts with '{')
// but fails to parse -- e.g. truncated by defect 1's 1MB cap -- used to be
// swallowed by `_ = json.Unmarshal(...)`, silently omitting every arg that
// should have been filled from the pipe with no record of why. The log line
// also carries pipeline/stage (pipelineLogArgs) and the incoming pipe's own
// truncation state, so a cut pipe that fails to parse is attributed to the
// cap, not read as unrelated malformed JSON.
func TestCLIRunner_ArgsFromPipe_MalformedJSON(t *testing.T) {
	_, wl := setupWhitelist(t, "echo")
	runner := &CLIRunner{Whitelist: wl}

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

	// Starts with '{' so the pipe-as-JSON path is taken, but the JSON is
	// truncated mid-value and fails to parse.
	pipe := `{"title": "Fix auth`

	for _, tt := range []struct {
		name          string
		pipeTruncated bool
	}{
		{"pipe not truncated upstream", false},
		{"pipe truncated upstream: attributed to the cap", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			logger, buf := testLoggerCapture()
			p := testPipeline()

			result, _, err := runner.Run(context.Background(), &Executor{Logger: logger}, p, 0, cmd, call, pipe, tt.pipeTruncated, false)
			require.NoError(t, err)
			assert.Empty(t, result) // no args filled: title was never resolved

			line := logLineContaining(t, buf, "pipe value looked like JSON but failed to parse")
			assert.Contains(t, line, "level=WARN")
			assert.Contains(t, line, "test-malformed-json")
			assert.Contains(t, line, "pipeline="+p.ID)
			assert.Contains(t, line, "stage=0")
			assert.Contains(t, line, fmt.Sprintf("pipe_truncated=%v", tt.pipeTruncated))
		})
	}
}

// TestBuildStageArgs_NoArgs confirms buildStageArgs returns nil (not an
// empty, non-nil slice) for an empty args map -- stagePayloadText's own
// len(payload.Args) > 0 check treats both the same way, but nil is the
// honest zero value for "no args were ever processed."
func TestBuildStageArgs_NoArgs(t *testing.T) {
	e := &Executor{Logger: testLogger()}
	p := testPipeline()

	got := buildStageArgs(e, p, 0, "test-cmd", map[string]any{})
	assert.Nil(t, got)
}

// TestBuildStageArgs_SortedByName proves buildStageArgs returns args sorted
// by name, the ordering stagePayloadText used to produce itself (via
// sort.Strings over the map's keys) before round 5 moved sanitizing and
// capping -- and with it, the sort -- here.
func TestBuildStageArgs_SortedByName(t *testing.T) {
	e := &Executor{Logger: testLogger()}
	p := testPipeline()

	got := buildStageArgs(e, p, 0, "test-cmd", map[string]any{"zebra": "z", "alpha": "a", "mid": 1})
	require.Len(t, got, 3)
	assert.Equal(t, "alpha", got[0].Name)
	assert.Equal(t, "mid", got[1].Name)
	assert.Equal(t, "zebra", got[2].Name)
}

// TestBuildStageArgs_SanitizesNULAndInvalidUTF8 is the round-2 regression
// test for beadle-4gm1's normalization finding (moved here in round 5
// alongside the sanitize/cap logic itself, from stagePayloadText): a NUL
// byte and an invalid UTF-8 sequence must be sanitized identically whether
// the value is under or over maxContractFieldRunes -- capRunes alone left
// invalid UTF-8 untouched under the cap while silently rewriting it at the
// rune boundary over the cap, two different behaviors for the same input
// depending only on length. Expected values are built from
// sanitizeText/capRunes themselves (not hand-computed strings), since the
// sanitize-then-cap pipeline is exactly what this test exists to pin down.
// Each case also proves the Warn log: silent when nothing fired, naming the
// affected arg (never its value) when something did.
func TestBuildStageArgs_SanitizesNULAndInvalidUTF8(t *testing.T) {
	invalid := "ok\x00bad\xffend"

	t.Run("under cap", func(t *testing.T) {
		sanitized, changed := sanitizeText(invalid)
		require.True(t, changed, "fixture must actually need sanitizing")

		logger, buf := testLoggerCapture()
		e := &Executor{Logger: logger}
		p := testPipeline()

		got := buildStageArgs(e, p, 0, "test-cmd", map[string]any{"note": invalid})
		require.Len(t, got, 1)
		assert.Equal(t, StageArg{Name: "note", Value: sanitized, Truncated: false, Sanitized: true}, got[0])

		line := logLineContaining(t, buf, "stage arg sanitized or truncated before stage input")
		assert.Contains(t, line, "pipeline="+p.ID)
		assert.Contains(t, line, "stage=0")
		assert.Contains(t, line, "test-cmd")
		assert.NotContains(t, line, invalid, "the arg's own value must never appear in the log line")
	})

	t.Run("over cap", func(t *testing.T) {
		longInvalid := strings.Repeat("x", maxContractFieldRunes) + invalid
		sanitized, changed := sanitizeText(longInvalid)
		require.True(t, changed, "fixture must actually need sanitizing")
		capped, truncated := capRunes(sanitized, maxContractFieldRunes)
		require.True(t, truncated, "fixture must actually need truncating")

		logger, buf := testLoggerCapture()
		e := &Executor{Logger: logger}
		p := testPipeline()

		got := buildStageArgs(e, p, 0, "test-cmd", map[string]any{"note": longInvalid})
		require.Len(t, got, 1)
		assert.Equal(t, StageArg{Name: "note", Value: capped, Truncated: true, Sanitized: true}, got[0])

		line := logLineContaining(t, buf, "stage arg sanitized or truncated before stage input")
		assert.Contains(t, line, "pipeline="+p.ID)
		assert.Contains(t, line, "stage=0")
	})

	t.Run("clean arg under the cap: no log line", func(t *testing.T) {
		logger, buf := testLoggerCapture()
		e := &Executor{Logger: logger}
		p := testPipeline()

		got := buildStageArgs(e, p, 0, "test-cmd", map[string]any{"note": "clean value"})
		require.Len(t, got, 1)
		assert.Equal(t, StageArg{Name: "note", Value: "clean value"}, got[0])
		assert.NotContains(t, buf.String(), "stage arg sanitized or truncated")
	})
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

// TestCLIRunner_CompoundOutputCap is TestCLIRunner_OutputCap's sibling for
// the compound-step path: truncation is reported via Run's own truncated
// return value, not a marker appended to the returned string (round 3 of
// beadle-4gm1) and not a shared Pipeline field (round 4).
func TestCLIRunner_CompoundOutputCap(t *testing.T) {
	// The last step emits >1MiB. The compound path captures it in a
	// cappedWriter, so the result truncates at 1MiB without hanging —
	// equivalent to the single-process LimitReader cap. It must also be
	// recorded visibly (beadle-k1g defect 2): cappedWriter used to drop the
	// excess with no trace, in the result or in a log line.
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

	result, truncated, err := runner.Run(context.Background(), &Executor{Logger: logger}, p, 0, cmd, call, "input data", false, false)
	require.NoError(t, err)
	assert.Equal(t, 1<<20, len(result), "the returned string itself is capped at exactly 1MB -- no marker appended")
	assert.NotContains(t, result, "…[truncated]", "no truncation marker must reach the returned string; the truncated return value is the sole statement of truncation")
	assert.True(t, truncated)

	line := logLineContaining(t, buf, "compound command stdout truncated")
	assert.Contains(t, line, "level=WARN")
	assert.Contains(t, line, "test-compound-cap")
	assert.Contains(t, line, "discarded_bytes=1")
	assert.Contains(t, line, "pipeline=test-pipe")
	assert.Contains(t, line, "stage=0")
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

	result, _, err := runner.Run(context.Background(), &Executor{Logger: testLogger()}, p, 0, cmd, call, "input data", false, false)
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

	result, _, err := runner.Run(context.Background(), &Executor{Logger: testLogger()}, p, 0, cmd, call, "", false, false)
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

	_, _, err := runner.Run(context.Background(), &Executor{Logger: testLogger()}, p, 0, cmd, call, "", false, false)
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

	_, _, err := runner.Run(context.Background(), &Executor{Logger: testLogger()}, p, 0, cmd, call, "", false, false)
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
	_, _, err := runner.Run(context.Background(), &Executor{Logger: testLogger()}, p, 0, cmd, call, "", false, false)
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

	result, _, err := runner.Run(context.Background(), &Executor{Logger: testLogger()}, p, 0, cmd, call, "", false, false)
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

	result, _, err := runner.Run(context.Background(), &Executor{Logger: testLogger()}, p, 0, cmd, call, "", false, false)
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

	result, _, err := runner.Run(context.Background(), &Executor{Logger: testLogger()}, p, 0, cmd, call, "pipe data", false, false)
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
	result, _, err := runner.Run(context.Background(), &Executor{Logger: testLogger()}, p, 0, cmd, call, pipe, false, false)
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
	result, _, err := runner.Run(context.Background(), &Executor{Logger: testLogger()}, p, 0, cmd, call, pipe, false, false)
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
	result, _, err := runner.Run(context.Background(), &Executor{Logger: testLogger()}, p, 0, cmd, call, pipe, false, false)
	require.NoError(t, err)
	assert.Contains(t, result, "--title=FromArgs")
}

// TestExecutor_SanitizedPipeReachesClaudeStagePayload proves the pipe's
// sanitization state is decided once, in Executor.Run's stage loop, and
// carried to the fence header: a CLI stage emitting a NUL byte and invalid
// UTF-8 must reach the claude stage's fence as "sanitized", with the
// sanitized text in the fence body.
func TestExecutor_SanitizedPipeReachesClaudeStagePayload(t *testing.T) {
	testenv.EthosOrFatal(t)
	testenv.IsolateEthos(t)

	_, wl := setupWhitelist(t, "printf")
	spawner := &capturingSpawner{result: WorkerResult{Output: "done"}}
	claude := &ClaudeRunner{
		Templates: &MissionTemplate{TmpDir: t.TempDir()},
		Spawner:   spawner,
	}

	cmds := testCommands()
	cmds["dirty"] = &Command{
		Name:         "dirty",
		Runner:       "cli",
		Mode:         "process",
		Binary:       "printf",
		FixedArgs:    []string{`a\000b\377c`},
		OutputSchema: "text",
		Timeout:      "5s",
	}
	cmds["greet"].MCPServers = []string{}

	exec := &Executor{
		Planner: &StubPlanner{Result: []CommandCall{
			{Command: "dirty", Args: map[string]any{}},
			{Command: "greet", Args: map[string]any{}},
		}},
		Commands: cmds,
		Runners:  map[string]Runner{"cli": &CLIRunner{Whitelist: wl}, "claude": claude},
		Logger:   testLogger(),
	}

	meta := EmailMeta{MessageID: "1", From: "[EMAIL]", Subject: "T"}
	p, err := exec.Run(context.Background(), meta, "body")
	require.NoError(t, err)
	require.Equal(t, "completed", p.Status)

	content := string(spawner.systemPromptContent)
	assert.Contains(t, content, "pipeline output: 4 runes, sanitized ---")
	assert.Contains(t, content, "\nab�c\n")
	assert.NotContains(t, content, "\x00")
}
