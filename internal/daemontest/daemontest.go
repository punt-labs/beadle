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
	EnvOverrides     map[string]string
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
func (s *FakeSpawner) Run(_ context.Context, missionID, mcpConfigPath, systemPromptPath string, envOverrides map[string]string) (daemon.WorkerResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.calls = append(s.calls, FakeSpawnerCall{
		MissionID:        missionID,
		MCPConfigPath:    mcpConfigPath,
		SystemPromptPath: systemPromptPath,
		EnvOverrides:     envOverrides,
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
