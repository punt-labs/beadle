// Command beadle-email is a stdio MCP server providing email channel tools
// for Beadle. It connects to Proton Bridge via IMAP and sends via Resend API.
//
// Usage:
//
//	beadle-email serve [--config PATH]    # Start MCP server (default)
//	beadle-email version                  # Print version
//	beadle-email doctor [--config PATH]   # Check installation health
//	beadle-email status [--config PATH]   # Current state summary
package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"

	"github.com/mark3labs/mcp-go/server"

	"github.com/punt-labs/beadle/internal/email"
	mcptools "github.com/punt-labs/beadle/internal/mcp"
	"github.com/punt-labs/beadle/internal/secret"
)

const version = "0.1.0"

const usage = `beadle-email: Beadle email channel MCP server

Usage:
  beadle-email serve [--config PATH]    Start MCP server (stdio transport)
  beadle-email version                  Print version and exit
  beadle-email doctor [--config PATH]   Check installation health
  beadle-email status [--config PATH]   Show current state
  beadle-email --help                   Show this help

Without a subcommand, starts the MCP server (same as 'serve').
`

func main() {
	os.Exit(run())
}

func run() int {
	args := os.Args[1:]

	// Default to serve when no subcommand given
	if len(args) == 0 {
		return runServe(email.DefaultConfigPath())
	}

	switch args[0] {
	case "version":
		fmt.Printf("beadle-email %s\n", version)
		return 0

	case "doctor":
		configPath := extractConfig(args[1:])
		return runDoctor(configPath)

	case "status":
		configPath := extractConfig(args[1:])
		return runStatus(configPath)

	case "serve":
		configPath := extractConfig(args[1:])
		return runServe(configPath)

	case "--help", "-h":
		fmt.Fprint(os.Stderr, usage)
		return 0

	case "--version", "-v":
		fmt.Printf("beadle-email %s\n", version)
		return 0

	default:
		fmt.Fprintf(os.Stderr, "error: unknown command %q\n\n%s", args[0], usage)
		return 2
	}
}

func extractConfig(args []string) string {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "--config" || args[i] == "-c" {
			return args[i+1]
		}
	}
	return email.DefaultConfigPath()
}

func runServe(configPath string) int {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: logLevel(),
	}))

	cfg, err := email.LoadConfig(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	s := server.NewMCPServer(
		"beadle-email",
		version,
		server.WithToolCapabilities(false),
	)

	mcptools.RegisterTools(s, cfg, logger)

	logger.Info("starting beadle-email MCP server", "version", version, "config", configPath)

	if err := server.ServeStdio(s); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	return 0
}

func runDoctor(configPath string) int {
	type check struct {
		Name   string `json:"name"`
		Status string `json:"status"`
		Detail string `json:"detail,omitempty"`
	}

	var checks []check

	// Check credential backends
	backends := secret.Available()
	checks = append(checks, check{"secret_backends", "OK", strings.Join(backends, ", ")})

	// Check config file
	cfg, err := email.LoadConfig(configPath)
	if err != nil {
		checks = append(checks, check{"config", "FAIL", err.Error()})
	} else {
		checks = append(checks, check{"config", "OK", configPath})

		// Check IMAP password
		if _, err := cfg.IMAPPassword(); err != nil {
			checks = append(checks, check{"imap_password", "FAIL", err.Error()})
		} else {
			checks = append(checks, check{"imap_password", "OK", ""})
		}

		// Check Resend API key
		if _, err := cfg.ResendAPIKey(); err != nil {
			checks = append(checks, check{"resend_api_key", "FAIL", err.Error()})
		} else {
			checks = append(checks, check{"resend_api_key", "OK", ""})
		}

		// Check GPG binary
		gpgPath, err := exec.LookPath(cfg.GPGBinary)
		if err != nil {
			checks = append(checks, check{"gpg", "FAIL", fmt.Sprintf("%s not found", cfg.GPGBinary)})
		} else {
			checks = append(checks, check{"gpg", "OK", gpgPath})
		}

		// Check GPG signing key
		gpgKeyCmd := exec.Command(cfg.GPGBinary, "--list-keys", cfg.GPGSigner)
		if err := gpgKeyCmd.Run(); err != nil {
			checks = append(checks, check{"gpg_signing_key", "FAIL", fmt.Sprintf("no key for %s", cfg.GPGSigner)})
		} else {
			checks = append(checks, check{"gpg_signing_key", "OK", cfg.GPGSigner})
		}

		// Check GPG passphrase
		if _, err := cfg.GPGPassphrase(); err != nil {
			checks = append(checks, check{"gpg_passphrase", "FAIL", err.Error()})
		} else {
			checks = append(checks, check{"gpg_passphrase", "OK", ""})
		}

		// Check Proton Bridge SMTP
		if email.SMTPAvailable(cfg) {
			checks = append(checks, check{"smtp", "OK", fmt.Sprintf("%s:%d", cfg.IMAPHost, cfg.SMTPPort)})
		} else {
			checks = append(checks, check{"smtp", "WARN", fmt.Sprintf("Proton Bridge SMTP not reachable at %s:%d — will use Resend fallback", cfg.IMAPHost, cfg.SMTPPort)})
		}
	}

	failed := false
	for _, c := range checks {
		mark := "+"
		if c.Status == "FAIL" {
			mark = "!"
			failed = true
		}
		if c.Detail != "" {
			fmt.Printf("[%s] %-16s %s\n", mark, c.Name, c.Detail)
		} else {
			fmt.Printf("[%s] %s\n", mark, c.Name)
		}
	}

	if failed {
		return 1
	}
	return 0
}

func runStatus(configPath string) int {
	cfg, err := email.LoadConfig(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	status := map[string]string{
		"version":      version,
		"imap_host":    cfg.IMAPHost,
		"imap_port":    fmt.Sprintf("%d", cfg.IMAPPort),
		"imap_user":    cfg.IMAPUser,
		"smtp_port":    fmt.Sprintf("%d", cfg.SMTPPort),
		"from_address": cfg.FromAddress,
		"gpg_binary":   cfg.GPGBinary,
		"gpg_signer":   cfg.GPGSigner,
		"config":       configPath,
	}

	data, _ := json.MarshalIndent(status, "", "  ")
	fmt.Println(string(data))
	return 0
}

func logLevel() slog.Level {
	if os.Getenv("BEADLE_DEBUG") != "" {
		return slog.LevelDebug
	}
	return slog.LevelInfo
}
