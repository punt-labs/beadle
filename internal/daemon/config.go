package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/punt-labs/beadle/internal/identity"
)

// Config holds daemon-instance configuration — settings that describe the
// daemon process itself, not any one email identity it operates as.
//
// OwnerHandle and OwnerGPGKeyID are mutually exclusive: at most one may be
// set. A freshly-parsed Config (straight out of LoadConfig) is not yet
// validated — both fields empty, both fields set, an OwnerHandle that
// cannot be resolved, and a malformed OwnerGPGKeyID are all possible zero-
// or ill-formed states at this point. ResolveOwnerKeyID is what turns a
// Config into a single validated fingerprint or an error naming exactly
// why none is usable; nothing about Config's fields should be trusted
// before that call runs.
type Config struct {
	OwnerHandle   string `json:"owner_handle,omitempty"`
	OwnerGPGKeyID string `json:"owner_gpg_key_id,omitempty"`
}

// LoadConfig reads daemon configuration from path.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	return &cfg, nil
}

// ResolveOwnerKeyID turns c's owner-key config into a validated fingerprint,
// or an error naming exactly why none is usable. It is exported because
// cmd/beadle-daemon/main.go (package main) calls it directly on the *Config
// LoadConfig returns; the fingerprint check inside it reuses signature.go's
// unexported fingerprintPattern, which only internal/daemon code can reach
// — main.go must never need to touch that var itself.
func (c *Config) ResolveOwnerKeyID(resolver *identity.Resolver) (string, error) {
	var keyID string
	switch {
	case c.OwnerHandle != "" && c.OwnerGPGKeyID != "":
		return "", fmt.Errorf("daemon.json: owner_handle and owner_gpg_key_id are both set — ambiguous, set exactly one")
	case c.OwnerHandle != "":
		id, err := resolver.ResolveHandle(c.OwnerHandle)
		if err != nil {
			return "", fmt.Errorf("resolve owner handle %q: %w", c.OwnerHandle, err)
		}
		if id.GPGKeyID == "" {
			return "", fmt.Errorf("owner identity %q has no gpg_key_id in its beadle extension", c.OwnerHandle)
		}
		keyID = id.GPGKeyID
	case c.OwnerGPGKeyID != "":
		keyID = c.OwnerGPGKeyID
	default:
		return "", fmt.Errorf("daemon.json: set owner_handle or owner_gpg_key_id — no default owner")
	}

	// Identical validation for both paths: whichever branch resolved a
	// value, it must pass the same full-fingerprint pattern VerifySignature
	// itself enforces (signature.go:46) before ResolveOwnerKeyID accepts
	// it — a malformed, short, or email-form identifier fails here, at
	// config load, with one clear error naming the misconfigured field,
	// never threaded through for VerifySignature to reject once per file.
	if !fingerprintPattern.MatchString(keyID) {
		return "", fmt.Errorf("daemon owner key %q is not a full 40-hex OpenPGP fingerprint", keyID)
	}
	return keyID, nil
}
