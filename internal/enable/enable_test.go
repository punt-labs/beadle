package enable

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/punt-labs/beadle/internal/claudemd"
)

func beadleDir(root string) string  { return filepath.Join(root, ".punt-labs", "beadle") }
func markerPath(root string) string { return filepath.Join(beadleDir(root), "enabled") }
func hostPath(root string) string   { return filepath.Join(root, "CLAUDE.md") }

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(data)
}

func exists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Stat(path)
	if err == nil {
		return true
	}
	require.True(t, os.IsNotExist(err), "unexpected stat error: %v", err)
	return false
}

func TestEnableDepositsGuideMarkerAndImport(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, Enable(root, nil))

	assert.Equal(t, string(claudemd.Guide), read(t, filepath.Join(beadleDir(root), "CLAUDE.md")),
		"guide deposited verbatim")
	assert.True(t, exists(t, markerPath(root)), "enabled marker written")
	assert.Equal(t, ImportLine+"\n", read(t, hostPath(root)),
		"import is the sole content of a repo CLAUDE.md created from nothing")
}

func TestEnableIdempotent(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, Enable(root, nil))
	first := read(t, hostPath(root))
	require.NoError(t, Enable(root, nil), "re-run is the upgrade path")
	assert.Equal(t, first, read(t, hostPath(root)), "no duplicate import line")
}

func TestEnableFailedRegisterLeavesNoMarker(t *testing.T) {
	root := t.TempDir()
	// A directory where CLAUDE.md should be makes Register fail. The marker is
	// the enabled-iff-import signal, so a failed import must leave no marker —
	// the repo must never look enabled without it (§2.11).
	require.NoError(t, os.Mkdir(hostPath(root), 0o755))

	require.Error(t, Enable(root, nil))
	assert.False(t, exists(t, markerPath(root)), "a failed enable leaves no marker")
}

func TestDisableClearsMarkerBeforePruneFailure(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(beadleDir(root), 0o750))
	require.NoError(t, os.WriteFile(markerPath(root), nil, 0o644))
	// A directory where CLAUDE.md should be makes Prune fail on read. Disable
	// clears the marker first, so even when the later prune errors the marker is
	// already gone — never a marker present while its import is not (§2.11).
	require.NoError(t, os.Mkdir(hostPath(root), 0o755))

	require.Error(t, Disable(root, false, nil))
	assert.False(t, exists(t, markerPath(root)), "marker cleared before the failing prune")
}

func TestDisableLeavesDirectoryDormant(t *testing.T) {
	root := t.TempDir()
	existing := "# Team rules\n"
	require.NoError(t, os.WriteFile(hostPath(root), []byte(existing), 0o644))
	require.NoError(t, Enable(root, nil))
	require.NoError(t, Disable(root, false, nil))

	assert.Equal(t, existing, read(t, hostPath(root)), "user prose restored, import gone")
	assert.False(t, exists(t, markerPath(root)), "marker deleted")
	assert.Equal(t, string(claudemd.Guide), read(t, filepath.Join(beadleDir(root), "CLAUDE.md")),
		"guide stays dormant, not erased")
}

func TestDisablePurgeRemovesDirectory(t *testing.T) {
	root := t.TempDir()
	existing := "# Team rules\n"
	require.NoError(t, os.WriteFile(hostPath(root), []byte(existing), 0o644))
	require.NoError(t, Enable(root, nil))
	require.NoError(t, Disable(root, true, nil))

	assert.Equal(t, existing, read(t, hostPath(root)), "user prose restored, import gone")
	assert.False(t, exists(t, beadleDir(root)), "--purge removes the whole directory")
}

func TestEnableDisableRoundTrip(t *testing.T) {
	root := t.TempDir()
	existing := "# Rules\n"
	require.NoError(t, os.WriteFile(hostPath(root), []byte(existing), 0o644))

	require.NoError(t, Enable(root, nil))
	require.NoError(t, Disable(root, false, nil))
	assert.Equal(t, existing, read(t, hostPath(root)),
		"enable then disable restores the user's CLAUDE.md byte-for-byte")
}

func TestConcurrentEnableDisableReachConsistentState(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(hostPath(root), []byte("# Rules\n"), 0o644))

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); assert.NoError(t, Enable(root, nil)) }()
	go func() { defer wg.Done(); assert.NoError(t, Disable(root, false, nil)) }()
	wg.Wait()

	// The per-repo lock serializes the two, so the end state is whichever ran
	// last: fully enabled (marker and import both present) or fully dormant
	// (neither) — never the §2.11-incorrect marker without its import.
	marker := exists(t, markerPath(root))
	host := read(t, hostPath(root))
	imported := host == ImportLine+"\n" || host == "# Rules\n"+ImportLine+"\n"
	if marker {
		assert.True(t, imported, "enabled end state: marker implies import present")
	} else {
		assert.Equal(t, "# Rules\n", host, "dormant end state: no marker and no import")
	}
}

func TestProgressfNilIsSafe(t *testing.T) {
	// A nil Progressf discards output; the MCP surface relies on this.
	var p Progressf
	assert.NotPanics(t, func() { p.printf("anything %d\n", 1) })
}
