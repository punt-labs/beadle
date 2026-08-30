package daemon

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/punt-labs/beadle/internal/identity"
)

// setupResolver builds an identity.Resolver against a temp ethos dir with a
// single ethos identity named handle, whose beadle extension carries
// gpgKeyID (omitted from the extension file entirely when gpgKeyID == "").
func setupResolver(t *testing.T, handle, gpgKeyID string) *identity.Resolver {
	t.Helper()
	ethosDir := t.TempDir()
	beadleDir := t.TempDir()

	idDir := filepath.Join(ethosDir, "identities")
	require.NoError(t, os.MkdirAll(idDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(idDir, handle+".yaml"),
		[]byte("name: Test\nhandle: "+handle+"\nemail: "+handle+"@example.com\n"), 0o640))

	if gpgKeyID != "" {
		extDir := filepath.Join(idDir, handle+".ext")
		require.NoError(t, os.MkdirAll(extDir, 0o750))
		require.NoError(t, os.WriteFile(filepath.Join(extDir, "beadle.yaml"),
			[]byte("gpg_key_id: "+gpgKeyID+"\n"), 0o640))
	}

	return identity.NewResolver(ethosDir, beadleDir, "")
}

func TestConfig_ResolveOwnerKeyID(t *testing.T) {
	validFpr := strings.Repeat("A", 40)

	tests := []struct {
		name       string
		cfg        Config
		resolver   *identity.Resolver
		wantKeyID  string
		wantErrSub string
	}{
		{
			name:       "both fields empty",
			cfg:        Config{},
			resolver:   setupResolver(t, "owner", validFpr),
			wantErrSub: "no default owner",
		},
		{
			name: "both fields set — ambiguous",
			cfg: Config{
				OwnerHandle:   "owner",
				OwnerGPGKeyID: validFpr,
			},
			resolver:   setupResolver(t, "owner", validFpr),
			wantErrSub: "ambiguous",
		},
		{
			name:      "owner_gpg_key_id alone, well-formed",
			cfg:       Config{OwnerGPGKeyID: validFpr},
			resolver:  setupResolver(t, "owner", validFpr),
			wantKeyID: validFpr,
		},
		{
			name:       "owner_gpg_key_id malformed",
			cfg:        Config{OwnerGPGKeyID: "not-a-fingerprint"},
			resolver:   setupResolver(t, "owner", validFpr),
			wantErrSub: "not a full 40-hex OpenPGP fingerprint",
		},
		{
			name:      "owner_handle alone, resolves to well-formed fingerprint",
			cfg:       Config{OwnerHandle: "owner"},
			resolver:  setupResolver(t, "owner", validFpr),
			wantKeyID: validFpr,
		},
		{
			name:       "owner_handle malformed fingerprint in extension",
			cfg:        Config{OwnerHandle: "owner"},
			resolver:   setupResolver(t, "owner", "short"),
			wantErrSub: "not a full 40-hex OpenPGP fingerprint",
		},
		{
			name:       "owner_handle unresolvable",
			cfg:        Config{OwnerHandle: "nonexistent"},
			resolver:   setupResolver(t, "owner", validFpr),
			wantErrSub: "resolve owner handle",
		},
		{
			name:       "owner_handle resolves but has no gpg_key_id",
			cfg:        Config{OwnerHandle: "owner"},
			resolver:   setupResolver(t, "owner", ""),
			wantErrSub: "has no gpg_key_id",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			keyID, err := tt.cfg.ResolveOwnerKeyID(tt.resolver)
			if tt.wantErrSub != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErrSub)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantKeyID, keyID)
		})
	}
}

func TestLoadConfig(t *testing.T) {
	t.Run("valid config", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "daemon.json")
		require.NoError(t, os.WriteFile(path, []byte(`{"owner_handle": "claude"}`), 0o640))

		cfg, err := LoadConfig(path)
		require.NoError(t, err)
		assert.Equal(t, "claude", cfg.OwnerHandle)
		assert.Equal(t, "", cfg.OwnerGPGKeyID)
	})

	t.Run("absent file", func(t *testing.T) {
		_, err := LoadConfig(filepath.Join(t.TempDir(), "daemon.json"))
		require.Error(t, err)
		assert.True(t, errors.Is(err, fs.ErrNotExist))
	})

	t.Run("corrupt JSON", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "daemon.json")
		require.NoError(t, os.WriteFile(path, []byte("{not json"), 0o640))

		_, err := LoadConfig(path)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "parse config")
	})
}
