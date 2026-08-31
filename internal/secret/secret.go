// Package secret resolves credentials from OS credential stores or files.
//
// Resolution priority:
//  1. OS credential store
//     - macOS: Keychain via `security` CLI
//     - Linux: `pass` (primary) then `secret-tool` (fallback), both as
//     subprocesses. See keychain_linux.go for the rationale.
//  2. Secret file (~/.punt-labs/beadle/secrets/<name>, mode 600)
//  3. Environment variable (BEADLE_<NAME>)
//
// Each platform file (keychain_darwin.go, keychain_linux.go) provides
// keychainAvailable, keychainGet, and keychainBackendNames so the
// resolution logic in this file stays platform-agnostic.
package secret

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/punt-labs/beadle/internal/paths"
)

// ErrNotFound is returned when a credential is absent from all backends.
var ErrNotFound = errors.New("credential not found")

// CredGPGPassphrase is the secret.Get name for a GPG signing key's
// passphrase. Named once here so every caller that resolves a signing
// passphrase -- internal/email's SMTP/PGP signing path and
// `beadle-daemon sign` alike -- uses the identical credential name
// instead of each hand-maintaining its own copy of the string.
const CredGPGPassphrase = "gpg-passphrase"

// Get resolves a named credential through the priority chain.
// Name must not contain path separators to prevent path traversal.
func Get(name string) (string, error) {
	if strings.ContainsAny(name, "/\\") {
		return "", fmt.Errorf("credential name %q contains path separator", name)
	}
	// 1. OS keychain
	keychainVal, keychainErr := keychainGet(name)
	if keychainErr == nil && keychainVal != "" {
		return keychainVal, nil
	}

	// 2. Secret file — propagate errors other than "not present"
	val, fileErr := fileGet(name)
	if fileErr == nil && val != "" {
		return val, nil
	}
	if fileErr != nil && !errors.Is(fileErr, os.ErrNotExist) {
		// File exists but is inaccessible (permissions, I/O, etc.) — do not fall through.
		return "", fmt.Errorf("read credential %q: %w", name, fileErr)
	}

	// 3. Environment variable
	envKey := "BEADLE_" + strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
	if val := os.Getenv(envKey); val != "" {
		return val, nil
	}

	// keychainErr is folded in here, not checked earlier, because every
	// backend before this point is a normal "try the next one" outcome --
	// only once all three have failed does the operator need to know WHY
	// the keychain step in particular came back empty. Left out, the
	// remedy this error names ("set it via the credential chain") can be
	// actively wrong: a keychain backend that is genuinely locked (e.g.
	// `pass` with gpg-agent not unlocked) reports ErrNotFound here even
	// though the credential IS correctly stored, sending the operator to
	// re-insert a secret that was never missing.
	if keychainErr != nil {
		return "", fmt.Errorf("credential %q not found (checked: keychain: %w; file; env %s): %w",
			name, keychainErr, envKey, ErrNotFound)
	}
	return "", fmt.Errorf("credential %q not found (checked: keychain, file, env %s): %w",
		name, envKey, ErrNotFound)
}

// Available reports which credential backends are available on this
// host, in resolution order. Each platform file contributes its own
// keychainBackendNames; the file and environment backends are always
// present.
func Available() []string {
	backends := keychainBackendNames()
	backends = append(backends, "file (~/.punt-labs/beadle/secrets/)")
	backends = append(backends, "environment variable")
	return backends
}

// secretsDir returns ~/.punt-labs/beadle/secrets/, creating it with 700 perms if needed.
func secretsDir() (string, error) {
	root, err := paths.DataDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(root, "secrets")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create secrets dir: %w", err)
	}
	return dir, nil
}

// fileGet reads a credential from ~/.punt-labs/beadle/secrets/<name>.
// Rejects files that are group/world readable.
func fileGet(name string) (string, error) {
	dir, err := secretsDir()
	if err != nil {
		return "", err
	}

	path := filepath.Join(dir, name)
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if info.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("credential file %s has unsafe permissions %o (must not be group/world readable)", path, info.Mode().Perm())
	}

	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}
