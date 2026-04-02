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
	CredResendAPIKey  = "resend-api-key"
	CredGPGPassphrase = "gpg-passphrase"
)

// Config holds email channel configuration.
// Credentials are not stored here — they are resolved at runtime via the
// secret package (OS keychain → file → env var).
type Config struct {
	IMAPHost    string `json:"imap_host"`
	IMAPPort    int    `json:"imap_port"`
	IMAPUser    string `json:"imap_user"`
	SMTPPort    int    `json:"smtp_port"`
	FromAddress string `json:"from_address"`
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
	if cfg.SMTPPort == 0 {
		cfg.SMTPPort = 1025
	}
	if cfg.GPGBinary == "" {
		cfg.GPGBinary = "gpg"
	}
	if cfg.GPGSigner == "" {
		cfg.GPGSigner = cfg.FromAddress
	}

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

// ResendAPIKey resolves the Resend API key via the secret store.
func (c *Config) ResendAPIKey() (string, error) {
	return secret.Get(CredResendAPIKey)
}

// GPGPassphrase resolves the GPG signing key passphrase via the secret store.
func (c *Config) GPGPassphrase() (string, error) {
	return secret.Get(CredGPGPassphrase)
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

// SaveConfig writes the config back to the given path, preserving any
// unknown fields from the original file.
func SaveConfig(path string, cfg *Config) error {
	// Read existing file to preserve unknown fields.
	existing := make(map[string]any)
	data, readErr := os.ReadFile(path)
	if readErr == nil {
		if err := json.Unmarshal(data, &existing); err != nil {
			return fmt.Errorf("existing config %s is corrupt: %w", path, err)
		}
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return fmt.Errorf("read existing config %s: %w", path, readErr)
	}

	// Overlay known fields.
	existing["imap_host"] = cfg.IMAPHost
	existing["imap_port"] = cfg.IMAPPort
	existing["imap_user"] = cfg.IMAPUser
	existing["smtp_port"] = cfg.SMTPPort
	existing["from_address"] = cfg.FromAddress
	if cfg.GPGBinary != "" {
		existing["gpg_binary"] = cfg.GPGBinary
	}
	if cfg.GPGSigner != "" {
		existing["gpg_signer"] = cfg.GPGSigner
	}
	if cfg.PollInterval != "" && cfg.PollInterval != "n" {
		existing["poll_interval"] = cfg.PollInterval
	} else {
		delete(existing, "poll_interval")
	}

	data, err := json.MarshalIndent(existing, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o640)
}
