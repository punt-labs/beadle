// Package daemontest provides fixtures for testing internal/daemon's
// mail-triggered pipeline that genuinely need internal/daemon's exported
// surface: a gate-3 command-signing helper and a fake worker spawner.
//
// It is a separate package from internal/testenv because internal/daemon's
// own white-box tests (package daemon) already import internal/testenv --
// a helper in testenv that itself imported internal/daemon would close
// that into an import cycle ("import cycle not allowed in test"), confirmed
// by trial build. The same constraint means package daemon's own
// _test.go files cannot import this package either (it imports daemon,
// same cycle) -- this package exists specifically for
// internal/daemon/harness_test.go, which must therefore be an external
// test package (`package daemon_test`), not the white-box convention every
// other file in internal/daemon uses.
//
// Fixtures that need no daemon type at all -- GenOwnerKey,
// BuildPGPSignedMessage, BuildTrustedMessage, EthosOrFatal -- live in
// internal/testenv instead, where package daemon's own white-box tests can
// reach them too. Only SignCommand and FakeSpawner genuinely require this
// package: they build a *daemon.Command and implement daemon.Spawner.
package daemontest

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/punt-labs/beadle/internal/daemon"
)

// SignCommand detach-signs cmd's canonical bytes (daemon.CanonicalCommandBytes)
// using signerEmail's key in the gpg homedir home, and returns a copy of cmd
// with Signature set to the resulting armored signature. It never mutates
// cmd. home must already hold signerEmail's keypair (see
// testenv.GenKey/GenKeyNoExpiry).
func SignCommand(t testing.TB, gpgBin, home, signerEmail string, cmd *daemon.Command) *daemon.Command {
	t.Helper()

	canon, err := daemon.CanonicalCommandBytes(cmd)
	require.NoError(t, err)

	sigCmd := exec.Command(gpgBin, "--homedir", home, "--batch", "--no-tty",
		"--pinentry-mode", "loopback", "--passphrase", "",
		"--detach-sign", "--armor", "-u", signerEmail)
	sigCmd.Stdin = bytes.NewReader(canon)
	var stdout, stderr bytes.Buffer
	sigCmd.Stdout = &stdout
	sigCmd.Stderr = &stderr
	require.NoError(t, sigCmd.Run(), "gpg detach-sign failed: %s", stderr.String())

	signed := *cmd
	signed.Signature = stdout.String()
	return &signed
}

// FakeSpawnerCall records one FakeSpawner.Run invocation.
type FakeSpawnerCall struct {
	MissionID        string
	MCPConfigPath    string
	SystemPromptPath string
	Tools            []string
	EnvOverrides     map[string]string

	// MCPConfigContent and SystemPromptContent hold the bytes of the
	// mcp-config and system-prompt files at the moment Run was called.
	// ClaudeRunner.Run (runner.go) creates both files before calling
	// Spawner.Run and removes them (via deferred os.Remove) only after
	// Run returns, so reading them here -- inside Run -- is the one
	// window in which a test can observe their contents; by the time
	// Run's caller regains control, the files are gone. nil when the
	// read failed, so a test path that never expects these files
	// populated (or races the cleanup) does not fail the spawn itself.
	MCPConfigContent    []byte
	SystemPromptContent []byte
}

// FakeSpawner implements daemon.Spawner without spawning a real Claude Code
// subprocess -- the one seam a daemon pipeline test must fake, since a real
// spawn is unbounded in time, cost, and non-determinism. It records every
// call so a test can assert the pipeline reached (or never reached) the
// worker-spawn step, and returns Result (or Err, if set) as the canned
// outcome.
type FakeSpawner struct {
	mu     sync.Mutex
	calls  []FakeSpawnerCall
	Result daemon.WorkerResult
	Err    error
}

// Run implements daemon.Spawner. It is safe for concurrent use, since
// OnNewMail spawns each pipeline in its own goroutine.
func (s *FakeSpawner) Run(_ context.Context, missionID, mcpConfigPath, systemPromptPath string, tools []string, envOverrides map[string]string) (daemon.WorkerResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Best-effort: the caller (ClaudeRunner.Run) removes both files via
	// deferred os.Remove once Run returns, so this is the only window in
	// which their contents are readable. A read failure is not this
	// method's failure to report -- some test paths never populate these
	// files at all (e.g. the spawner is never reached) -- so a failed
	// read simply leaves the field nil rather than erroring Run.
	mcpContent, _ := os.ReadFile(mcpConfigPath)       //nolint:gosec // test fixture: paths come from ClaudeRunner.Run's own os.CreateTemp call, not external input
	promptContent, _ := os.ReadFile(systemPromptPath) //nolint:gosec // test fixture: paths come from ClaudeRunner.Run's own os.CreateTemp call, not external input

	s.calls = append(s.calls, FakeSpawnerCall{
		MissionID:           missionID,
		MCPConfigPath:       mcpConfigPath,
		SystemPromptPath:    systemPromptPath,
		Tools:               cloneTools(tools),
		EnvOverrides:        cloneEnvOverrides(envOverrides),
		MCPConfigContent:    mcpContent,
		SystemPromptContent: promptContent,
	})
	if s.Err != nil {
		return daemon.WorkerResult{}, s.Err
	}
	result := s.Result
	result.MissionID = missionID
	return result, nil
}

// Calls returns a copy of every call recorded so far, safe to read
// concurrently with Run.
func (s *FakeSpawner) Calls() []FakeSpawnerCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]FakeSpawnerCall(nil), s.calls...)
}

// cloneTools returns a copy of t, for the same reason cloneEnvOverrides
// copies its map: a recorded FakeSpawnerCall must not change after Run
// returns because a caller mutated or reused the slice it passed in.
// Preserves the nil/non-nil distinction.
func cloneTools(t []string) []string {
	if t == nil {
		return nil
	}
	return append([]string(nil), t...)
}

// cloneEnvOverrides returns a copy of m, so a recorded FakeSpawnerCall
// cannot change after Run returns because a caller mutated (or reused) the
// map it passed in. Preserves the nil/non-nil distinction: a nil m yields a
// nil clone.
func cloneEnvOverrides(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	clone := make(map[string]string, len(m))
	for k, v := range m {
		clone[k] = v
	}
	return clone
}
