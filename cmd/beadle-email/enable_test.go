package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	assert.True(t, exists(t, guide), "guide deposited")
	assert.NotEmpty(t, read(t, guide), "guide has content")
	assert.True(t, exists(t, markerPath(root)), "enabled marker written")
	assert.Contains(t, read(t, hostPath(root)), importLine, "import added to repo CLAUDE.md")
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

func TestDisableLeavesDirectoryDormant(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, enableRepo(root))
	require.NoError(t, disableRepo(root, false))

	assert.NotContains(t, read(t, hostPath(root)), importLine, "import removed")
	assert.False(t, exists(t, markerPath(root)), "marker deleted")
	assert.True(t, exists(t, filepath.Join(beadleDir(root), "CLAUDE.md")),
		"guide stays dormant, not erased")
}

func TestDisablePurgeRemovesDirectory(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, enableRepo(root))
	require.NoError(t, disableRepo(root, true))

	assert.NotContains(t, read(t, hostPath(root)), importLine)
	assert.False(t, exists(t, beadleDir(root)), "--purge removes the whole directory")
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
