package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/spf13/cobra"

	"github.com/punt-labs/beadle/internal/contacts"
	"github.com/punt-labs/beadle/internal/email"
	"github.com/punt-labs/beadle/internal/identity"
	mcptools "github.com/punt-labs/beadle/internal/mcp"
	"github.com/punt-labs/beadle/internal/paths"
	"github.com/punt-labs/beadle/internal/pgp"
	"github.com/punt-labs/beadle/internal/secret"
)

// --- serve ---

var (
	serveTransport string
	servePort      int
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start MCP server",
	Long:  "Start the beadle-email MCP server. Transport: stdio (default) or ws (WebSocket).",
	RunE: func(_ *cobra.Command, _ []string) error {
		logWriter, logPath, logErr := openServeLogFile()
		var w io.Writer = os.Stderr
		if logWriter != nil {
			w = io.MultiWriter(os.Stderr, logWriter)
			defer func() { _ = logWriter.Close() }() // best-effort; stderr still has the log
		}
		logger := slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{
			Level: g.slogLevel(),
		}))
		if logErr != nil {
			logger.Warn("file logging disabled", "error", logErr)
		} else {
			logger.Info("file logging enabled", "path", logPath)
		}
		resolver, err := newResolver()
		if err != nil {
			return err
		}
		ethosDir, _ := paths.EthosDir()
		s := server.NewMCPServer(
			"beadle-email",
			version,
			server.WithToolCapabilities(true),
			server.WithInstructions(mcptools.ServerInstructions),
			server.WithExperimental(map[string]any{
				"claude/channel": map[string]any{},
			}),
		)
		// marker is assigned by RegisterTools below; observeCount closes over it
		// and fires only after Start, by which time it is set.
		var marker *mcptools.UnreadMarker
		onNewMail := func(newCount uint32) {
			s.SendNotificationToAllClients(mcp.MethodNotificationToolsListChanged, nil)
			logger.Info("poller: sent tools/list_changed notification")
			channelParams := map[string]any{
				"content": fmt.Sprintf("%d new message(s) in inbox. Check with /inbox.", newCount),
				"meta": map[string]string{
					"source": "beadle-email",
					"type":   "inbox_alert",
				},
			}
			logger.Info("poller: sending channel notification", "content", channelParams["content"])
			s.SendNotificationToAllClients("notifications/claude/channel", channelParams)
			logger.Info("poller: channel notification sent")
		}
		// observeCount tracks every count change, including drops, so the unread
		// marker clears when the inbox is read down. onNewMail prompts only on an
		// increase, so a drop never wakes the agent.
		observeCount := func(unseen uint32) {
			if marker != nil {
				marker.Update(unseen)
			}
		}
		poller := email.NewPoller(onNewMail, resolver, logger, email.DefaultDialer{}, email.WithCountObserver(observeCount))
		marker = mcptools.RegisterTools(s, resolver, logger, mcptools.WithEthosDir(ethosDir), mcptools.WithPoller(poller))
		if err := poller.Start(); err != nil {
			logger.Error("background polling failed to start", "error", err)
		}
		defer poller.Stop()
		logger.Info("starting beadle-email MCP server", "version", version, "transport", serveTransport)

		switch serveTransport {
		case "stdio":
			return server.ServeStdio(s)
		case "ws":
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			sigCh := make(chan os.Signal, 1)
			signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
			defer signal.Stop(sigCh)
			go func() {
				<-sigCh
				cancel()
			}()
			ws := mcptools.NewWSServer(s, version, logger)
			return ws.ListenAndServe(ctx, servePort)
		default:
			return fmt.Errorf("unknown transport %q (expected stdio or ws)", serveTransport)
		}
	},
}

func init() {
	serveCmd.Flags().StringVar(&serveTransport, "transport", "stdio", "Transport: stdio or ws")
	serveCmd.Flags().IntVar(&servePort, "port", 8420, "WebSocket server port (ws transport only)")
}

// --- version ---

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version",
	Run: func(_ *cobra.Command, _ []string) {
		fmt.Printf("beadle-email %s\n", version)
	},
}

// --- doctor ---

// doctorCheck is one line of doctor output: a named health check, its status
// (OK, WARN, or FAIL), and an optional human-readable detail.
type doctorCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

