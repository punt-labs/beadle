// Command beadle-daemon is a background daemon that monitors email
// and executes GPG-signed owner instructions.
package main

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/punt-labs/beadle/internal/daemon"
	"github.com/punt-labs/beadle/internal/email"
	"github.com/punt-labs/beadle/internal/identity"
	"github.com/punt-labs/beadle/internal/paths"
)

var version = "dev"

var rootCmd = &cobra.Command{
	Use:   "beadle-daemon",
	Short: "beadle-daemon: background daemon for Beadle",
}

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Start the daemon",
	Long:  "Start the background daemon. Polls for new mail and blocks until SIGTERM or SIGINT.",
	RunE: func(cmd *cobra.Command, args []string) error {
		logWriter, logPath, logErr := openDaemonLogFile()
		var w io.Writer = os.Stderr
		if logWriter != nil {
			w = io.MultiWriter(os.Stderr, logWriter)
			defer logWriter.Close()
		}
		logger := slog.New(slog.NewTextHandler(w, nil))
		if logErr != nil {
			logger.Warn("file logging disabled", "error", logErr)
		} else {
			logger.Info("file logging enabled", "path", logPath)
		}

		resolver, err := newResolver()
		if err != nil {
			return fmt.Errorf("create resolver: %w", err)
		}

		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("resolve cwd: %w", err)
		}
		missions := &daemon.EthosMissionCreator{
			TmpDir: filepath.Join(cwd, ".tmp", "missions"),
		}
		handler := daemon.NewMailHandler(resolver, email.DefaultDialer{}, missions, logger)

		poller := email.NewPoller(handler.OnNewMail, resolver, logger, email.DefaultDialer{})
		if err := poller.Start(); err != nil {
			return fmt.Errorf("start poller: %w", err)
		}
		defer poller.Stop()

		logger.Info("daemon started", "version", version)

		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
		defer signal.Stop(sigCh)
		sig := <-sigCh
		logger.Info("shutting down", "signal", sig)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(runCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

// newResolver creates an identity resolver using standard paths.
func newResolver() (*identity.Resolver, error) {
	ethosDir, err := paths.EthosDir()
	if err != nil {
		return nil, err
	}
	beadleDir, err := paths.DataDir()
	if err != nil {
		return nil, err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("resolve cwd: %w", err)
	}
	return identity.NewResolver(ethosDir, beadleDir, cwd), nil
}

// openDaemonLogFile opens ~/.punt-labs/beadle/logs/beadle-daemon.log for append.
func openDaemonLogFile() (*os.File, string, error) {
	dir, err := paths.DataDir()
	if err != nil {
		return nil, "", err
	}
	logDir := filepath.Join(dir, "logs")
	if err := os.MkdirAll(logDir, 0o750); err != nil {
		return nil, "", fmt.Errorf("create log dir %s: %w", logDir, err)
	}
	path := filepath.Join(logDir, "beadle-daemon.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, "", fmt.Errorf("open %s: %w", path, err)
	}
	return f, path, nil
}
