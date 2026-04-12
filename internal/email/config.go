package email

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/punt-labs/beadle/internal/paths"
	"github.com/punt-labs/beadle/internal/secret"
)

// Credential names used with the secret resolver.
const (
	CredIMAPPassword  = "imap-password"
	CredSMTPPassword  = "smtp-password"
	CredResendAPIKey  = "resend-api-key"
	CredGPGPassphrase = "gpg-passphrase"
)

// Config holds email channel configuration.
// Credentials are not stored here — they are resolved at runtime via the
// secret package (OS keychain → file → env var).
type Config struct {
	IMAPHost     string `json:"imap_host"`
	IMAPPort     int    `json:"imap_port"`
	IMAPUser     string `json:"imap_user"`
	SMTPHost     string `json:"smtp_host"`
	SMTPPort     int    `json:"smtp_port"`
	SMTPUser     string `json:"smtp_user"`
	FromAddress  string `json:"from_address"`
	GPGBinary    string `json:"gpg_binary"`
	GPGSigner    string `json:"gpg_signer"`
	PollInterval string `json:"poll_interval,omitempty"`

	// TestPassword bypasses the secret store for integration tests.
	// Required because macOS Keychain is process-global — setting HOME
	// does not prevent `security` from finding real credentials.
	// Never set in production config files — json:"-" excludes it.
	TestPassword string `json:"-"`
}

// DefaultConfigPath returns ~/.punt-labs/beadle/email.json.
func DefaultConfigPath() string {
	return filepath.Join(paths.MustDataDir(), "email.json")
}

// LoadConfig reads configuration from the given path.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}

	if cfg.IMAPHost == "" {
		cfg.IMAPHost = "127.0.0.1"
	}
	if cfg.IMAPPort == 0 {
		cfg.IMAPPort = 1143
	}
	if cfg.SMTPHost == "" {
		cfg.SMTPHost = cfg.IMAPHost
	}
	if cfg.SMTPPort == 0 {
		cfg.SMTPPort = 1025
	}
	if cfg.SMTPUser == "" {
		cfg.SMTPUser = cfg.IMAPUser
	}
	if cfg.GPGBinary == "" {
		cfg.GPGBinary = "gpg"
	}
	// GPGSigner is intentionally not defaulted. When empty, outbound
	// signing is disabled and doctor skips signing-key checks.

	return &cfg, nil
}

// IMAPPassword resolves the IMAP password. If TestPassword is set
// (for integration tests), it is returned directly — necessary because
// macOS Keychain is process-global and ignores HOME overrides.
func (c *Config) IMAPPassword() (string, error) {
	if c.TestPassword != "" {
		return c.TestPassword, nil
	}
	return secret.Get(CredIMAPPassword)
}

// SMTPPassword resolves the SMTP password. It first tries the "smtp-password"
// credential; if not found, falls back to IMAPPassword. If TestPassword is set
// (for integration tests), it is returned directly.
// Non-ErrNotFound errors (e.g. bad permissions) are returned immediately
// rather than silently falling through to the IMAP password.
func (c *Config) SMTPPassword() (string, error) {
	if c.TestPassword != "" {
		return c.TestPassword, nil
	}
	pw, err := secret.Get(CredSMTPPassword)
	if err == nil {
		return pw, nil
	}
	if errors.Is(err, secret.ErrNotFound) {
		return c.IMAPPassword()
	}
	return "", fmt.Errorf("read smtp-password: %w", err)
}

// ResendAPIKey resolves the Resend API key via the secret store.
func (c *Config) ResendAPIKey() (string, error) {
	return secret.Get(CredResendAPIKey)
}

// GPGPassphrase resolves the GPG signing key passphrase via the secret store.
func (c *Config) GPGPassphrase() (string, error) {
	return secret.Get(CredGPGPassphrase)
}

// SMTPEffectiveHost returns SMTPHost if set, falling back to IMAPHost.
// Callers that build Config directly without LoadConfig should use this
// instead of SMTPHost to ensure the IMAP fallback is honored.
func (c *Config) SMTPEffectiveHost() string {
	if c.SMTPHost != "" {
		return c.SMTPHost
	}
	return c.IMAPHost
}

// SMTPEffectiveUser returns SMTPUser if set, falling back to IMAPUser.
func (c *Config) SMTPEffectiveUser() string {
	if c.SMTPUser != "" {
		return c.SMTPUser
	}
	return c.IMAPUser
}

// validPollIntervals enumerates allowed poll_interval values.
var validPollIntervals = map[string]time.Duration{
	"5m":  5 * time.Minute,
	"10m": 10 * time.Minute,
	"15m": 15 * time.Minute,
	"30m": 30 * time.Minute,
	"1h":  time.Hour,
	"2h":  2 * time.Hour,
}

// PollDuration returns the polling interval and whether polling is enabled.
// Empty string or "n" means disabled.
func (c *Config) PollDuration() (time.Duration, bool) {
	if c.PollInterval == "" || c.PollInterval == "n" {
		return 0, false
	}
	d, ok := validPollIntervals[c.PollInterval]
	return d, ok
}

// ValidPollInterval reports whether s is a valid poll_interval value.
// Valid values: "5m", "10m", "15m", "30m", "1h", "2h", "n", "".
func ValidPollInterval(s string) bool {
	if s == "" || s == "n" {
		return true
	}
	_, ok := validPollIntervals[s]
	return ok
}

// SaveConfig updates only the poll_interval field in the config file at path,
// leaving all other fields untouched. The write is atomic (temp file + rename).
func SaveConfig(path string, cfg *Config) error {
	existing := make(map[string]any)
	data, readErr := os.ReadFile(path)
	if readErr == nil {
		if err := json.Unmarshal(data, &existing); err != nil {
			return fmt.Errorf("existing config %s is corrupt: %w", path, err)
		}
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return fmt.Errorf("read existing config %s: %w", path, readErr)
	}

	// Only update poll_interval — leave all other fields untouched.
	if cfg.PollInterval != "" && cfg.PollInterval != "n" {
		existing["poll_interval"] = cfg.PollInterval
	} else {
		delete(existing, "poll_interval")
	}

	out, err := json.MarshalIndent(existing, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	out = append(out, '\n')

	// Atomic write: temp file + rename.
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, "email.json.tmp-*")
	if err != nil {
		return fmt.Errorf("create temp config: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(out); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp config: %w", err)
	}
	if err := tmp.Chmod(0o640); err != nil {
		tmp.Close()
		return fmt.Errorf("set temp config permissions: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp config: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace config: %w", err)
	}
	return nil
}
