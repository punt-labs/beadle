package email

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

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
	GPGBinary   string `json:"gpg_binary"`
	GPGSigner   string `json:"gpg_signer"`

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
