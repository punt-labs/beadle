// Command beadle-email is a stdio MCP server providing email channel tools
// for Beadle. It connects to Proton Bridge via IMAP and sends via Resend API.
package main

import (
	"os"

	"github.com/spf13/cobra"
)

// version is set at build time via -ldflags="-X main.version=..."
// Defaults to "dev" for local builds without ldflags.
var version = "dev"

// g holds global flags bound by cobra persistent flags.
var g globalOpts

var rootCmd = &cobra.Command{
	Use:   "beadle-email",
	Short: "beadle-email: Beadle email channel MCP server",
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		cmd.SilenceUsage = true
	},
}

func init() {
	rootCmd.PersistentFlags().BoolVarP(&g.JSON, "json", "j", false, "JSON output")
	rootCmd.PersistentFlags().BoolVarP(&g.Verbose, "verbose", "v", false, "Debug logging")
	rootCmd.PersistentFlags().BoolVarP(&g.Quiet, "quiet", "q", false, "Errors only")

	// Product commands first
	rootCmd.AddCommand(listCmd)
	rootCmd.AddCommand(readCmd)
	rootCmd.AddCommand(sendCmd)
	rootCmd.AddCommand(moveCmd)
	rootCmd.AddCommand(foldersCmd)
	rootCmd.AddCommand(contactCmd)

	// Admin commands
	rootCmd.AddCommand(serveCmd)
	rootCmd.AddCommand(installCmd)
	rootCmd.AddCommand(uninstallCmd)
	rootCmd.AddCommand(doctorCmd)
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(versionCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
