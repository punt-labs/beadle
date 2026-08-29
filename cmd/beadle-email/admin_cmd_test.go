package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/punt-labs/beadle/internal/identity"
	"github.com/punt-labs/beadle/internal/paths"
)

// writeConfigFixture writes a minimal valid email.json config at path,
// creating parent directories as needed.
func writeConfigFixture(t *testing.T, path string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte(`{"imap_host":"127.0.0.1","imap_user":"fixture@test.com"}`), 0o600))
}

// TestResolveIdentityConfig proves doctorCmd and statusCmd share one
// fallback precedence: the identity-scoped config wins only when identity
// resolution succeeded and that config loads; every other case — no
// identity, or an identity config that is missing — falls back to the
// caller's explicit/default path.
func TestResolveIdentityConfig(t *testing.T) {
	tests := []struct {
		name string
		// writeIdentityConfig, when true, writes a valid config at the
		// identity-scoped path before resolveIdentityConfig runs.
		writeIdentityConfig  bool
		idErr                error
		wantIMAPUser         string
		wantUsedIdentityPath bool
	}{
		{
			name:                 "identity config present and valid is used",
			writeIdentityConfig:  true,
			idErr:                nil,
			wantIMAPUser:         "fixture@test.com",
			wantUsedIdentityPath: true,
		},
		{
			name:                 "identity config absent falls back to fallback path",
			writeIdentityConfig:  false,
			idErr:                nil,
			wantIMAPUser:         "fallback@test.com",
			wantUsedIdentityPath: false,
		},
		{
			name:                 "identity resolution failure falls back to fallback path",
			writeIdentityConfig:  true, // present but must not be consulted
			idErr:                errors.New("no identity resolver configured"),
			wantIMAPUser:         "fallback@test.com",
			wantUsedIdentityPath: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)

			id := &identity.Identity{Email: "agent@test.com", Source: "ethos"}
			idConfigPath, err := paths.IdentityConfigPath(id.Email)
			require.NoError(t, err)
			if tt.writeIdentityConfig {
				writeConfigFixture(t, idConfigPath)
			}

			fallbackPath := filepath.Join(home, "fallback-email.json")
			require.NoError(t, os.WriteFile(fallbackPath, []byte(`{"imap_host":"127.0.0.1","imap_user":"fallback@test.com"}`), 0o600))

			cfg, usedPath, err := resolveIdentityConfig(id, tt.idErr, fallbackPath)
			require.NoError(t, err)
			assert.Equal(t, tt.wantIMAPUser, cfg.IMAPUser)
			if tt.wantUsedIdentityPath {
				assert.Equal(t, idConfigPath, usedPath)
			} else {
				assert.Equal(t, fallbackPath, usedPath)
			}
		})
	}
}

// TestResolveIdentityConfig_FallbackLoadErrorPropagates proves that when
// neither the identity config nor the fallback path load, the fallback's
// load error is returned rather than swallowed.
func TestResolveIdentityConfig_FallbackLoadErrorPropagates(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	id := &identity.Identity{Email: "agent@test.com", Source: "ethos"}
	missingFallback := filepath.Join(home, "does-not-exist.json")

	cfg, usedPath, err := resolveIdentityConfig(id, nil, missingFallback)
	require.Error(t, err)
	assert.Nil(t, cfg)
	assert.Equal(t, missingFallback, usedPath)
}
