package pgp

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// ExpiryOption configures CheckKeyExpiry.
type ExpiryOption func(*expiryConfig)

type expiryConfig struct {
	homedir string
}

// Homedir directs CheckKeyExpiry to check keys in the given GNUPGHOME
// instead of gpg's default. Used to check a key inside an isolated
// keyring rather than the ambient one.
func Homedir(dir string) ExpiryOption {
	return func(c *expiryConfig) { c.homedir = dir }
}

// CheckKeyExpiry verifies that the given GPG key has an expiration date set.
// Keys without an expiry are rejected because non-expiring signing keys violate
// the beadle security invariant. Any signing-capable subkey present must also
// carry its own expiration date.
//
// gpgBinary is the path to the gpg executable. keyID is a key fingerprint,
// email address, or any identifier gpg accepts for --list-keys. With no
// options, CheckKeyExpiry checks gpg's default GNUPGHOME; pass Homedir to
// check a specific keyring instead.
func CheckKeyExpiry(gpgBinary, keyID string, opts ...ExpiryOption) error {
	var cfg expiryConfig
	for _, o := range opts {
		o(&cfg)
	}

	args := []string{"--batch", "--no-tty"}
	if cfg.homedir != "" {
		args = append(args, "--homedir", cfg.homedir)
	}
	args = append(args, "--list-keys", "--with-colons", "--", keyID)

	cmd := exec.Command(gpgBinary, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("gpg list-keys %q: %w: %s", keyID, err, stderr.String())
	}

	return parseColonExpiry(stdout.String(), keyID)
}

// parseColonExpiry inspects gpg --with-colons output for pub and sub
// records. It requires the pub record's expiry field (column 6, 0-indexed)
// to be non-empty and non-zero, and applies the same requirement to any sub
// record whose capabilities field (column 11, 0-indexed) marks it signing-
// capable ("s") — a signing subkey with no expiry is exactly as dangerous
// as a non-expiring primary key. Returns an error if any such key or subkey
// has no expiry, if no pub record is found, or if more than one pub record
// matches (ambiguous keyID).
func parseColonExpiry(output, keyID string) error {
	pubCount := 0

	for _, line := range strings.Split(output, "\n") {
		fields := strings.Split(line, ":")
		if len(fields) == 0 {
			continue
		}

		switch fields[0] {
		case "pub":
			if len(fields) < 7 {
				continue
			}
			pubCount++
			if fields[6] == "" || fields[6] == "0" {
				return fmt.Errorf("key %q has no expiration date: non-expiring signing keys are not permitted", keyID)
			}
		case "sub":
			if len(fields) < 12 || !strings.Contains(fields[11], "s") {
				continue
			}
			if fields[6] == "" || fields[6] == "0" {
				return fmt.Errorf("key %q has a signing subkey with no expiration date: non-expiring signing keys are not permitted", keyID)
			}
		}
	}

	if pubCount == 0 {
		return fmt.Errorf("key %q not found in gpg output", keyID)
	}
	if pubCount > 1 {
		return fmt.Errorf("key %q is ambiguous: matched %d public keys; use a unique key identifier (fingerprint)", keyID, pubCount)
	}

	return nil
}