var doctorConfig string

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check installation health",
	Long:  "Run health checks on identity, credentials, GPG, SMTP, and contacts.",
	RunE: func(cmd *cobra.Command, _ []string) error {
		var checks []doctorCheck

		checks = append(checks, doctorCheck{"version", "OK", version})

		// Check identity resolution
		var id *identity.Identity
		var idErr error
		resolver, resolverErr := newResolver()
		if resolverErr != nil {
			checks = append(checks, doctorCheck{"identity", "FAIL", resolverErr.Error()})
			idErr = resolverErr
		} else {
			id, idErr = resolver.Resolve()
			if idErr != nil {
				checks = append(checks, doctorCheck{"identity", "WARN", fmt.Sprintf("no identity: %v", idErr)})
			} else {
				checks = append(checks, doctorCheck{"identity", "OK", fmt.Sprintf("%s (source: %s)", id.Email, id.Source)})
			}
		}

		// Check credential backends
		backends := secret.Available()
		checks = append(checks, doctorCheck{"secret_backends", "OK", strings.Join(backends, ", ")})

		// Check config file — falls back to the identity-scoped config the
		// same way statusCmd does, so doctor reports on whatever config is
		// actually in effect. An explicit -c/--config always wins.
		cfg, usedConfigPath, cfgErr := loadConfigForCmd(cmd, id, idErr, doctorConfig)
		if cfgErr != nil {
			checks = append(checks, doctorCheck{"config", "FAIL", cfgErr.Error()})
		} else {
			checks = append(checks, doctorCheck{"config", "OK", usedConfigPath})

			if _, err := cfg.IMAPPassword(); err != nil {
				checks = append(checks, doctorCheck{"imap_password", "FAIL", err.Error()})
			} else {
				checks = append(checks, doctorCheck{"imap_password", "OK", ""})
			}

			if _, err := cfg.ResendAPIKey(); err != nil {
				checks = append(checks, doctorCheck{"resend_api_key", "FAIL", err.Error()})
			} else {
				checks = append(checks, doctorCheck{"resend_api_key", "OK", ""})
			}

			gpgAvailable := false
			gpgPath, err := exec.LookPath(cfg.GPGBinary)
			if err != nil {
				checks = append(checks, doctorCheck{"gpg", "FAIL", fmt.Sprintf("%s not found", cfg.GPGBinary)})
			} else {
				gpgAvailable = true
				checks = append(checks, doctorCheck{"gpg", "OK", gpgPath})
			}

			// Signing checks only run when gpg_signer is configured AND gpg is available.
			if cfg.GPGSigner != "" && gpgAvailable {
				gpgKeyCmd := exec.Command(cfg.GPGBinary, "--list-keys", cfg.GPGSigner)
				keyExists := gpgKeyCmd.Run() == nil
				if !keyExists {
					checks = append(checks, doctorCheck{"gpg_signing_key", "FAIL", fmt.Sprintf("no key for %s", cfg.GPGSigner)})
				} else {
					checks = append(checks, doctorCheck{"gpg_signing_key", "OK", cfg.GPGSigner})
				}

				switch {
				case !keyExists:
					if _, err := cfg.GPGPassphrase(); err != nil {
						checks = append(checks, doctorCheck{"gpg_passphrase", "FAIL", err.Error()})
					} else {
						checks = append(checks, doctorCheck{"gpg_passphrase", "OK", ""})
					}
				default:
					needsPassphrase, _ := pgp.KeyRequiresPassphrase(cfg.GPGBinary, cfg.GPGSigner)
					switch {
					case !needsPassphrase:
						checks = append(checks, doctorCheck{
							"gpg_passphrase", "OK",
							fmt.Sprintf("not required (%s has no passphrase — filesystem access grants signing authority)", cfg.GPGSigner),
						})
					default:
						if _, err := cfg.GPGPassphrase(); err != nil {
							checks = append(checks, doctorCheck{"gpg_passphrase", "FAIL", err.Error()})
						} else {
							checks = append(checks, doctorCheck{"gpg_passphrase", "OK", ""})
						}
					}
				}
			} else if cfg.GPGSigner != "" {
				checks = append(checks, doctorCheck{"gpg_signing_key", "FAIL", fmt.Sprintf("cannot check signing key: gpg binary %q not found", cfg.GPGBinary)})
			} else {
				checks = append(checks, doctorCheck{"gpg_signing_key", "OK", "signing disabled (gpg_signer not configured)"})
			}

			if email.SMTPAvailable(cfg) {
				checks = append(checks, doctorCheck{"smtp", "OK", fmt.Sprintf("%s:%d", cfg.SMTPEffectiveHost(), cfg.SMTPPort)})
			} else {
				checks = append(checks, doctorCheck{"smtp", "WARN", fmt.Sprintf("Proton Bridge SMTP not reachable at %s:%d — will use Resend fallback", cfg.SMTPEffectiveHost(), cfg.SMTPPort)})
			}
		}

		// Check contacts file
		contactsPath := resolveContactsPath()
		cs := contacts.NewStore(contactsPath)
		if err := cs.Load(); err != nil {
			checks = append(checks, doctorCheck{"contacts", "FAIL", err.Error()})
		} else if cs.Count() == 0 {
			checks = append(checks, doctorCheck{"contacts", "WARN", fmt.Sprintf("no contacts at %s", contactsPath)})
		} else {
			checks = append(checks, doctorCheck{"contacts", "OK", fmt.Sprintf("%d contacts at %s", cs.Count(), contactsPath)})
		}

		// Check for MCP-registration drift: a standalone server duplicating the
		// plugin, or a project-scope registration. The plugin is the single
		// automatic source; anything else is drift this check makes visible.
		checks = append(checks, inspectMCPRegistration()...)

		failed := false
		for _, c := range checks {
			if c.Status == "FAIL" {
				failed = true
			}
		}

		g.printResult(checks, func() {
			for _, c := range checks {
				mark := "+"
				if c.Status == "FAIL" {
					mark = "!"
				}
				if c.Detail != "" {
					fmt.Printf("[%s] %-16s %s\n", mark, c.Name, c.Detail)
				} else {
					fmt.Printf("[%s] %s\n", mark, c.Name)
				}
			}
		})

		if failed {
			return fmt.Errorf("health check failed")
		}
		return nil
	},
}

