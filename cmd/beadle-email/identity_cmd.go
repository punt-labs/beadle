package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/punt-labs/beadle/internal/contacts"
	"github.com/punt-labs/beadle/internal/identity"
	"github.com/punt-labs/beadle/internal/paths"
)

// --- identity (parent) ---

var identityCmd = &cobra.Command{
	Use:   "identity",
	Short: "Show or set active identity",
	Long:  "Show the resolved identity for the current repo, or set the per-repo identity handle.",
	RunE:  identityShowRun, // default: show
}

func init() {
	identityCmd.AddCommand(identityShowCmd)
	identityCmd.AddCommand(identitySetCmd)
}

// --- identity show ---

var identityShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show active identity",
	RunE:  identityShowRun,
}

func identityShowRun(cmd *cobra.Command, args []string) error {
	resolver, err := newResolver()
	if err != nil {
		return fmt.Errorf("create resolver: %w", err)
	}
	id, err := resolver.Resolve()
	if err != nil {
		return fmt.Errorf("resolve identity: %w", err)
	}

	contactsPath := contactsPathForEmail(id.Email)
	store := contacts.NewStore(contactsPath)
	contactCount := 0
	contactsError := ""
	if loadErr := store.Load(); loadErr != nil {
		if !errors.Is(loadErr, os.ErrNotExist) {
			contactsError = loadErr.Error()
		}
	} else {
		contactCount = store.Count()
	}

	result := map[string]string{
		"email":          id.Email,
		"source":         id.Source,
		"contacts_path":  contactsPath,
		"contacts_count": fmt.Sprintf("%d", contactCount),
	}
	if id.Handle != "" {
		result["handle"] = id.Handle
	}
	if id.Name != "" {
		result["name"] = id.Name
	}
	if id.GPGKeyID != "" {
		result["gpg_key_id"] = id.GPGKeyID
	}
	if contactsError != "" {
		result["contacts_error"] = contactsError
	}

	g.printResult(result, func() {
		fmt.Printf("%-16s %s\n", "email:", id.Email)
		if id.Handle != "" {
			fmt.Printf("%-16s %s\n", "handle:", id.Handle)
		}
		if id.Name != "" {
			fmt.Printf("%-16s %s\n", "name:", id.Name)
		}
		fmt.Printf("%-16s %s\n", "source:", id.Source)
		if id.GPGKeyID != "" {
			fmt.Printf("%-16s %s\n", "gpg_key_id:", id.GPGKeyID)
		}
		if contactsError != "" {
			fmt.Printf("%-16s %s (error: %s)\n", "contacts:", contactsPath, contactsError)
		} else {
			fmt.Printf("%-16s %s (%d contacts)\n", "contacts:", contactsPath, contactCount)
		}
	})
	return nil
}

// --- identity set ---

var identitySetCmd = &cobra.Command{
	Use:   "set <handle>",
	Short: "Set per-repo identity handle",
	Long:  "Write .punt-labs/ethos/config.yaml with the given handle for this repo.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		handle := strings.TrimSpace(args[0])
		if handle == "" {
			return fmt.Errorf("handle cannot be empty")
		}
		if err := identity.ValidateHandle(handle); err != nil {
			return err
		}

		// Detect repo root — don't write to arbitrary CWD
		repoRoot, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
		if err != nil {
			return fmt.Errorf("not in a git repository — run from a repo root to set per-repo identity")
		}
		root := strings.TrimSpace(string(repoRoot))

		// Write per-repo ethos config
		dir := filepath.Join(root, ".punt-labs", "ethos")
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
		configPath := filepath.Join(dir, "config.yaml")
		content := fmt.Sprintf("active: %s\n", handle)
		if err := os.WriteFile(configPath, []byte(content), 0o640); err != nil {
			return fmt.Errorf("write %s: %w", configPath, err)
		}

		// Verify by resolving again
		resolver, err := newResolver()
		if err != nil {
			return fmt.Errorf("create resolver: %w", err)
		}
		id, err := resolver.Resolve()
		if err != nil {
			return fmt.Errorf("set handle %q but resolution failed: %w\n(the handle may not exist in ethos — check ~/.punt-labs/ethos/identities/%s.yaml)", handle, err, handle)
		}

		g.printResult(id, func() {
			fmt.Printf("set identity to %s <%s> (source: %s)\n", handle, id.Email, id.Source)
		})
		return nil
	},
}

// contactsPathForEmail returns the identity-scoped contacts path for a given email.
func contactsPathForEmail(email string) string {
	beadleDir, err := paths.DataDir()
	if err != nil {
		return filepath.Join(paths.MustDataDir(), "contacts.json")
	}
	idDir, err := identity.EnsureIdentityDir(beadleDir, email)
	if err != nil {
		return filepath.Join(paths.MustDataDir(), "contacts.json")
	}
	return filepath.Join(idDir, "contacts.json")
}
