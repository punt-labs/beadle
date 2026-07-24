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
		dir := filepath.Join(root, ".punt-labs", "beadle")
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return fmt.Errorf("creating %s: %w", dir, err)
		}

		guidePath := filepath.Join(dir, "CLAUDE.md")
		if err := os.WriteFile(guidePath, claudemd.Guide, 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", guidePath, err)
		}
		fmt.Fprintf(os.Stderr, "deposited %s\n", guidePath)

		markerPath := filepath.Join(dir, "enabled")
		if err := os.WriteFile(markerPath, nil, 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", markerPath, err)
		}

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
		fmt.Fprintln(os.Stderr, "beadle enabled")
		return nil
	},
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
		dir := filepath.Join(root, ".punt-labs", "beadle")

		hostPath := filepath.Join(root, "CLAUDE.md")
		wrote, err := claudemd.Prune(hostPath, importLine)
		if err != nil {
			return fmt.Errorf("removing import from %s: %w", hostPath, err)
		}
		if wrote {
			fmt.Fprintf(os.Stderr, "removed %s from %s\n", importLine, hostPath)
		}

		markerPath := filepath.Join(dir, "enabled")
		if err := os.Remove(markerPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("removing %s: %w", markerPath, err)
		}

		if disablePurge {
			if err := os.RemoveAll(dir); err != nil {
				return fmt.Errorf("purging %s: %w", dir, err)
			}
			fmt.Fprintf(os.Stderr, "purged %s\n", dir)
		}
		fmt.Fprintln(os.Stderr, "beadle disabled")
		return nil
	},
}
