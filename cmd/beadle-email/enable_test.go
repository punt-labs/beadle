package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/punt-labs/beadle/internal/claudemd"
)

func beadleDir(root string) string  { return filepath.Join(root, ".punt-labs", "beadle") }
func markerPath(root string) string { return filepath.Join(beadleDir(root), "enabled") }
func hostPath(root string) string   { return filepath.Join(root, "CLAUDE.md") }

func exists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Stat(path)
	if err == nil {
		return true
	}
	require.True(t, os.IsNotExist(err), "unexpected stat error: %v", err)
	return false
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(data)
}

func TestEnableDepositsGuideMarkerAndImport(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, enableRepo(root))

	guide := filepath.Join(beadleDir(root), "CLAUDE.md")
	assert.Equal(t, string(claudemd.Guide), read(t, guide), "guide deposited verbatim")
	assert.True(t, exists(t, markerPath(root)), "enabled marker written")
	assert.Equal(t, importLine+"\n", read(t, hostPath(root)),
		"import is the sole content of a repo CLAUDE.md created from nothing")
}

func TestEnablePreservesExistingCLAUDEMD(t *testing.T) {
	root := t.TempDir()
	existing := "# Team rules\n\nBe kind.\n"
	require.NoError(t, os.WriteFile(hostPath(root), []byte(existing), 0o644))

	require.NoError(t, enableRepo(root))
	assert.Equal(t, existing+importLine+"\n", read(t, hostPath(root)))
}

func TestEnableIdempotent(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, enableRepo(root))
	first := read(t, hostPath(root))

	require.NoError(t, enableRepo(root), "re-run is the upgrade path")
	assert.Equal(t, first, read(t, hostPath(root)), "no duplicate import line")
}

func TestEnableFailedRegisterLeavesNoMarker(t *testing.T) {
	root := t.TempDir()
	// Force Register to fail by making the host CLAUDE.md a directory, which
	// cannot be read. The marker is the enabled-iff-import signal, so a failed
	// import must leave no marker — the repo must never look enabled without it.
	require.NoError(t, os.Mkdir(hostPath(root), 0o755))

	err := enableRepo(root)
	require.Error(t, err)
	assert.False(t, exists(t, markerPath(root)), "a failed enable leaves no marker")
}

// captureStderr redirects os.Stderr for the duration of fn and returns what was
// written. Tests in this package run sequentially, so the global swap is safe.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stderr = w
	defer func() { os.Stderr = old }()
	fn()
	require.NoError(t, w.Close())
	out, err := io.ReadAll(r)
	require.NoError(t, err)
	return string(out)
}

func TestEnableDisableQuietSuppressesProgress(t *testing.T) {
	defer func(prev bool) { g.Quiet = prev }(g.Quiet)

	g.Quiet = false
	loud := captureStderr(t, func() {
		require.NoError(t, enableRepo(t.TempDir()))
	})
	assert.NotEmpty(t, loud, "default enable prints progress")

	quietRoot := t.TempDir()
	g.Quiet = true
	quietEnable := captureStderr(t, func() {
		require.NoError(t, enableRepo(quietRoot))
	})
	assert.Empty(t, quietEnable, "--quiet suppresses enable progress")

	quietDisable := captureStderr(t, func() {
		require.NoError(t, disableRepo(quietRoot, false))
	})
	assert.Empty(t, quietDisable, "--quiet suppresses disable progress")
}

func TestConcurrentEnableDisableReachConsistentState(t *testing.T) {
	root := t.TempDir()
	// Seed a user CLAUDE.md so neither end state deletes the file; the check is
	// purely about the import line and the marker moving together.
	require.NoError(t, os.WriteFile(hostPath(root), []byte("# Rules\n"), 0o644))

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); assert.NoError(t, enableRepo(root)) }()
	go func() { defer wg.Done(); assert.NoError(t, disableRepo(root, false)) }()
	wg.Wait()

	// The per-repo lock serializes the two, so the end state is whichever ran
	// last: fully enabled (marker and import both present) or fully dormant
	// (neither) — never the §2.11-incorrect marker without its import.
	marker := exists(t, markerPath(root))
	imported := strings.Contains(read(t, hostPath(root)), importLine)
	if marker {
		assert.True(t, imported, "enabled end state: marker implies import present")
	} else {
		assert.False(t, imported, "dormant end state: no marker and no import")
	}
}

