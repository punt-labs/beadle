package testenv

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/punt-labs/beadle/internal/enable"
)

// EthosVersion is the pinned ethos CLI version this test tier requires --
// kept in sync by hand with Makefile's ETHOS_VERSION (see
// docs/daemon-test-harness.md's "Pinning ethos"). It appears only in
// EthosOrFatal's human-facing failure message, never in an assertion.
const EthosVersion = "v4.16.0"

// EthosOrFatal returns the ethos binary's path, or fails t naming the
// install remedy. Per this test tier's hard constraint, a missing
// dependency is a test failure naming what to install, never a t.Skip --
// exactly the failure mode that let beadle-8gt's regression coverage skip
// silently in CI (docs/daemon-test-harness.md). `make test`/`make check`
// provision ethos via the tools-ethos Makefile target, so this should only
// ever fire on a developer machine that bypassed make.
func EthosOrFatal(t testing.TB) string {
	t.Helper()
	path, err := exec.LookPath("ethos")
	if err != nil {
		t.Fatalf("ethos not found on PATH: install it via `go install github.com/punt-labs/ethos/v4/cmd/ethos@%s` (or run `make tools-ethos`): %v", EthosVersion, err)
	}
	return path
}

// IsolateEthos redirects $HOME and $ETHOS_REPO_ROOT to fresh scratch
// directories for the remainder of t, then symlinks back only the subtrees
// a real `ethos` CLI invocation reads (never writes) so contract validation
// exercises the genuine schema and identity graph while every subtree ethos
// might write to — global sessions/missions/delegations/locks/counters,
// repo missions/sessions — starts absent and empty. It returns the scratch
// $HOME so a caller that needs to build more state under it (New does, for
// beadle's own identity/contacts layout) does not have to create a second,
// unrelated fake HOME.
//
// Call it before any other code touches $HOME: it captures the real,
// ambient $HOME (and the real repo root) to source the symlinks from, and
// calling it after $HOME has already been redirected would symlink from
// the wrong tree. New calls it first for exactly this reason.
//
// Every ETHOS_* environment variable already set in this process is
// stripped, not just emptied — notably ETHOS_SESSION, which the calling
// agent's own session sets. Leaving it in place would let an exec'd ethos
// binary resolve a real session identifier while its backing storage sits
// under these redirected scratch roots, the same half-redirect this
// function's HOME/ETHOS_REPO_ROOT split otherwise avoids, just for a
// different variable.
//
// This repo runs in a shared, multi-agent environment: without this,
// `ethos mission create`/`ethos mission abandon` corrupt whatever mission a
// real concurrent session has bound, and burn real per-date mission-ID
// counter slots.
func IsolateEthos(t testing.TB) string {
	t.Helper()

	for _, kv := range os.Environ() {
		key, _, ok := strings.Cut(kv, "=")
		if ok && strings.HasPrefix(key, "ETHOS_") {
			unsetenvForTest(t, key)
		}
	}

	realHome, err := os.UserHomeDir()
	require.NoError(t, err)
	realRepoRoot, err := enable.RepoRoot()
	require.NoError(t, err)

	scratchHome := t.TempDir()
	scratchGlobalEthos := filepath.Join(scratchHome, ".punt-labs", "ethos")
	require.NoError(t, os.MkdirAll(scratchGlobalEthos, 0o700))
	require.NoError(t, os.Symlink(
		filepath.Join(realHome, ".punt-labs", "ethos", "archetypes"),
		filepath.Join(scratchGlobalEthos, "archetypes"),
	))
	t.Setenv("HOME", scratchHome)

	scratchRepo := t.TempDir()
	scratchRepoEthos := filepath.Join(scratchRepo, ".punt-labs", "ethos")
	require.NoError(t, os.MkdirAll(scratchRepoEthos, 0o700))
	realRepoEthos := filepath.Join(realRepoRoot, ".punt-labs", "ethos")
	for _, sub := range []string{"identities", "personalities", "roles", "teams", "talents", "writing-styles"} {
		require.NoError(t, os.Symlink(filepath.Join(realRepoEthos, sub), filepath.Join(scratchRepoEthos, sub)))
	}
	t.Setenv("ETHOS_REPO_ROOT", scratchRepo)

	return scratchHome
}

// unsetenvForTest removes key from the process environment for the
// remainder of t, restoring its prior value (or absence) on cleanup.
// testing.TB has no built-in Unsetenv — t.Setenv can only set a value, not
// remove one — but a subprocess that inherits os.Environ() (every
// exec.Command that leaves .Env nil, which is how the real ethos CLI is
// invoked throughout internal/daemon) needs the variable genuinely absent,
// not merely emptied.
func unsetenvForTest(t testing.TB, key string) {
	t.Helper()
	prev, had := os.LookupEnv(key)
	require.NoError(t, os.Unsetenv(key))
	t.Cleanup(func() {
		if had {
			_ = os.Setenv(key, prev)
		} else {
			_ = os.Unsetenv(key)
		}
	})
}
