package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/punt-labs/beadle/internal/claudemd"
)

// importLine is the canonical @-import the repo CLAUDE.md carries when beadle
// is enabled. It must be byte-identical across every tool the standard governs.
const importLine = "@.punt-labs/beadle/CLAUDE.md"

// disablePurge, when set, makes disable remove the whole .punt-labs/beadle/
// directory rather than leaving it dormant.
var disablePurge bool

func init() {
	disableCmd.Flags().BoolVar(&disablePurge, "purge", false,
		"Remove the whole .punt-labs/beadle/ directory, not just the enabled marker")
}

var enableCmd = &cobra.Command{
	Use:   "enable",
	Short: "Enable beadle guidance in this repo",
	Long: "Deposit the beadle user guide into .punt-labs/beadle/, mark the repo\n" +
		"enabled, and add the @.punt-labs/beadle/CLAUDE.md import to the repo\n" +
		"CLAUDE.md. Idempotent: re-running is the upgrade path.",
	RunE: func(cmd *cobra.Command, args []string) error {
		root, err := repoRoot()
		if err != nil {
			return err
		}
		return enableRepo(root)
	},
}

// enableRepo deposits the guide into <root>/.punt-labs/beadle/, writes the
// enabled marker, and registers the import in <root>/CLAUDE.md. It is
// idempotent, so re-running upgrades in place. The whole operation holds an
// exclusive per-repo lock so a concurrent enable and disable cannot interleave
// into the §2.11-incorrect "marker without import" state; the nested CLAUDE.md
// lock inside Register is always acquired after this one, never the reverse.
func enableRepo(root string) error {
	return claudemd.WithLock(root, func() error { return enableLocked(root) })
}

func enableLocked(root string) error {
	dir := filepath.Join(root, ".punt-labs", "beadle")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}

	// The guide and marker are beadle-owned: enable overwrites them wholesale
	// and a torn write self-heals on the next run, so they use plain WriteFile.
	// Only the user-owned repo CLAUDE.md below goes through the atomic+flock
	// import-writer, which exists to never corrupt bytes the user authored.
	guidePath := filepath.Join(dir, "CLAUDE.md")
	if err := os.WriteFile(guidePath, claudemd.Guide, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", guidePath, err)
	}
	fmt.Fprintf(os.Stderr, "deposited %s\n", guidePath)

	// Register the import BEFORE the marker. The marker is the enabled-iff-import
	// signal (§2.7/§2.11), so it must be the last write: if Register fails, enable
	// errors out with no marker, never leaving the repo looking enabled while the
	// import never landed.
	hostPath := filepath.Join(root, "CLAUDE.md")
	wrote, err := claudemd.Register(hostPath, importLine)
	if err != nil {
		return fmt.Errorf("registering import in %s: %w", hostPath, err)
	}
	if wrote {
		fmt.Fprintf(os.Stderr, "added %s to %s\n", importLine, hostPath)
	} else {
		fmt.Fprintf(os.Stderr, "%s already imports beadle\n", hostPath)
	}

	markerPath := filepath.Join(dir, "enabled")
	if err := os.WriteFile(markerPath, nil, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", markerPath, err)
	}

	fmt.Fprintln(os.Stderr, "beadle enabled")
	return nil
}

var disableCmd = &cobra.Command{
	Use:   "disable",
	Short: "Disable beadle guidance in this repo",
	Long: "Remove the @.punt-labs/beadle/CLAUDE.md import from the repo CLAUDE.md\n" +
		"and delete the enabled marker, leaving .punt-labs/beadle/ dormant. Pass\n" +
		"--purge to remove the whole directory.",
	RunE: func(cmd *cobra.Command, args []string) error {
		root, err := repoRoot()
		if err != nil {
			return err
		}
		return disableRepo(root, disablePurge)
	},
}

// disableRepo removes the import from <root>/CLAUDE.md and deletes the enabled
// marker, leaving the rest of .punt-labs/beadle/ dormant. When purge is set it
// removes the whole directory instead.
//
// The order mirrors enable in reverse. enable adds the import then the marker,
// so "marker present ⟹ import present" (§2.11); disable must therefore clear the
// marker BEFORE removing the import. A partial failure then leaves at worst an
// orphan import with no marker (audit-flaggable), never a marker whose import is
// already gone — the state that would make a repo look enabled while it is not.
//
// It holds the same exclusive per-repo lock as enableRepo, so the two are
// mutually exclusive and a concurrent pair reaches one consistent end state.
func disableRepo(root string, purge bool) error {
	return claudemd.WithLock(root, func() error { return disableLocked(root, purge) })
}

func disableLocked(root string, purge bool) error {
	dir := filepath.Join(root, ".punt-labs", "beadle")

	// Clear the enabled signal first. Under --purge the whole directory (which
	// contains the marker) goes now, for the same reason.
	if purge {
		if err := os.RemoveAll(dir); err != nil {
			return fmt.Errorf("purging %s: %w", dir, err)
		}
		fmt.Fprintf(os.Stderr, "purged %s\n", dir)
	} else {
		markerPath := filepath.Join(dir, "enabled")
		if err := os.Remove(markerPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("removing %s: %w", markerPath, err)
		}
	}

	hostPath := filepath.Join(root, "CLAUDE.md")
	wrote, err := claudemd.Prune(hostPath, importLine)
	if err != nil {
		return fmt.Errorf("removing import from %s: %w", hostPath, err)
	}
	if wrote {
		fmt.Fprintf(os.Stderr, "removed %s from %s\n", importLine, hostPath)
	}

	// When enable created CLAUDE.md from nothing, pruning the sole import line
	// leaves a 0-byte file. Remove it, but only when this run actually removed
	// an import line (wrote) — so a no-op disable never deletes a user's own
	// empty CLAUDE.md — and only when the path is a regular file, so os.Lstat
	// leaves a symlinked CLAUDE.md and its link intact.
	if wrote {
		if fi, err := os.Lstat(hostPath); err == nil && fi.Mode().IsRegular() && fi.Size() == 0 {
			if err := os.Remove(hostPath); err != nil {
				return fmt.Errorf("removing empty %s: %w", hostPath, err)
			}
			fmt.Fprintf(os.Stderr, "removed empty %s\n", hostPath)
		}
	}

	fmt.Fprintln(os.Stderr, "beadle disabled")
	return nil
}
