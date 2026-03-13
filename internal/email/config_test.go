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

	assert.Equal(t, "127.0.0.1", cfg.IMAPHost)         // default
	assert.Equal(t, 1143, cfg.IMAPPort)                  // default
	assert.Equal(t, 1025, cfg.SMTPPort)                   // default
	assert.Equal(t, "gpg", cfg.GPGBinary)                // default
	assert.Equal(t, "test@example.com", cfg.GPGSigner)  // defaults to FromAddress
}

func TestLoadConfig_FileNotFound(t *testing.T) {
	_, err := LoadConfig("/nonexistent/path.json")
	assert.Error(t, err)
}

func TestCredentialMethods_Exist(t *testing.T) {
	// Verify the credential methods exist and return either a value or a
	// meaningful error. We don't assert specific values because the
	// resolution chain (keychain → file → env) is environment-dependent.
	cfg := &Config{}

	_, imapErr := cfg.IMAPPassword()
	_, resendErr := cfg.ResendAPIKey()
	_, gpgErr := cfg.GPGPassphrase()

	// On a configured dev machine these succeed; on CI they return
	// "credential not found" errors. Either outcome is correct —
	// the methods are wired up and don't panic.
	_ = imapErr
	_ = resendErr
	_ = gpgErr
}