func TestDisableClearsMarkerBeforePruneFailure(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(beadleDir(root), 0o750))
	require.NoError(t, os.WriteFile(markerPath(root), nil, 0o644))
	// Make CLAUDE.md a directory so Prune fails on read. disable clears the
	// marker first, so even when the later prune errors the marker is already
	// gone — never a marker left present while its import is not (§2.11).
	require.NoError(t, os.Mkdir(hostPath(root), 0o755))

	err := disableRepo(root, false)
	require.Error(t, err)
	assert.False(t, exists(t, markerPath(root)), "marker cleared before the failing prune")
}

func TestDisableLeavesDirectoryDormant(t *testing.T) {
	root := t.TempDir()
	existing := "# Team rules\n"
	require.NoError(t, os.WriteFile(hostPath(root), []byte(existing), 0o644))
	require.NoError(t, enableRepo(root))
	require.NoError(t, disableRepo(root, false))

	assert.Equal(t, existing, read(t, hostPath(root)), "user prose restored, import gone")
	assert.False(t, exists(t, markerPath(root)), "marker deleted")
	assert.Equal(t, string(claudemd.Guide), read(t, filepath.Join(beadleDir(root), "CLAUDE.md")),
		"guide stays dormant, not erased")
}

func TestDisablePurgeRemovesDirectory(t *testing.T) {
	root := t.TempDir()
	existing := "# Team rules\n"
	require.NoError(t, os.WriteFile(hostPath(root), []byte(existing), 0o644))
	require.NoError(t, enableRepo(root))
	require.NoError(t, disableRepo(root, true))

	assert.Equal(t, existing, read(t, hostPath(root)), "user prose restored, import gone")
	assert.False(t, exists(t, beadleDir(root)), "--purge removes the whole directory")
}

func TestDisableRemovesEmptyCLAUDEMD(t *testing.T) {
	root := t.TempDir()
	// No prior CLAUDE.md: enable creates it holding only the import line, so
	// disable's prune empties it and must remove the 0-byte file it left.
	require.NoError(t, enableRepo(root))
	require.NoError(t, disableRepo(root, false))

	assert.False(t, exists(t, hostPath(root)), "a CLAUDE.md created from nothing is removed")
	assert.False(t, exists(t, markerPath(root)), "marker deleted")
	assert.True(t, exists(t, filepath.Join(beadleDir(root), "CLAUDE.md")),
		"the .punt-labs/beadle dir stays dormant")
}

func TestDisableKeepsPreexistingEmptyCLAUDEMD(t *testing.T) {
	root := t.TempDir()
	// A user's own empty CLAUDE.md, never enabled: disable finds no import to
	// prune (wrote==false), so it must not delete a file it did not empty.
	require.NoError(t, os.WriteFile(hostPath(root), nil, 0o644))
	require.NoError(t, disableRepo(root, false))

	require.True(t, exists(t, hostPath(root)), "a no-op disable leaves the user's file")
	assert.Equal(t, "", read(t, hostPath(root)))
}

func TestDisableKeepsSymlinkedCLAUDEMD(t *testing.T) {
	root := t.TempDir()
	// CLAUDE.md is a symlink into a dotfile store. enable appends the import to
	// the real target; disable prunes it back to empty. The 0-byte target must
	// not tempt disable into unlinking the symlink — the link and its target
	// both survive.
	store := t.TempDir()
	target := filepath.Join(store, "CLAUDE.md")
	require.NoError(t, os.WriteFile(target, nil, 0o644))
	require.NoError(t, os.Symlink(target, hostPath(root)))

	require.NoError(t, enableRepo(root))
	require.NoError(t, disableRepo(root, false))

	fi, err := os.Lstat(hostPath(root))
	require.NoError(t, err)
	assert.NotZero(t, fi.Mode()&os.ModeSymlink, "the symlink survives disable")
	require.True(t, exists(t, target), "the real target survives disable")
	assert.Equal(t, "", read(t, target), "the target is empty, import pruned")
}

func TestDisableWithoutEnableIsClean(t *testing.T) {
	root := t.TempDir()
	// Disable on a repo that was never enabled: no marker, no import, no error.
	require.NoError(t, disableRepo(root, false))
	assert.False(t, exists(t, markerPath(root)))
	assert.False(t, exists(t, hostPath(root)), "disable does not create CLAUDE.md")
}

func TestEnableDisableRoundTrip(t *testing.T) {
	root := t.TempDir()
	existing := "# Rules\n"
	require.NoError(t, os.WriteFile(hostPath(root), []byte(existing), 0o644))

	require.NoError(t, enableRepo(root))
	require.NoError(t, disableRepo(root, false))
	assert.Equal(t, existing, read(t, hostPath(root)),
		"enable then disable restores the user's CLAUDE.md byte-for-byte")
}
