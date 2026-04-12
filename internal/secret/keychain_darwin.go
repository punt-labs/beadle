package secret

import (
	"bytes"
	"os/exec"
	"strings"
)

const service = "beadle"

// keychainAvailable checks if macOS Keychain is usable.
func keychainAvailable() bool {
	_, err := exec.LookPath("security")
	return err == nil
}

// keychainBackendNames returns the human-readable labels for the
// macOS keychain backends that are present on the host. The Darwin
// build has exactly one: the system Keychain, accessed via the
// `security` CLI.
func keychainBackendNames() []string {
	if keychainAvailable() {
		return []string{"macOS Keychain"}
	}
	return nil
}

// keychainGet reads a credential from macOS Keychain.
func keychainGet(name string) (string, error) {
	cmd := exec.Command("security", "find-generic-password",
		"-s", service,
		"-a", name,
		"-w", // output password only
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", err
	}
	return strings.TrimSpace(stdout.String()), nil
}
