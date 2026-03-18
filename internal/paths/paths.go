// Package paths provides the single root directory for all beadle data.
// All packages derive their paths from DataDir to ensure a unified
// filesystem layout under ~/.punt-labs/beadle/.
package paths

import (
	"fmt"
	"os"
	"path/filepath"
)

// DataDir returns ~/.punt-labs/beadle/.
// This is the single root for all beadle runtime data:
// config, secrets, contacts, attachments, logs.
func DataDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".punt-labs", "beadle"), nil
}

// MustDataDir returns DataDir or panics. Use only in contexts where
// a missing home directory is unrecoverable (e.g., DefaultPath helpers).
func MustDataDir() string {
	dir, err := DataDir()
	if err != nil {
		panic(err)
	}
	return dir
}
