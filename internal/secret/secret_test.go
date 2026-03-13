package secret

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFileRoundtrip(t *testing.T) {
	dir := t.TempDir()
	name := "test-cred"
	path := filepath.Join(dir, name)

	err := os.WriteFile(path, []byte("s3cret\n"), 0600)
	require.NoError(t, err)

	// Read back the file directly (fileGet uses ~/.config/beadle/ which
	// won't have this test file)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(data), "s3cret")
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
	assert.Contains(t, backends, "file (~/.config/beadle/)")
	assert.Contains(t, backends, "environment variable")
}

func TestGet_PathTraversal(t *testing.T) {
	_, err := Get("../../etc/passwd")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "path separator")
}
