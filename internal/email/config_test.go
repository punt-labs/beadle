package email

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/punt-labs/beadle/internal/identity"
	"github.com/punt-labs/beadle/internal/paths"
)

func TestLoadConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "email.json")

	err := os.WriteFile(cfgPath, []byte(`{
		"imap_host": "localhost",
		"imap_port": 1143,
		"imap_user": "test@example.com",
		"from_address": "test@example.com"
	}`), 0o644)
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
	}`), 0o644)
	require.NoError(t, err)

	cfg, err := LoadConfig(cfgPath)
	require.NoError(t, err)

	assert.Equal(t, "127.0.0.1", cfg.IMAPHost) // default
	assert.Equal(t, 1143, cfg.IMAPPort)        // default
	assert.Equal(t, 1025, cfg.SMTPPort)        // default
	assert.Equal(t, "gpg", cfg.GPGBinary)      // default
	assert.Equal(t, "", cfg.GPGSigner)         // empty = signing disabled
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
		{"1m", true, 1},
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
	assert.True(t, ValidPollInterval("1m"))
	assert.True(t, ValidPollInterval("5m"))
	assert.True(t, ValidPollInterval("2h"))
	assert.False(t, ValidPollInterval("3m"))
	assert.False(t, ValidPollInterval("bogus"))
}

func TestSaveConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "email.json")

	// Write a minimal config with only imap_user.
	require.NoError(t, os.WriteFile(cfgPath, []byte(`{"imap_user":"test@example.com"}`+"\n"), 0o644))

	cfg := &Config{PollInterval: "10m"}
	require.NoError(t, SaveConfig(cfgPath, cfg))

	loaded, err := LoadConfig(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, "10m", loaded.PollInterval)
	assert.Equal(t, "test@example.com", loaded.IMAPUser)

	// Verify SaveConfig did not bake in LoadConfig defaults.
	raw, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	_, hasHost := m["imap_host"]
	assert.False(t, hasHost, "SaveConfig must not inject imap_host default")
	_, hasPort := m["imap_port"]
	assert.False(t, hasPort, "SaveConfig must not inject imap_port default")

	// Save with empty poll interval removes the field.
	cfg.PollInterval = ""
	require.NoError(t, SaveConfig(cfgPath, cfg))

	loaded, err = LoadConfig(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, "", loaded.PollInterval)
}

func TestSaveConfig_CorruptExistingFile(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "email.json")

	// Write corrupt JSON.
	require.NoError(t, os.WriteFile(cfgPath, []byte(`{not json`), 0o644))

	cfg := &Config{PollInterval: "10m"}
	err := SaveConfig(cfgPath, cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "corrupt")
}

func TestSaveConfig_NormalizesNToDeletion(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "email.json")

	// Seed a minimal config.
	require.NoError(t, os.WriteFile(cfgPath, []byte(`{"imap_user":"test@example.com"}`+"\n"), 0o644))

	cfg := &Config{PollInterval: "10m"}
	require.NoError(t, SaveConfig(cfgPath, cfg))
	loaded, err := LoadConfig(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, "10m", loaded.PollInterval)

	// Save with "n" should remove poll_interval.
	cfg.PollInterval = "n"
	require.NoError(t, SaveConfig(cfgPath, cfg))
	loaded, err = LoadConfig(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, "", loaded.PollInterval)
}

func TestSaveConfig_NewFile(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "email.json")

	// No existing file — SaveConfig should create it with only poll_interval.
	cfg := &Config{PollInterval: "30m"}
	require.NoError(t, SaveConfig(cfgPath, cfg))

	raw, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	assert.Equal(t, "30m", m["poll_interval"])
	assert.Len(t, m, 1, "new file should contain only the poll_interval field")
}

func TestLoadConfig_SMTPDefaults(t *testing.T) {
	tests := []struct {
		name         string
		json         string
		wantSMTPHost string
		wantSMTPUser string
	}{
		{
			name:         "defaults to IMAP values when smtp fields absent",
			json:         `{"imap_host":"bridge.example.com","imap_user":"user@example.com","from_address":"user@example.com"}`,
			wantSMTPHost: "bridge.example.com",
			wantSMTPUser: "user@example.com",
		},
		{
			name:         "explicit smtp_host overrides imap_host default",
			json:         `{"imap_host":"imap.example.com","smtp_host":"smtp.example.com","imap_user":"user@example.com","from_address":"user@example.com"}`,
			wantSMTPHost: "smtp.example.com",
			wantSMTPUser: "user@example.com",
		},
		{
			name:         "explicit smtp_user overrides imap_user default",
			json:         `{"imap_host":"bridge.example.com","imap_user":"imap@example.com","smtp_user":"smtp@example.com","from_address":"user@example.com"}`,
			wantSMTPHost: "bridge.example.com",
			wantSMTPUser: "smtp@example.com",
		},
		{
			name:         "both smtp fields explicit",
			json:         `{"imap_host":"imap.example.com","imap_user":"imap@example.com","smtp_host":"smtp.example.com","smtp_user":"smtp@example.com","from_address":"user@example.com"}`,
			wantSMTPHost: "smtp.example.com",
			wantSMTPUser: "smtp@example.com",
		},
		{
			name:         "imap_host absent uses 127.0.0.1 default for smtp too",
			json:         `{"imap_user":"user@example.com","from_address":"user@example.com"}`,
			wantSMTPHost: "127.0.0.1",
			wantSMTPUser: "user@example.com",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			cfgPath := filepath.Join(dir, "email.json")
			require.NoError(t, os.WriteFile(cfgPath, []byte(tt.json), 0o644))

			cfg, err := LoadConfig(cfgPath)
			require.NoError(t, err)

			assert.Equal(t, tt.wantSMTPHost, cfg.SMTPHost)
			assert.Equal(t, tt.wantSMTPUser, cfg.SMTPUser)
		})
	}
}

func TestCredentialMethods_Exist(_ *testing.T) {
	// Verify the credential methods exist and return either a value or a
	// meaningful error. We don't assert specific values because the
	// resolution chain (keychain → file → env) is environment-dependent.
	cfg := &Config{}

	_, imapErr := cfg.IMAPPassword()
	_, smtpErr := cfg.SMTPPassword()
	_, resendErr := cfg.ResendAPIKey()
	_, gpgErr := cfg.GPGPassphrase()

	// On a configured dev machine these succeed; on CI they return
	// "credential not found" errors. Either outcome is correct —
	// the methods are wired up and don't panic.
	_ = imapErr
	_ = smtpErr
	_ = resendErr
	_ = gpgErr
}

func TestSMTPPassword_TestPasswordOverride(t *testing.T) {
	cfg := &Config{TestPassword: "test-secret"}
	pw, err := cfg.SMTPPassword()
	require.NoError(t, err)
	assert.Equal(t, "test-secret", pw)
}

func TestSMTPEffectiveHost_FallsBackToIMAPHost(t *testing.T) {
	cfg := &Config{IMAPHost: "bridge.example.com"}
	assert.Equal(t, "bridge.example.com", cfg.SMTPEffectiveHost())
}

func TestSMTPEffectiveHost_UsesExplicitSMTPHost(t *testing.T) {
	cfg := &Config{IMAPHost: "imap.example.com", SMTPHost: "smtp.example.com"}
	assert.Equal(t, "smtp.example.com", cfg.SMTPEffectiveHost())
}

func TestSMTPEffectiveUser_FallsBackToIMAPUser(t *testing.T) {
	cfg := &Config{IMAPUser: "user@example.com"}
	assert.Equal(t, "user@example.com", cfg.SMTPEffectiveUser())
}

func TestSMTPEffectiveUser_UsesExplicitSMTPUser(t *testing.T) {
	cfg := &Config{IMAPUser: "imap@example.com", SMTPUser: "smtp@example.com"}
	assert.Equal(t, "smtp@example.com", cfg.SMTPEffectiveUser())
}

// TestLoadIdentityConfig proves the one shared precedence rule poller.go and
// poll_tools.go both rely on: the identity-scoped config wins only when id
// is non-nil and that config is present and loads cleanly; a nil id (no
// resolved identity) or a missing identity config falls back to
// fallbackPath; a present-but-corrupt identity config is a hard failure,
// never a silent fallback.
func TestLoadIdentityConfig(t *testing.T) {
	t.Run("nil identity uses fallback directly", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)

		fallbackPath := filepath.Join(home, "fallback-email.json")
		require.NoError(t, os.WriteFile(fallbackPath, []byte(`{"imap_user":"fallback@test.com"}`), 0o600))

		cfg, usedPath, err := LoadIdentityConfig(nil, fallbackPath)
		require.NoError(t, err)
		assert.Equal(t, "fallback@test.com", cfg.IMAPUser)
		assert.Equal(t, fallbackPath, usedPath)
	})

	t.Run("empty-email identity uses fallback directly, same as nil", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)

		fallbackPath := filepath.Join(home, "fallback-email.json")
		require.NoError(t, os.WriteFile(fallbackPath, []byte(`{"imap_user":"fallback@test.com"}`), 0o600))

		cfg, usedPath, err := LoadIdentityConfig(&identity.Identity{}, fallbackPath)
		require.NoError(t, err)
		assert.Equal(t, "fallback@test.com", cfg.IMAPUser)
		assert.Equal(t, fallbackPath, usedPath)
	})

	t.Run("identity config present and valid wins over fallback", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)

		id := &identity.Identity{Email: "agent@test.com"}
		idConfigPath, err := paths.IdentityConfigPath(id.Email)
		require.NoError(t, err)
		require.NoError(t, os.MkdirAll(filepath.Dir(idConfigPath), 0o700))
		require.NoError(t, os.WriteFile(idConfigPath, []byte(`{"imap_user":"identity@test.com"}`), 0o600))

		fallbackPath := filepath.Join(home, "fallback-email.json")
		require.NoError(t, os.WriteFile(fallbackPath, []byte(`{"imap_user":"fallback@test.com"}`), 0o600))

		cfg, usedPath, err := LoadIdentityConfig(id, fallbackPath)
		require.NoError(t, err)
		assert.Equal(t, "identity@test.com", cfg.IMAPUser)
		assert.Equal(t, idConfigPath, usedPath)
	})

	t.Run("identity config absent falls back", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)

		id := &identity.Identity{Email: "agent@test.com"}
		fallbackPath := filepath.Join(home, "fallback-email.json")
		require.NoError(t, os.WriteFile(fallbackPath, []byte(`{"imap_user":"fallback@test.com"}`), 0o600))

		cfg, usedPath, err := LoadIdentityConfig(id, fallbackPath)
		require.NoError(t, err)
		assert.Equal(t, "fallback@test.com", cfg.IMAPUser)
		assert.Equal(t, fallbackPath, usedPath)
	})

	t.Run("corrupt identity config fails closed, never falls back", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)

		id := &identity.Identity{Email: "agent@test.com"}
		idConfigPath, err := paths.IdentityConfigPath(id.Email)
		require.NoError(t, err)
		require.NoError(t, os.MkdirAll(filepath.Dir(idConfigPath), 0o700))
		require.NoError(t, os.WriteFile(idConfigPath, []byte(`{not json`), 0o600))

		fallbackPath := filepath.Join(home, "fallback-email.json")
		require.NoError(t, os.WriteFile(fallbackPath, []byte(`{"imap_user":"fallback@test.com"}`), 0o600))

		cfg, usedPath, err := LoadIdentityConfig(id, fallbackPath)
		require.Error(t, err, "a corrupt identity config must fail closed, not silently prefer fallbackPath")
		assert.Nil(t, cfg)
		assert.Equal(t, idConfigPath, usedPath, "usedPath names the path actually attempted, even on error")
		assert.Equal(t, 1, strings.Count(err.Error(), idConfigPath),
			"the path must be named once, not repeated by both LoadIdentityConfig and LoadConfig's own wrap")
	})

	t.Run("identity config path failure is wrapped for diagnosis", func(t *testing.T) {
		t.Setenv("HOME", "")

		id := &identity.Identity{Email: "agent@test.com"}
		_, _, err := LoadIdentityConfig(id, "/does-not-matter.json")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "identity config path",
			"a paths.IdentityConfigPath failure must be distinguishable from a load failure")
	})

	t.Run("fallback load error propagates when identity config is also absent", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)

		id := &identity.Identity{Email: "agent@test.com"}
		missingFallback := filepath.Join(home, "does-not-exist.json")

		cfg, usedPath, err := LoadIdentityConfig(id, missingFallback)
		require.Error(t, err)
		assert.Nil(t, cfg)
		assert.Equal(t, missingFallback, usedPath)
	})
}

func TestLoadConfig_TLSSkipVerify(t *testing.T) {
	tests := []struct {
		name string
		json string
		want bool
	}{
		{
			name: "absent defaults to false",
			json: `{"imap_user":"u@example.com"}`,
			want: false,
		},
		{
			name: "explicit true",
			json: `{"imap_user":"u@example.com","tls_skip_verify":true}`,
			want: true,
		},
		{
			name: "explicit false",
			json: `{"imap_user":"u@example.com","tls_skip_verify":false}`,
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			cfgPath := filepath.Join(dir, "email.json")
			require.NoError(t, os.WriteFile(cfgPath, []byte(tt.json), 0o644))

			cfg, err := LoadConfig(cfgPath)
			require.NoError(t, err)
			assert.Equal(t, tt.want, cfg.TLSSkipVerify)
		})
	}
}
