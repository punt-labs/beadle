// Package paths provides the single root directory for all beadle data.
// All packages derive their paths from DataDir to ensure a unified
// filesystem layout under ~/.punt-labs/beadle/.
package paths

import (
	"os"
	"path/filepath"
)

// DataDir returns ~/.punt-labs/beadle/.
// This is the single root for all beadle runtime data:
// config, secrets, contacts, attachments, logs.
func DataDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".punt-labs", "beadle")
}
