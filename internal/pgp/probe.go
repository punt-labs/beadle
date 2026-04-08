package pgp

import (
	"io"
	"os"
	"os/exec"
)

// KeyRequiresPassphrase reports whether the given signing key requires a
// passphrase to sign. It probes the key by attempting a dry-run sign with
// an empty passphrase in batch mode (--pinentry-mode=error, which causes
// gpg to fail instead of prompting). If that succeeds, the key has no
// passphrase; if it fails, a passphrase is required.
//
// Callers should confirm the key exists (e.g. via `gpg --list-keys`)
// before calling this — a missing key and a passphrase-protected key both
// produce non-zero exits from this probe, and this function does not
// distinguish them. The beadle doctor runs its signing-key existence
// check immediately before calling this probe, so that disambiguation
// is handled at the call site.
//
// Contract: the returned error is always nil in the current
// implementation. The error return is part of the signature to leave
// room for future enhancements (e.g. parsing stderr to distinguish
// pinentry-needed from other gpg failures) without a breaking API
// change.
func KeyRequiresPassphrase(gpgBinary, signer string) (bool, error) {
	cmd := exec.Command(gpgBinary,
		"--batch",
		"--pinentry-mode=error",
		"--passphrase", "",
		"--dry-run",
		"--sign",
		"--local-user", signer,
		os.DevNull,
	)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return true, nil
	}
	return false, nil
}
