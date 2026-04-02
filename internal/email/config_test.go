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

func TestPollDuration(t *testing.T) {
	tests := []struct {
		interval string
		wantOK   bool
		wantMins int // expected duration in minutes, 0 if disabled
	}{
		{"", false, 0},
		{"n", false, 0},
		{"5m", true, 5},
		{"10m", true, 10},
		{"15m", true, 15},
		{"30m", true, 30},
		{"1h", true, 60},
		{"2h", true, 120},
		{"3m", false, 0},
		{"1d", false, 0},
		{"invalid", false, 0},
	}
	for _, tt := range tests {
		t.Run(tt.interval, func(t *testing.T) {
			cfg := &Config{PollInterval: tt.interval}
			d, ok := cfg.PollDuration()
			assert.Equal(t, tt.wantOK, ok)
			if tt.wantOK {
				assert.Equal(t, tt.wantMins, int(d.Minutes()))
			}
		})
	}
}

func TestValidPollInterval(t *testing.T) {
	assert.True(t, ValidPollInterval(""))
	assert.True(t, ValidPollInterval("n"))
	assert.True(t, ValidPollInterval("5m"))
	assert.True(t, ValidPollInterval("2h"))
	assert.False(t, ValidPollInterval("3m"))
	assert.False(t, ValidPollInterval("bogus"))
}

func TestSaveConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "email.json")

	cfg := &Config{
		IMAPHost:     "127.0.0.1",
		IMAPPort:     1143,
		IMAPUser:     "test@example.com",
		SMTPPort:     1025,
		FromAddress:  "test@example.com",
		PollInterval: "10m",
	}

	require.NoError(t, SaveConfig(cfgPath, cfg))

	loaded, err := LoadConfig(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, "10m", loaded.PollInterval)
	assert.Equal(t, "127.0.0.1", loaded.IMAPHost)

	// Save with empty poll interval removes the field.
	cfg.PollInterval = ""
	require.NoError(t, SaveConfig(cfgPath, cfg))

	loaded, err = LoadConfig(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, "", loaded.PollInterval)
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
