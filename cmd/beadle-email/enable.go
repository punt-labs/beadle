package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/punt-labs/beadle/internal/enable"
)

// importLine is the canonical @-import the repo CLAUDE.md carries when beadle
// is enabled. It aliases the shared source of truth so the CLI, the MCP tool,
// and every tool the standard governs write byte-identical lines.
const importLine = enable.ImportLine

// disablePurge, when set, makes disable remove the whole .punt-labs/beadle/
// directory rather than leaving it dormant.
var disablePurge bool

func init() {
	disableCmd.Flags().BoolVar(&disablePurge, "purge", false,
		"Remove the whole .punt-labs/beadle/ directory, not just the enabled marker")
}

// progressf writes an enable/disable progress line to stderr unless --quiet is
// set. It carries only progress, never errors: errors are returned to the
// caller and surface regardless of --quiet.
func progressf(format string, a ...any) {
	if g.Quiet {
		return
	}
	fmt.Fprintf(os.Stderr, format, a...)
}

// enableRepo and disableRepo adapt the shared internal/enable operations to the
// CLI: they bind the --quiet-aware progress writer, and the CLI tests drive
// them. The MCP enable tool calls the same internal/enable code with a nil
// progress sink, so both surfaces write the identical marker (§2.14).
func enableRepo(root string) error          { return enable.Enable(root, progressf) }
func disableRepo(root string, p bool) error { return enable.Disable(root, p, progressf) }

var enableCmd = &cobra.Command{
	Use:   "enable",
	Short: "Enable beadle guidance in this repo",
	Long: "Deposit the beadle user guide into .punt-labs/beadle/, mark the repo\n" +
		"enabled, and add the @.punt-labs/beadle/CLAUDE.md import to the repo\n" +
		"CLAUDE.md. Idempotent: re-running is the upgrade path.",
	RunE: func(cmd *cobra.Command, args []string) error {
		root, err := enable.RepoRoot()
		if err != nil {
			return err
		}
		return enableRepo(root)
	},
}

var disableCmd = &cobra.Command{
	Use:   "disable",
	Short: "Disable beadle guidance in this repo",
	Long: "Remove the @.punt-labs/beadle/CLAUDE.md import from the repo CLAUDE.md\n" +
		"and delete the enabled marker, leaving .punt-labs/beadle/ dormant. Pass\n" +
		"--purge to remove the whole directory.",
	RunE: func(cmd *cobra.Command, args []string) error {
		root, err := enable.RepoRoot()
		if err != nil {
			return err
		}
		return disableRepo(root, disablePurge)
	},
}
