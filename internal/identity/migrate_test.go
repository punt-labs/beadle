package identity

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnsureIdentityDir_CreatesDir(t *testing.T) {
	beadleDir := t.TempDir()

	dir, err := EnsureIdentityDir(beadleDir, "test@example.com")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(beadleDir, "identities", "test@example.com"), dir)

	info, err := os.Stat(dir)
	require.NoError(t, err)
	assert.True(t, info.IsDir())
}

func TestEnsureIdentityDir_MigratesRootFiles(t *testing.T) {
	beadleDir := t.TempDir()

	// Create root files
	require.NoError(t, os.WriteFile(filepath.Join(beadleDir, "email.json"), []byte(`{"imap_host":"127.0.0.1"}`), 0o640))
	require.NoError(t, os.WriteFile(filepath.Join(beadleDir, "contacts.json"), []byte(`[{"name":"Jim"}]`), 0o640))

	dir, err := EnsureIdentityDir(beadleDir, "test@example.com")
	require.NoError(t, err)

	// Check copies exist
	data, err := os.ReadFile(filepath.Join(dir, "email.json"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "127.0.0.1")

	data, err = os.ReadFile(filepath.Join(dir, "contacts.json"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "Jim")

	// Root files should still exist (copy, not move)
	_, err = os.Stat(filepath.Join(beadleDir, "email.json"))
	require.NoError(t, err)
}

func TestEnsureIdentityDir_Idempotent(t *testing.T) {
	beadleDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(beadleDir, "email.json"), []byte(`{"original":true}`), 0o640))

	// First call migrates
	dir, err := EnsureIdentityDir(beadleDir, "test@example.com")
	require.NoError(t, err)

	// Modify the identity-dir copy
	require.NoError(t, os.WriteFile(filepath.Join(dir, "email.json"), []byte(`{"modified":true}`), 0o640))

	// Second call should NOT overwrite
	_, err = EnsureIdentityDir(beadleDir, "test@example.com")
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dir, "email.json"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "modified")
}

func TestEnsureIdentityDir_NoRootFiles(t *testing.T) {
	beadleDir := t.TempDir()

	// No root email.json or contacts.json — should still create dir
	dir, err := EnsureIdentityDir(beadleDir, "test@example.com")
	require.NoError(t, err)

	info, err := os.Stat(dir)
	require.NoError(t, err)
	assert.True(t, info.IsDir())

	// Identity dir should have no files
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Empty(t, entries)
}

func TestEnsureIdentityDir_EmptyEmail(t *testing.T) {
	beadleDir := t.TempDir()
	_, err := EnsureIdentityDir(beadleDir, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "email is required")
}
