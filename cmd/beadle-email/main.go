// Command beadle-email is a stdio MCP server providing email channel tools
// for Beadle. It connects to Proton Bridge via IMAP and sends via Resend API.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/punt-labs/punt-observe-go/logging"
	"github.com/spf13/cobra"

	"github.com/punt-labs/beadle/internal/usage"
)

// version is set at build time via -ldflags="-X main.version=..."
// Defaults to "dev" for local builds without ldflags.
var version = "dev"

// g holds global flags bound by cobra persistent flags.
var g globalOpts

var (
	appLogger      = slog.New(slog.DiscardHandler)
	logHandler     *logging.Handler
	appUsage       *usage.Client
	usageCommand   usage.Command
	commandStarted time.Time
)

var rootCmd = &cobra.Command{
	Use:               "beadle-email",
	Short:             "beadle-email: Beadle email channel MCP server",
	PersistentPreRunE: func(cmd *cobra.Command, _ []string) error { return configureObservability(cmd) },
}

func configureObservability(cmd *cobra.Command) error {
	commandStarted = time.Now()
	cmd.SilenceUsage = true
	role := "cli"
	if cmd.Name() == "serve" {
		role = "mcp"
	}
	var err error
	logHandler, err = logging.New("beadle", role, logging.WithLevel(g.slogLevel()))
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("initialize logging: log lock busy; another beadle process may be holding it; retry: %w", err)
		}
		return fmt.Errorf("initialize logging: %w", err)
	}
	appLogger = slog.New(logHandler).With("component", "beadle."+role)
	missionID := os.Getenv("BEADLE_LOG_MISSION_ID")
	if len(missionID) <= 64 && strings.HasPrefix(missionID, "m-") && strings.Trim(missionID, "abcdefghijklmnopqrstuvwxyz0123456789-") == "" {
		appLogger = appLogger.With("mission", missionID)
	}
	appUsage = usage.New(appLogger.With("component", "beadle.usage"))
	usageCommand = usage.CommandForPath(cmd.CommandPath())
	appLogger.Debug("Diagnostic logging configured")
	appLogger.Info("Command started", "command", cmd.Name())
	return nil
}

func init() {
	rootCmd.PersistentFlags().BoolVarP(&g.JSON, "json", "j", false, "JSON output")
	rootCmd.PersistentFlags().BoolVarP(&g.Verbose, "verbose", "v", false, "Debug logging")
	rootCmd.PersistentFlags().BoolVarP(&g.Quiet, "quiet", "q", false, "Suppress optional command output (does not affect JSON or logging)")

	// Product commands first
	rootCmd.AddCommand(listCmd)
	rootCmd.AddCommand(searchCmd)
	rootCmd.AddCommand(readCmd)
	rootCmd.AddCommand(sendCmd)
	rootCmd.AddCommand(replyCmd)
	rootCmd.AddCommand(moveCmd)
	rootCmd.AddCommand(markCmd)
	rootCmd.AddCommand(foldersCmd)
	rootCmd.AddCommand(contactCmd)

	// Admin commands
	rootCmd.AddCommand(serveCmd)
	rootCmd.AddCommand(installCmd)
	rootCmd.AddCommand(uninstallCmd)
	rootCmd.AddCommand(enableCmd)
	rootCmd.AddCommand(disableCmd)
	rootCmd.AddCommand(doctorCmd)
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(identityCmd)
	rootCmd.AddCommand(healthCmd)
	rootCmd.AddCommand(versionCmd)
}

func main() {
	started := time.Now()
	cmd, err := rootCmd.ExecuteC()
	if err != nil && commandStarted.IsZero() && cmd != nil && usage.CommandForPath(cmd.CommandPath()) != "" {
		if initErr := configureObservability(cmd); initErr != nil {
			fmt.Fprintln(os.Stderr, initErr)
		} else {
			commandStarted = started
		}
	}
	if appUsage != nil {
		if usageCommand != usage.CommandServe {
			outcome := usage.OK
			if err != nil {
				outcome = usage.Error
			}
			appUsage.CLI(usageCommand, outcome, commandStarted)
		}
		appUsage.Shutdown()
	}
	if logHandler != nil {
		appLogger.Info("Command completed", "success", err == nil)
		if closeErr := logHandler.Close(); closeErr != nil {
			fmt.Fprintln(os.Stderr, "close logging:", closeErr)
		}
	}
	if err != nil {
		os.Exit(1)
	}
}
