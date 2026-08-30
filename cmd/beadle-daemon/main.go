// Command beadle-daemon is a background daemon that monitors email
// and executes GPG-signed owner instructions.
package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
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
	RunE: func(cmd *cobra.Command, _ []string) error {
		logWriter, logPath, logErr := openDaemonLogFile()
		var w io.Writer = os.Stderr
		if logWriter != nil {
			w = io.MultiWriter(os.Stderr, logWriter)
			defer func() { _ = logWriter.Close() }() // best-effort; stderr still has the log
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

		store := &daemon.PipelineStore{
			Dir:    filepath.Join(dataDir, "pipelines"),
			Logger: logger,
		}
		stale, err := store.LoadRunning()
		if err != nil {
			logger.Warn("load stale pipelines", "error", err)
		}
		for _, p := range stale {
			logger.Warn("pipeline was running when daemon stopped",
				"pipeline", p.ID, "from", p.Email.From)
			p.Status = "failed"
			p.Error = "daemon stopped while pipeline was running"
			if saveErr := store.Save(p); saveErr != nil {
				logger.Error("mark stale pipeline failed", "pipeline", p.ID, "error", saveErr)
			}
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

		// gpgBinary is the gpg binary used to verify command-file
		// signatures. It is not identity-scoped: command verification
		// runs once at startup, before any identity's mailbox is
		// polled, so there is no per-message email.Config to read
		// gpg_binary from yet. "gpg" matches email.Config's own
		// default (internal/email/config.go).
		const gpgBinary = "gpg"

		ownerKeyID := resolveDaemonOwnerKeyID(filepath.Join(dataDir, "daemon.json"), resolver, logger)

		// Load command definitions.
		cmdDir := filepath.Join(dataDir, "commands")
		commands, err := daemon.LoadCommands(cmdDir, gpgBinary, ownerKeyID)
		if err != nil {
			logger.Warn("load commands", "dir", cmdDir, "error", err)
			commands = make(map[string]*daemon.Command)
		}
		logger.Info("commands loaded", "count", len(commands), "dir", cmdDir)

		// Configure planner. Use RulePlanner with a "summarize" rule.
		// Reply is auto-appended by the executor after the last stage.
		var planner daemon.Planner
		if len(commands) > 0 {
			rules := []daemon.RuleEntry{
				{
					Pattern: "(?i)summarize|summary",
					Commands: []daemon.CommandCall{
						{Command: "summarize", Args: map[string]any{"text": "see email body"}},
					},
				},
			}
			rp, rpErr := daemon.NewRulePlanner(rules)
			if rpErr != nil {
				logger.Error("create rule planner", "error", rpErr)
			} else {
				planner = rp
			}
		}

		handler := daemon.NewMailHandler(cmd.Context(), resolver, email.DefaultDialer{}, missions, spawner, templates, logger, 0, planner, commands)
		defer handler.Stop()

		// The daemon acts on owner commands — untagged and repo-agnostic,
		// gated by sender-permission and transport-trust — so its poller must
		// count the whole mailbox, matching handler.go's unscoped fetch. Repo
		// scoping applies only to the interactive serve/MCP poller (DES-033).
		poller := email.NewPoller(handler.OnNewMail, resolver, logger, email.DefaultDialer{},
			email.WithRepoScope(func() string { return "" }))
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

// resolveDaemonOwnerKeyID resolves the daemon's signing-enforcement policy
// from configPath (DES-035). It returns "" -- signature verification
// disabled, LoadCommands behaves exactly as it does today -- on any failure
// to resolve an owner key: an absent or unreadable daemon.json, an
// ambiguous or unresolvable owner config, or a malformed fingerprint. That
// failure disables command loading only, never the daemon process: mail
// polling and the MCP server (a separate binary) have no dependency on the
// commands map. An absent daemon.json is the common, expected case for any
// daemon that has not opted into this feature, so it is silent; every other
// failure logs at Error, since it means the operator tried to configure
// enforcement and it did not take effect. errors.Is (not os.IsNotExist,
// which does not unwrap %w chains) is required here because LoadConfig
// wraps the underlying os.ReadFile error.
func resolveDaemonOwnerKeyID(configPath string, resolver *identity.Resolver, logger *slog.Logger) string {
	cfg, err := daemon.LoadConfig(configPath)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			logger.Error("daemon config unreadable, command loading disabled", "error", err)
		}
		return ""
	}
	ownerKeyID, err := cfg.ResolveOwnerKeyID(resolver)
	if err != nil {
		logger.Error("signature policy unavailable, command loading disabled", "error", err)
		return "" // explicit: falls back to disabled, never to "trust anyway"
	}
	return ownerKeyID
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
	f, err := os.OpenFile(filepath.Clean(path), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, "", fmt.Errorf("open %s: %w", path, err)
	}
	return f, path, nil
}