func init() {
	doctorCmd.Flags().StringVarP(&doctorConfig, "config", "c", email.DefaultConfigPath(), "Config file path")
}

// loadConfigForCmd loads the config for a doctor/status invocation. An
// explicit -c/--config always wins over the implicit identity-config
// preference: when the user passed it, cmd.Flags().Changed("config") is
// true and fallbackPath (the flag's value) is used directly, skipping
// identity-config lookup entirely. Otherwise this defers to
// email.LoadIdentityConfig's identity-scoped-over-fallback precedence,
// passing id only when idErr is nil — an unresolved identity must not be
// consulted for its (possibly stale or zero-value) Email field.
func loadConfigForCmd(cmd *cobra.Command, id *identity.Identity, idErr error, fallbackPath string) (cfg *email.Config, usedPath string, err error) {
	if cmd.Flags().Changed("config") {
		cfg, err = email.LoadConfig(fallbackPath)
		return cfg, fallbackPath, err
	}
	var idArg *identity.Identity
	if idErr == nil {
		idArg = id
	}
	return email.LoadIdentityConfig(idArg, fallbackPath)
}

// --- status ---

var statusConfig string

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show current state",
	Long:  "Show operational state: version, IMAP/SMTP settings, identity, contacts count.",
	RunE: func(cmd *cobra.Command, _ []string) error {
		resolver, err := newResolver()
		if err != nil {
			return err
		}
		id, idErr := resolver.Resolve()

		cfg, usedConfigPath, err := loadConfigForCmd(cmd, id, idErr, statusConfig)
		if err != nil {
			return err
		}

		contactsPath := resolveContactsPath()
		cs := contacts.NewStore(contactsPath)
		contactsCount := "0"
		contactsError := ""
		if err := cs.Load(); err != nil {
			contactsError = err.Error()
		} else {
			contactsCount = fmt.Sprintf("%d", cs.Count())
		}

		status := map[string]string{
			"version":        version,
			"imap_host":      cfg.IMAPHost,
			"imap_port":      fmt.Sprintf("%d", cfg.IMAPPort),
			"imap_user":      cfg.IMAPUser,
			"smtp_host":      cfg.SMTPEffectiveHost(),
			"smtp_user":      cfg.SMTPEffectiveUser(),
			"smtp_port":      fmt.Sprintf("%d", cfg.SMTPPort),
			"from_address":   cfg.FromAddress,
			"gpg_binary":     cfg.GPGBinary,
			"gpg_signer":     cfg.GPGSigner,
			"config":         usedConfigPath,
			"contacts_path":  contactsPath,
			"contacts_count": contactsCount,
		}
		if idErr == nil {
			status["identity_email"] = id.Email
			status["identity_source"] = id.Source
			if id.Handle != "" {
				status["identity_handle"] = id.Handle
			}
			if id.Name != "" {
				status["identity_name"] = id.Name
			}
		} else {
			status["identity_error"] = idErr.Error()
		}
		if contactsError != "" {
			status["contacts_error"] = contactsError
		}

		g.printResult(status, func() {
			for k, v := range status {
				fmt.Printf("%-18s %s\n", k+":", v)
			}
		})
		return nil
	},
}

func init() {
	statusCmd.Flags().StringVarP(&statusConfig, "config", "c", email.DefaultConfigPath(), "Config file path")
}

// openServeLogFile opens ~/.punt-labs/beadle/logs/beadle-email.log for append.
// Returns the file, its path, and any error. On error, caller should fall back
// to stderr-only logging.
func openServeLogFile() (*os.File, string, error) {
	dir, err := paths.DataDir()
	if err != nil {
		return nil, "", err
	}
	logDir := filepath.Join(dir, "logs")
	if err := os.MkdirAll(logDir, 0o750); err != nil {
		return nil, "", fmt.Errorf("create log dir %s: %w", logDir, err)
	}
	path := filepath.Join(logDir, "beadle-email.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, "", fmt.Errorf("open %s: %w", path, err)
	}
	return f, path, nil
}
