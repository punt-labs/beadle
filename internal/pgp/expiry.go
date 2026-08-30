package pgp

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// ErrKeyExpiryFinding wraps every error CheckKeyExpiry returns after gpg has
// run to completion and produced output worth parsing -- a key with no
// expiry, an already-expired key, a signing subkey missing its own expiry,
// an ambiguous or missing keyID. It is never wrapped around a failure to run
// gpg at all; that case is returned unwrapped, straight from
// (*exec.Cmd).Run(). Callers use errors.Is against this sentinel to tell a
// genuine expiry finding apart from an operational failure in the
// subprocess itself, without needing to enumerate what a start failure can
// look like.
var ErrKeyExpiryFinding = errors.New("key expiry finding")

// RanToCompletion reports whether err represents an exec.Cmd that ran to
// completion, as opposed to one that never started. A nil error or an
// *exec.ExitError both mean the process ran and exited, successfully or
// not; every other error shape -- *exec.Error for a $PATH lookup failure,
// *fs.PathError for a binary that exists but lacks execute permission, or
// whatever else a platform or a future Go release adds for a start
// failure -- means it never got that far. This inverts the question
// deliberately: enumerating every start-failure shape would need updating
// each time the platform or toolchain adds one, while nil-or-*exec.ExitError
// is a closed, two-member set that never changes.
//
// Callers across internal/pgp and internal/daemon share this single
// definition so the classification cannot drift between them the way it
// did before this function existed -- see internal/daemon/signature.go's
// isOperationalExecFailure, which wraps this with its own domain-specific
// exception on top.
func RanToCompletion(err error) bool {
	if err == nil {
		return true
	}
	var exitErr *exec.ExitError
	return errors.As(err, &exitErr)
}

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

	// gpg --list-keys exits non-zero when keyID legitimately isn't in the
	// keyring -- an expected domain outcome that parseColonExpiry's
	// pubCount == 0 branch already handles. Only a process that never ran
	// to completion at all (binary missing, or present but not executable)
	// is an operational failure.
	if err := cmd.Run(); !RanToCompletion(err) {
		return fmt.Errorf("gpg list-keys %q: %w: %s", keyID, err, stderr.String())
	}

	if err := parseColonExpiry(stdout.String(), keyID); err != nil {
		return fmt.Errorf("%w: %w", ErrKeyExpiryFinding, err)
	}
	return nil
}

// parseColonExpiry inspects gpg --with-colons output for pub and sub
// records. It requires the pub record's expiry field (column 6, 0-indexed)
// to be non-empty, non-zero, and not already in the past, and applies the
// same requirement to any sub record whose capabilities field (column 11,
// 0-indexed) marks it signing-capable ("s") — a signing subkey with no
// expiry, or one that has already expired, is exactly as dangerous as a
// non-expiring or expired primary key. Returns an error if any such key or
// subkey has no expiry or has already expired, if no pub record is found,
// or if more than one pub record matches (ambiguous keyID).
func parseColonExpiry(output, keyID string) error {
	pubCount := 0
	now := time.Now().Unix()

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
			if err := checkNotExpired(fields[6], now); err != nil {
				return fmt.Errorf("key %q %w", keyID, err)
			}
		case "sub":
			if len(fields) < 12 || !strings.Contains(fields[11], "s") {
				continue
			}
			if fields[6] == "" || fields[6] == "0" {
				return fmt.Errorf("key %q has a signing subkey with no expiration date: non-expiring signing keys are not permitted", keyID)
			}
			if err := checkNotExpired(fields[6], now); err != nil {
				return fmt.Errorf("key %q signing subkey %w", keyID, err)
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

// checkNotExpired parses a gpg --with-colons expiry field — a Unix
// timestamp string — and returns an error if it names a time at or before
// now. The caller has already ruled out the "no expiry set" case (an empty
// field or "0"): every field reaching this function is expected to hold a
// real timestamp.
func checkNotExpired(field string, now int64) error {
	exp, err := strconv.ParseInt(field, 10, 64)
	if err != nil {
		return fmt.Errorf("has an unparseable expiry timestamp %q: %w", field, err)
	}
	if exp <= now {
		return fmt.Errorf("has expired: expiration was %s", time.Unix(exp, 0).UTC().Format(time.RFC3339))
	}
	return nil
}
