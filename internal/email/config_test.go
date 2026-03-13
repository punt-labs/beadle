package email

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "email.json")

	err := os.WriteFile(cfgPath, []byte(`{
		"imap_host": "localhost",
		"imap_port": 1143,
		"imap_user": "test@example.com",
		"from_address": "test@example.com"
	}`), 0644)
	require.NoError(t, err)

	cfg, err := LoadConfig(cfgPath)
	require.NoError(t, err)

	assert.Equal(t, "localhost", cfg.IMAPHost)
	assert.Equal(t, 1143, cfg.IMAPPort)
	assert.Equal(t, "test@example.com", cfg.IMAPUser)
	assert.Equal(t, "gpg", cfg.GPGBinary) // default
}

func TestLoadConfig_Defaults(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "email.json")

	err := os.WriteFile(cfgPath, []byte(`{
		"imap_user": "test@example.com",
		"from_address": "test@example.com"
	}`), 0644)
	require.NoError(t, err)

	cfg, err := LoadConfig(cfgPath)
	require.NoError(t, err)

	assert.Equal(t, "127.0.0.1", cfg.IMAPHost) // default
	assert.Equal(t, 1143, cfg.IMAPPort)          // default
	assert.Equal(t, "gpg", cfg.GPGBinary)        // default
}

func TestLoadConfig_FileNotFound(t *testing.T) {
	_, err := LoadConfig("/nonexistent/path.json")
	assert.Error(t, err)
}

func TestIMAPPassword_Resolves(t *testing.T) {
	// Credential resolution goes: keychain → file → env var.
	// On a dev machine with keychain configured, this returns the real
	// credential. We just verify it resolves without error.
	cfg := &Config{}
	pw, err := cfg.IMAPPassword()
	require.NoError(t, err)
	assert.NotEmpty(t, pw)
}

func TestResendAPIKey_Resolves(t *testing.T) {
	cfg := &Config{}
	key, err := cfg.ResendAPIKey()
	require.NoError(t, err)
	assert.NotEmpty(t, key)
}
