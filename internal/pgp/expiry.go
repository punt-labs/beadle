package pgp

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// CheckKeyExpiry verifies that the given GPG key has an expiration date set.
// Keys without an expiry are rejected because non-expiring signing keys violate
// the beadle security invariant.
//
// gpgBinary is the path to the gpg executable. keyID is a key fingerprint,
// email address, or any identifier gpg accepts for --list-keys.
func CheckKeyExpiry(gpgBinary, keyID string) error {
	cmd := exec.Command(gpgBinary,
		"--batch", "--no-tty",
		"--list-keys", "--with-colons",
		"--",
		keyID,
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("gpg list-keys %q: %w: %s", keyID, err, stderr.String())
	}

	return parseColonExpiry(stdout.String(), keyID)
}

// parseColonExpiry inspects gpg --with-colons output for a pub record and
// checks whether the expiry field (column 6, 0-indexed) is non-empty and
// non-zero. Returns an error if the key has no expiry.
func parseColonExpiry(output, keyID string) error {
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Split(line, ":")
		if len(fields) < 7 {
			continue
		}
		recType := fields[0]
		if recType != "pub" {
			continue
		}
		expiry := fields[6]
		if expiry == "" || expiry == "0" {
			return fmt.Errorf("key %q has no expiration date: non-expiring signing keys are not permitted", keyID)
		}
		return nil
	}
	return fmt.Errorf("key %q not found in gpg output", keyID)
}
