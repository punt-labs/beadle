package identity

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolve_EthosWithRepoConfig(t *testing.T) {
	ethosDir := t.TempDir()
	beadleDir := t.TempDir()
	repoDir := t.TempDir()

	// Set up repo-local ethos config
	repoEthosDir := filepath.Join(repoDir, ".punt-labs", "ethos")
	require.NoError(t, os.MkdirAll(repoEthosDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(repoEthosDir, "config.yaml"), []byte("active: claude\n"), 0o640))

	// Set up ethos identity
	idDir := filepath.Join(ethosDir, "identities")
	require.NoError(t, os.MkdirAll(idDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(idDir, "claude.yaml"), []byte("name: Claude Agento\nhandle: claude\nemail: claude@punt-labs.com\n"), 0o640))

	// Set up beadle extension
	extDir := filepath.Join(idDir, "claude.ext")
	require.NoError(t, os.MkdirAll(extDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(extDir, "beadle.yaml"), []byte("gpg_key_id: ABCD1234\n"), 0o640))

	r := NewResolver(ethosDir, beadleDir, repoDir)
	id, err := r.Resolve()
	require.NoError(t, err)

	assert.Equal(t, "claude", id.Handle)
	assert.Equal(t, "Claude Agento", id.Name)
	assert.Equal(t, "claude@punt-labs.com", id.Email)
	assert.Equal(t, "ABCD1234", id.GPGKeyID)
	assert.Equal(t, "claude@punt-labs.com", id.OwnerEmail) // no owner_email in ext → defaults to own email
	assert.Equal(t, "ethos", id.Source)
}

func TestResolve_EthosWithOwnerEmail(t *testing.T) {
	ethosDir := t.TempDir()
	beadleDir := t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(ethosDir, "active"), []byte("claude"), 0o640))

	idDir := filepath.Join(ethosDir, "identities")
	require.NoError(t, os.MkdirAll(idDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(idDir, "claude.yaml"), []byte("name: Claude\nhandle: claude\nemail: claude@punt-labs.com\n"), 0o640))

	extDir := filepath.Join(idDir, "claude.ext")
	require.NoError(t, os.MkdirAll(extDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(extDir, "beadle.yaml"), []byte("gpg_key_id: KEY1\nowner_email: jim@punt-labs.com\n"), 0o640))

	r := NewResolver(ethosDir, beadleDir, "")
	id, err := r.Resolve()
	require.NoError(t, err)
	assert.Equal(t, "jim@punt-labs.com", id.OwnerEmail)
	assert.Equal(t, "KEY1", id.GPGKeyID)
}

func TestResolve_EthosCorruptExtensionErrors(t *testing.T) {
	ethosDir := t.TempDir()
	beadleDir := t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(ethosDir, "active"), []byte("claude"), 0o640))

	idDir := filepath.Join(ethosDir, "identities")
	require.NoError(t, os.MkdirAll(idDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(idDir, "claude.yaml"), []byte("name: Claude\nhandle: claude\nemail: claude@punt-labs.com\n"), 0o640))

	// Corrupt beadle extension
	extDir := filepath.Join(idDir, "claude.ext")
	require.NoError(t, os.MkdirAll(extDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(extDir, "beadle.yaml"), []byte("{{not yaml"), 0o640))

	r := NewResolver(ethosDir, beadleDir, "")
	_, err := r.Resolve()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse beadle extension")
}

func TestResolve_HandlePathTraversal(t *testing.T) {
	ethosDir := t.TempDir()
	beadleDir := t.TempDir()
	repoDir := t.TempDir()

	// Repo config with malicious handle
	repoEthosDir := filepath.Join(repoDir, ".punt-labs", "ethos")
	require.NoError(t, os.MkdirAll(repoEthosDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(repoEthosDir, "config.yaml"), []byte("active: ../../../etc/passwd\n"), 0o640))

	r := NewResolver(ethosDir, beadleDir, repoDir)
	_, err := r.Resolve()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "path separator")
}

func TestResolve_EthosGlobalActive(t *testing.T) {
	ethosDir := t.TempDir()
	beadleDir := t.TempDir()

	// Set up global active file (no repo config)
	require.NoError(t, os.WriteFile(filepath.Join(ethosDir, "active"), []byte("jfreeman\n"), 0o640))

	// Set up ethos identity
	idDir := filepath.Join(ethosDir, "identities")
	require.NoError(t, os.MkdirAll(idDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(idDir, "jfreeman.yaml"), []byte("name: Jim Freeman\nhandle: jfreeman\nemail: jim@punt-labs.com\n"), 0o640))

	r := NewResolver(ethosDir, beadleDir, "")
	id, err := r.Resolve()
	require.NoError(t, err)

	assert.Equal(t, "jfreeman", id.Handle)
	assert.Equal(t, "Jim Freeman", id.Name)
	assert.Equal(t, "jim@punt-labs.com", id.Email)
	assert.Equal(t, "", id.GPGKeyID)
	assert.Equal(t, "ethos", id.Source)
}

