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

	"github.com/punt-labs/beadle/internal/contacts"
	"github.com/punt-labs/beadle/internal/email"
	mcptools "github.com/punt-labs/beadle/internal/mcp"
	"github.com/punt-labs/beadle/internal/secret"
)

// version is set at build time via -ldflags="-X main.version=..."
// Defaults to "dev" for local builds without ldflags.
var version = "dev"

const usage = `beadle-email: Beadle email channel MCP server

Usage:
  beadle-email serve [--config PATH]              Start MCP server (stdio transport)
  beadle-email version                            Print version and exit
  beadle-email doctor [--config PATH]             Check installation health
  beadle-email status [--config PATH]             Show current state
  beadle-email contact list [--contacts PATH]     List all contacts
  beadle-email contact add [flags]                Add a contact
  beadle-email contact remove <name>              Remove a contact
  beadle-email contact find <query>               Find contacts by name/alias/email
  beadle-email --help                             Show this help

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

	case "contact":
		return runContact(args[1:])

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

	mcptools.RegisterTools(s, cfg, contacts.DefaultPath(), logger)

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

	// Check contacts file
	contactsPath := contacts.DefaultPath()
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

	contactsPath := contacts.DefaultPath()
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
		"smtp_port":      fmt.Sprintf("%d", cfg.SMTPPort),
		"from_address":   cfg.FromAddress,
		"gpg_binary":     cfg.GPGBinary,
		"gpg_signer":     cfg.GPGSigner,
		"config":         configPath,
		"contacts_path":  contactsPath,
		"contacts_count": contactsCount,
	}
	if contactsError != "" {
		status["contacts_error"] = contactsError
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

// --- Contact CLI ---

const contactUsage = `Usage:
  beadle-email contact list [--contacts PATH]
  beadle-email contact add --name NAME --email EMAIL [--alias ALIAS]... [--gpg-key-id KEY] [--notes TEXT] [--contacts PATH]
  beadle-email contact remove NAME [--contacts PATH]
  beadle-email contact find QUERY [--contacts PATH]
`

func extractContactsPath(args []string) string {
	for i := 0; i < len(args); i++ {
		if args[i] == "--contacts" {
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "--") {
				fmt.Fprintln(os.Stderr, "error: --contacts requires a path")
				os.Exit(2)
			}
			return args[i+1]
		}
	}
	return contacts.DefaultPath()
}

func runContact(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, contactUsage)
		return 2
	}
	path := extractContactsPath(args[1:])
	switch args[0] {
	case "list":
		return runContactList(path)
	case "add":
		return runContactAdd(args[1:], path)
	case "remove":
		return runContactRemove(args[1:], path)
	case "find":
		return runContactFind(args[1:], path)
	default:
		fmt.Fprintf(os.Stderr, "error: unknown contact command %q\n\n%s", args[0], contactUsage)
		return 2
	}
}

func runContactList(path string) int {
	store := contacts.NewStore(path)
	if err := store.Load(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	data, _ := json.MarshalIndent(store.Contacts(), "", "  ")
	fmt.Println(string(data))
	return 0
}

func runContactAdd(args []string, path string) int {
	var name, addr, gpgKeyID, notes string
	var aliases []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--name":
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "--") {
				fmt.Fprintln(os.Stderr, "error: --name requires a value")
				return 2
			}
			name = args[i+1]
			i++
		case "--email":
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "--") {
				fmt.Fprintln(os.Stderr, "error: --email requires a value")
				return 2
			}
			addr = args[i+1]
			i++
		case "--alias":
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "--") {
				fmt.Fprintln(os.Stderr, "error: --alias requires a value")
				return 2
			}
			aliases = append(aliases, args[i+1])
			i++
		case "--gpg-key-id":
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "--") {
				fmt.Fprintln(os.Stderr, "error: --gpg-key-id requires a value")
				return 2
			}
			gpgKeyID = args[i+1]
			i++
		case "--notes":
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "--") {
				fmt.Fprintln(os.Stderr, "error: --notes requires a value")
				return 2
			}
			notes = args[i+1]
			i++
		case "--contacts":
			i++ // skip value, already handled by extractContactsPath
		default:
			fmt.Fprintf(os.Stderr, "error: unexpected argument %q\n", args[i])
			return 2
		}
	}
	if name == "" || addr == "" {
		fmt.Fprintln(os.Stderr, "error: --name and --email are required")
		return 2
	}
	store := contacts.NewStore(path)
	if err := store.Load(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	c := contacts.Contact{
		Name:     name,
		Email:    addr,
		Aliases:  aliases,
		GPGKeyID: gpgKeyID,
		Notes:    notes,
	}
	if err := store.Add(c); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	data, _ := json.MarshalIndent(c, "", "  ")
	fmt.Println(string(data))
	return 0
}

func runContactRemove(args []string, path string) int {
	name := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--contacts":
			i++ // skip value
		default:
			if !strings.HasPrefix(args[i], "--") {
				if name != "" {
					fmt.Fprintf(os.Stderr, "error: unexpected argument %q (use quotes for multi-word names)\n", args[i])
					return 2
				}
				name = args[i]
			}
		}
	}
	if name == "" {
		fmt.Fprintln(os.Stderr, "error: contact name is required")
		return 2
	}
	store := contacts.NewStore(path)
	if err := store.Load(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if err := store.Remove(name); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	result := map[string]string{"status": "removed", "name": name}
	data, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(data))
	return 0
}

func runContactFind(args []string, path string) int {
	// Collect non-flag args as the query
	var parts []string
	for i := 0; i < len(args); i++ {
		if args[i] == "--contacts" {
			i++ // skip
			continue
		}
		if !strings.HasPrefix(args[i], "--") {
			parts = append(parts, args[i])
		}
	}
	query := strings.Join(parts, " ")
	if query == "" {
		fmt.Fprintln(os.Stderr, "error: query is required")
		return 2
	}
	store := contacts.NewStore(path)
	if err := store.Load(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	matches := store.Find(query)
	data, _ := json.MarshalIndent(matches, "", "  ")
	fmt.Println(string(data))
	return 0
}
