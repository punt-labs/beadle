package secret

// Linux keychain backend.
//
// Two subprocess wrappers are supported, tried in priority order:
//
//  1. pass (https://www.passwordstore.org/) — entries live under
//     beadle/<name> and are encrypted with the user's GPG key. This
//     matches the Proton Bridge vault backend and the Punt Labs
//     convention for sensitive material, and it shares the same trust
//     anchor as the daemon's PGP signing key.
//  2. secret-tool (libsecret / XDG Secret Service API) — keyed by
//     (service=beadle, account=<name>), matching the Darwin convention
//     in keychain_darwin.go.
//
// If both are installed, pass wins. The resolution chain in
// secret.Get() still falls through to the file backend and environment
// variables after the keychain layer, so a Linux user with neither
// binary installed is no worse off than before.

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

const service = "beadle"

// passRunner and secretToolRunner are seams for unit tests. They are
// swapped by keychain_linux_test.go to exercise the priority ordering
// and fallback logic without invoking real subprocesses. Production
// code uses realPassGet and realSecretToolGet.
var (
	passRunner       = realPassGet
	secretToolRunner = realSecretToolGet
)

// keychainAvailable reports whether any Linux keychain backend is
// present on the host.
func keychainAvailable() bool {
	if _, err := exec.LookPath("pass"); err == nil {
		return true
	}
	if _, err := exec.LookPath("secret-tool"); err == nil {
		return true
	}
	return false
}

// keychainBackendNames returns the human-readable labels for the
// Linux keychain backends that are present on the host, in priority
// order. Used by Available() to report what resolution paths the
// current process will actually use.
func keychainBackendNames() []string {
	var names []string
	if _, err := exec.LookPath("pass"); err == nil {
		names = append(names, "pass")
	}
	if _, err := exec.LookPath("secret-tool"); err == nil {
		names = append(names, "secret-tool")
	}
	return names
}

// keychainGet reads a credential from the Linux keyring. It tries the
// configured runners in order, returning the first non-empty value.
// An error is returned only when no runner produced a value; the
// caller treats any error as "not in keychain, try the next backend
// in secret.Get()'s resolution chain."
func keychainGet(name string) (string, error) {
	var lastErr error

	val, err := passRunner(name)
	if err == nil && val != "" {
		return val, nil
	}
	if err != nil {
		lastErr = err
	}

	val, err = secretToolRunner(name)
	if err == nil && val != "" {
		return val, nil
	}
	if err != nil {
		lastErr = err
	}

	if lastErr == nil {
		return "", fmt.Errorf("no Linux keychain backend available (install pass or secret-tool)")
	}
	return "", lastErr
}

// realPassGet runs `pass show beadle/<name>` and returns the trimmed
// secret. Beadle credentials are single-line, so pass's multi-line
// metadata format (password on line 1, metadata on subsequent lines)
// degenerates to the expected shape. Trimming strips the trailing
// newline pass always appends.
//
// Errors are surfaced verbatim so the caller can distinguish "pass
// not installed" from "entry not in store" from "gpg-agent locked" —
// but the secret.Get() resolution chain treats any error as "try next
// backend," so the distinction is informational today.
func realPassGet(name string) (string, error) {
	if _, err := exec.LookPath("pass"); err != nil {
		return "", fmt.Errorf("pass not installed: %w", err)
	}
	cmd := exec.Command("pass", "show", service+"/"+name)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("pass show %s/%s: %w", service, name, err)
	}
	return strings.TrimSpace(stdout.String()), nil
}

// realSecretToolGet runs `secret-tool lookup service beadle account
// <name>` and returns the trimmed secret. The attribute pairing
// matches the Darwin convention (service + account) so users coming
// from macOS do not need to relearn the namespace.
func realSecretToolGet(name string) (string, error) {
	if _, err := exec.LookPath("secret-tool"); err != nil {
		return "", fmt.Errorf("secret-tool not installed: %w", err)
	}
	cmd := exec.Command("secret-tool", "lookup",
		"service", service,
		"account", name)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("secret-tool lookup service=%s account=%s: %w", service, name, err)
	}
	return strings.TrimSpace(stdout.String()), nil
}
