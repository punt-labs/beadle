package secret

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFileGet(t *testing.T) {
	// Override HOME so fileGet resolves to our temp dir
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	cfgDir := filepath.Join(dir, ".punt-labs", "beadle", "secrets")
	require.NoError(t, os.MkdirAll(cfgDir, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(cfgDir, "test-cred"), []byte("s3cret\n"), 0600))

	val, err := fileGet("test-cred")
	require.NoError(t, err)
	assert.Equal(t, "s3cret", val)
}

func TestFileGet_UnsafePerms(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	cfgDir := filepath.Join(dir, ".punt-labs", "beadle", "secrets")
	require.NoError(t, os.MkdirAll(cfgDir, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(cfgDir, "world-readable"), []byte("s3cret\n"), 0644))

	_, err := fileGet("world-readable")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsafe permissions")
}

func TestGet_BadPermissionsFile_NotErrNotFound(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	cfgDir := filepath.Join(dir, ".punt-labs", "beadle", "secrets")
	require.NoError(t, os.MkdirAll(cfgDir, 0700))
	// Use a name that cannot exist in any real OS keychain.
	const credName = "__test_bad_perms_beadle__"
	credPath := filepath.Join(cfgDir, credName)
	require.NoError(t, os.WriteFile(credPath, []byte("s3cret\n"), 0644))

	_, err := Get(credName)
	require.Error(t, err)
	// Must NOT be ErrNotFound — the file exists but has bad permissions.
	assert.False(t, errors.Is(err, ErrNotFound), "bad-permissions error should not wrap ErrNotFound")
	assert.Contains(t, err.Error(), "unsafe permissions")
}

func TestGet_EnvFallback(t *testing.T) {
	t.Setenv("BEADLE_MY_SECRET", "from-env")

	val, err := Get("my-secret")
	require.NoError(t, err)
	assert.Equal(t, "from-env", val)
}

func TestGet_NotFound(t *testing.T) {
	_, err := Get("nonexistent-credential-xyz")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestAvailable(t *testing.T) {
	backends := Available()
	assert.NotEmpty(t, backends)
	// File and env are always available
	assert.Contains(t, backends, "file (~/.punt-labs/beadle/secrets/)")
	assert.Contains(t, backends, "environment variable")
}

func TestGet_PathTraversal(t *testing.T) {
	_, err := Get("../../etc/passwd")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "path separator")
}
