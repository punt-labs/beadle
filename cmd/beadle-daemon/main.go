// Command beadle-daemon is a background daemon that monitors email
// and executes GPG-signed owner instructions.
package main

import (
	"errors"
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
	"github.com/punt-labs/beadle/internal/secret"
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

		dataDir, err := paths.DataDir()
		if err != nil {
			return fmt.Errorf("resolve data dir: %w", err)
		}
		missionsTmpDir := filepath.Join(dataDir, "tmp", "missions")
		if err := os.MkdirAll(missionsTmpDir, 0o750); err != nil {
			return fmt.Errorf("create missions tmp dir: %w", err)
		}
		missions := &daemon.EthosMissionCreator{
			TmpDir: missionsTmpDir,
		}

		// Resolve API key: keychain → file → BEADLE_ANTHROPIC_API_KEY env.
		var apiKey string
		var apiSource string
		apiKey, secretErr := secret.Get("anthropic-api-key")
		if secretErr == nil {
			apiSource = "beadle secret"
		} else if errors.Is(secretErr, secret.ErrNotFound) {
			// Not in any beadle backend — fall back to standard Claude env var.
			apiKey = os.Getenv("ANTHROPIC_API_KEY")
			if apiKey != "" {
				apiSource = "ANTHROPIC_API_KEY env"
			}
		} else {
			// Non-ErrNotFound error (e.g., unsafe file perms) — fail closed.
			logger.Error("secret backend error for anthropic-api-key, worker spawning disabled", "error", secretErr)
		}
		var spawner *daemon.WorkerSpawner
		var templates *daemon.MissionTemplate
		if apiKey != "" {
			spawner = &daemon.WorkerSpawner{
				APIKey: apiKey,
				Logger: logger,
			}
			templates = &daemon.MissionTemplate{
				TmpDir: missionsTmpDir,
			}
			logger.Info("worker spawning enabled", "source", apiSource)
		} else if secretErr == nil || errors.Is(secretErr, secret.ErrNotFound) {
			logger.Warn("worker spawning disabled: no API key found (checked: secret backends, ANTHROPIC_API_KEY env)")
		}

		handler := daemon.NewMailHandler(cmd.Context(), resolver, email.DefaultDialer{}, missions, spawner, templates, logger)
		defer handler.Stop()

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
