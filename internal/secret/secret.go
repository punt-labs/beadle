// Package secret resolves credentials from OS credential stores or files.
//
// Resolution priority:
//  1. OS credential store (macOS Keychain via `security` CLI)
//  2. Secret file (~/.config/beadle/<name>, mode 600)
//  3. Environment variable (BEADLE_<NAME>)
//
// v0.1.1 will add Linux libsecret (`secret-tool`) as a keychain backend.
package secret

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const service = "beadle"

// Get resolves a named credential through the priority chain.
// Name must not contain path separators to prevent path traversal.
func Get(name string) (string, error) {
	if strings.ContainsAny(name, "/\\") {
		return "", fmt.Errorf("credential name %q contains path separator", name)
	}
	// 1. OS keychain
	if val, err := keychainGet(name); err == nil && val != "" {
		return val, nil
	}

	// 2. Secret file
	if val, err := fileGet(name); err == nil && val != "" {
		return val, nil
	}

	// 3. Environment variable
	envKey := "BEADLE_" + strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
	if val := os.Getenv(envKey); val != "" {
		return val, nil
	}

	return "", fmt.Errorf("credential %q not found (checked: keychain, file, env %s)", name, envKey)
}

// Available reports which credential backends are available.
func Available() []string {
	var backends []string
	if keychainAvailable() {
		switch runtime.GOOS {
		case "darwin":
			backends = append(backends, "macOS Keychain")
		case "linux":
			backends = append(backends, "libsecret")
		}
	}
	backends = append(backends, "file (~/.config/beadle/)")
	backends = append(backends, "environment variable")
	return backends
}

// configDir returns ~/.config/beadle/, creating it with 700 perms if needed.
func configDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}

	dir := filepath.Join(home, ".config", "beadle")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("create config dir: %w", err)
	}
	return dir, nil
}

// fileGet reads a credential from ~/.config/beadle/<name>.
// Rejects files that are group/world readable.
func fileGet(name string) (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}

	path := filepath.Join(dir, name)
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if info.Mode().Perm()&0077 != 0 {
		return "", fmt.Errorf("credential file %s has unsafe permissions %o (must be 600)", path, info.Mode().Perm())
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}
