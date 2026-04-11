package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"

	"github.com/mark3labs/mcp-go/server"
	"github.com/spf13/cobra"

	"github.com/punt-labs/beadle/internal/contacts"
	"github.com/punt-labs/beadle/internal/email"
	mcptools "github.com/punt-labs/beadle/internal/mcp"
	"github.com/punt-labs/beadle/internal/paths"
	"github.com/punt-labs/beadle/internal/pgp"
	"github.com/punt-labs/beadle/internal/secret"
)

// --- serve ---

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start MCP server",
	Long:  "Start the beadle-email MCP server on stdio transport.",
	RunE: func(cmd *cobra.Command, args []string) error {
		logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
			Level: g.slogLevel(),
		}))
		resolver, err := newResolver()
		if err != nil {
			return err
		}
		ethosDir, _ := paths.EthosDir()
		s := server.NewMCPServer(
			"beadle-email",
			version,
			server.WithToolCapabilities(true),
		)
		poller := email.NewPoller(s, resolver, logger, email.DefaultDialer{})
		mcptools.RegisterTools(s, resolver, logger, mcptools.WithEthosDir(ethosDir), mcptools.WithPoller(poller))
		if err := poller.Start(); err != nil {
			logger.Error("background polling failed to start", "error", err)
		}
		defer poller.Stop()
		logger.Info("starting beadle-email MCP server", "version", version)
		return server.ServeStdio(s)
	},
}

// --- version ---

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("beadle-email %s\n", version)
	},
}

// --- doctor ---

var doctorConfig string

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check installation health",
	Long:  "Run health checks on identity, credentials, GPG, SMTP, and contacts.",
	RunE: func(cmd *cobra.Command, args []string) error {
		type check struct {
			Name   string `json:"name"`
			Status string `json:"status"`
			Detail string `json:"detail,omitempty"`
		}

		var checks []check

		checks = append(checks, check{"version", "OK", version})

		// Check identity resolution
		resolver, resolverErr := newResolver()
		if resolverErr != nil {
			checks = append(checks, check{"identity", "FAIL", resolverErr.Error()})
		} else {
			id, idErr := resolver.Resolve()
			if idErr != nil {
				checks = append(checks, check{"identity", "WARN", fmt.Sprintf("no identity: %v", idErr)})
			} else {
				checks = append(checks, check{"identity", "OK", fmt.Sprintf("%s (source: %s)", id.Email, id.Source)})
			}
		}

		// Check credential backends
		backends := secret.Available()
		checks = append(checks, check{"secret_backends", "OK", strings.Join(backends, ", ")})

		// Check config file
		cfg, err := email.LoadConfig(doctorConfig)
		if err != nil {
			checks = append(checks, check{"config", "FAIL", err.Error()})
		} else {
			checks = append(checks, check{"config", "OK", doctorConfig})

			if _, err := cfg.IMAPPassword(); err != nil {
				checks = append(checks, check{"imap_password", "FAIL", err.Error()})
			} else {
				checks = append(checks, check{"imap_password", "OK", ""})
			}

			if _, err := cfg.ResendAPIKey(); err != nil {
				checks = append(checks, check{"resend_api_key", "FAIL", err.Error()})
			} else {
				checks = append(checks, check{"resend_api_key", "OK", ""})
			}

			gpgPath, err := exec.LookPath(cfg.GPGBinary)
			if err != nil {
				checks = append(checks, check{"gpg", "FAIL", fmt.Sprintf("%s not found", cfg.GPGBinary)})
			} else {
				checks = append(checks, check{"gpg", "OK", gpgPath})
			}

			// Signing checks only run when gpg_signer is configured.
			if cfg.GPGSigner != "" {
				gpgKeyCmd := exec.Command(cfg.GPGBinary, "--list-keys", cfg.GPGSigner)
				keyExists := gpgKeyCmd.Run() == nil
				if !keyExists {
					checks = append(checks, check{"gpg_signing_key", "FAIL", fmt.Sprintf("no key for %s", cfg.GPGSigner)})
				} else {
					checks = append(checks, check{"gpg_signing_key", "OK", cfg.GPGSigner})
				}

				// gpg_passphrase is conditional on whether the key actually
				// needs one. Probe the key protection state only if the key
				// exists — otherwise the probe fails indistinguishably and
				// falls back to the legacy "secret required" behavior so at
				// least the detail text points at the right fix.
				switch {
				case !keyExists:
					if _, err := cfg.GPGPassphrase(); err != nil {
						checks = append(checks, check{"gpg_passphrase", "FAIL", err.Error()})
					} else {
						checks = append(checks, check{"gpg_passphrase", "OK", ""})
					}
				default:
					needsPassphrase, _ := pgp.KeyRequiresPassphrase(cfg.GPGBinary, cfg.GPGSigner)
					switch {
					case !needsPassphrase:
						checks = append(checks, check{"gpg_passphrase", "OK",
							fmt.Sprintf("not required (%s has no passphrase — filesystem access grants signing authority)", cfg.GPGSigner)})
					default:
						if _, err := cfg.GPGPassphrase(); err != nil {
							checks = append(checks, check{"gpg_passphrase", "FAIL", err.Error()})
						} else {
							checks = append(checks, check{"gpg_passphrase", "OK", ""})
						}
					}
				}
			} else {
				checks = append(checks, check{"gpg_signing_key", "OK", "signing disabled (gpg_signer not configured)"})
			}

			if email.SMTPAvailable(cfg) {
				checks = append(checks, check{"smtp", "OK", fmt.Sprintf("%s:%d", cfg.SMTPEffectiveHost(), cfg.SMTPPort)})
			} else {
				checks = append(checks, check{"smtp", "WARN", fmt.Sprintf("Proton Bridge SMTP not reachable at %s:%d — will use Resend fallback", cfg.SMTPEffectiveHost(), cfg.SMTPPort)})
			}
		}

		// Check contacts file
		contactsPath := resolveContactsPath()
		cs := contacts.NewStore(contactsPath)
		if err := cs.Load(); err != nil {
			checks = append(checks, check{"contacts", "FAIL", err.Error()})
		} else if cs.Count() == 0 {
			checks = append(checks, check{"contacts", "WARN", fmt.Sprintf("no contacts at %s", contactsPath)})
		} else {
			checks = append(checks, check{"contacts", "OK", fmt.Sprintf("%d contacts at %s", cs.Count(), contactsPath)})
		}

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

// --- status ---

var statusConfig string

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show current state",
	Long:  "Show operational state: version, IMAP/SMTP settings, identity, contacts count.",
	RunE: func(cmd *cobra.Command, args []string) error {
		resolver, err := newResolver()
		if err != nil {
			return err
		}
		id, idErr := resolver.Resolve()

		var cfg *email.Config
		usedConfigPath := statusConfig
		if idErr == nil {
			idConfigPath, pathErr := paths.IdentityConfigPath(id.Email)
			if pathErr == nil {
				idCfg, cfgErr := email.LoadConfig(idConfigPath)
				if cfgErr == nil {
					cfg = idCfg
					usedConfigPath = idConfigPath
				} else if !errors.Is(cfgErr, os.ErrNotExist) {
					fmt.Fprintf(os.Stderr, "warning: identity config %s: %v (using fallback)\n", idConfigPath, cfgErr)
				}
			}
		}
		if cfg == nil {
			var cfgErr error
			cfg, cfgErr = email.LoadConfig(statusConfig)
			if cfgErr != nil {
				return cfgErr
			}
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