func TestResolve_DefaultIdentity(t *testing.T) {
	ethosDir := t.TempDir() // empty — no ethos
	beadleDir := t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(beadleDir, "default-identity"), []byte("claude@punt-labs.com\n"), 0o640))

	r := NewResolver(ethosDir, beadleDir, "")
	id, err := r.Resolve()
	require.NoError(t, err)

	assert.Equal(t, "", id.Handle)
	assert.Equal(t, "claude@punt-labs.com", id.Email)
	assert.Equal(t, "default", id.Source)
}

func TestResolve_LegacyFallback(t *testing.T) {
	ethosDir := t.TempDir()
	beadleDir := t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(beadleDir, "email.json"), []byte(`{"from_address":"legacy@example.com"}`), 0o640))

	r := NewResolver(ethosDir, beadleDir, "")
	id, err := r.Resolve()
	require.NoError(t, err)

	assert.Equal(t, "legacy@example.com", id.Email)
	assert.Equal(t, "legacy", id.Source)
}

func TestResolve_NothingFound(t *testing.T) {
	ethosDir := t.TempDir()
	beadleDir := t.TempDir()

	r := NewResolver(ethosDir, beadleDir, "")
	_, err := r.Resolve()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no identity found")
}

func TestResolve_EthosNoEmail(t *testing.T) {
	ethosDir := t.TempDir()
	beadleDir := t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(ethosDir, "active"), []byte("broken"), 0o640))
	idDir := filepath.Join(ethosDir, "identities")
	require.NoError(t, os.MkdirAll(idDir, 0o750))
	// Identity YAML with no email field
	require.NoError(t, os.WriteFile(filepath.Join(idDir, "broken.yaml"), []byte("name: Broken\nhandle: broken\n"), 0o640))

	// Should error — ethos has an active handle but the identity is unreadable.
	// Operating as the wrong identity is worse than failing.
	r := NewResolver(ethosDir, beadleDir, "")
	_, err := r.Resolve()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ethos active identity")
}

func TestResolve_RepoConfigOverridesGlobal(t *testing.T) {
	ethosDir := t.TempDir()
	beadleDir := t.TempDir()
	repoDir := t.TempDir()

	// Global active = jfreeman
	require.NoError(t, os.WriteFile(filepath.Join(ethosDir, "active"), []byte("jfreeman"), 0o640))

	// Repo config = claude (should win)
	repoEthosDir := filepath.Join(repoDir, ".punt-labs", "ethos")
	require.NoError(t, os.MkdirAll(repoEthosDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(repoEthosDir, "config.yaml"), []byte("active: claude\n"), 0o640))

	// Set up both identities
	idDir := filepath.Join(ethosDir, "identities")
	require.NoError(t, os.MkdirAll(idDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(idDir, "claude.yaml"), []byte("name: Claude\nhandle: claude\nemail: claude@punt-labs.com\n"), 0o640))
	require.NoError(t, os.WriteFile(filepath.Join(idDir, "jfreeman.yaml"), []byte("name: Jim\nhandle: jfreeman\nemail: jim@punt-labs.com\n"), 0o640))

	r := NewResolver(ethosDir, beadleDir, repoDir)
	id, err := r.Resolve()
	require.NoError(t, err)

	assert.Equal(t, "claude", id.Handle)
	assert.Equal(t, "claude@punt-labs.com", id.Email)
}

func TestResolve_NoExtension(t *testing.T) {
	ethosDir := t.TempDir()
	beadleDir := t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(ethosDir, "active"), []byte("claude"), 0o640))
	idDir := filepath.Join(ethosDir, "identities")
	require.NoError(t, os.MkdirAll(idDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(idDir, "claude.yaml"), []byte("name: Claude\nhandle: claude\nemail: claude@punt-labs.com\n"), 0o640))
	// No .ext/beadle.yaml — should still work

	r := NewResolver(ethosDir, beadleDir, "")
	id, err := r.Resolve()
	require.NoError(t, err)
	assert.Equal(t, "", id.GPGKeyID)
	assert.Equal(t, "ethos", id.Source)
}
