package identity

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// EnsureIdentityDir creates the identity-scoped directory
// ~/.punt-labs/beadle/identities/<email>/ if it doesn't exist.
// Copies root email.json and contacts.json into the identity dir
// if they exist at the root and don't yet exist in the identity dir.
// Idempotent — safe to call on every startup.
func EnsureIdentityDir(beadleDir, email string) (string, error) {
	if email == "" {
		return "", fmt.Errorf("email is required for identity directory")
	}

	idDir := filepath.Join(beadleDir, "identities", email)
	if err := os.MkdirAll(idDir, 0o750); err != nil {
		return "", fmt.Errorf("create identity dir %s: %w", idDir, err)
	}

	// Migrate root files into identity dir (copy, don't move)
	filesToMigrate := []string{"email.json", "contacts.json"}
	for _, name := range filesToMigrate {
		src := filepath.Join(beadleDir, name)
		dst := filepath.Join(idDir, name)
		if err := copyFileIfNeeded(src, dst); err != nil {
			return "", fmt.Errorf("migrate %s: %w", name, err)
		}
	}

	return idDir, nil
}

// copyFileIfNeeded copies src to dst if src exists and dst does not.
func copyFileIfNeeded(src, dst string) error {
	// Skip if destination already exists
	if _, err := os.Stat(dst); err == nil {
		return nil
	}

	// Skip if source doesn't exist
	srcFile, err := os.Open(src)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	defer srcFile.Close()

	info, err := srcFile.Stat()
	if err != nil {
		return err
	}

	dstFile, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_EXCL, info.Mode())
	if err != nil {
		return err
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, srcFile)
	return err
}
