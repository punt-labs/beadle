package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/punt-labs/beadle/internal/email"
	"github.com/punt-labs/beadle/internal/identity"
	"github.com/punt-labs/beadle/internal/paths"
)

// resolveConfig loads email config using identity resolution, falling back
// to the explicit --config path. CLI commands use this instead of
// email.LoadConfig directly.
func resolveConfig(explicitPath string) (*email.Config, error) {
	resolver, err := newResolver()
	if err != nil {
		return email.LoadConfig(explicitPath)
	}
	id, err := resolver.Resolve()
	if err != nil {
		return email.LoadConfig(explicitPath)
	}
	beadleDir, err := paths.DataDir()
	if err != nil {
		return email.LoadConfig(explicitPath)
	}
	idDir, err := identity.EnsureIdentityDir(beadleDir, id.Email)
	if err != nil {
		return email.LoadConfig(explicitPath)
	}
	cfg, err := email.LoadConfig(filepath.Join(idDir, "email.json"))
	if err != nil {
		return email.LoadConfig(explicitPath)
	}
	return cfg, nil
}

// resolveContactsPath returns the identity-scoped contacts path, or the default.
// Ensures the identity directory exists so callers can read/write contacts.
func resolveContactsPath() string {
	resolver, err := newResolver()
	if err != nil {
		return defaultContactsPath()
	}
	id, err := resolver.Resolve()
	if err != nil {
		return defaultContactsPath()
	}
	beadleDir, err := paths.DataDir()
	if err != nil {
		return defaultContactsPath()
	}
	idDir, err := identity.EnsureIdentityDir(beadleDir, id.Email)
	if err != nil {
		return defaultContactsPath()
	}
	return filepath.Join(idDir, "contacts.json")
}

func defaultContactsPath() string {
	return filepath.Join(paths.MustDataDir(), "contacts.json")
}

func runEmailList(g globalOpts, args []string) int {
	folder := "INBOX"
	count := 10
	unreadOnly := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--folder":
			if i+1 < len(args) {
				folder = args[i+1]
				i++
			}
		case "--count":
			if i+1 < len(args) {
				n, err := strconv.Atoi(args[i+1])
				if err == nil {
					count = n
				}
				i++
			}
		case "--unread":
			unreadOnly = true
		case "--config", "-c":
			i++ // handled by extractConfig
		default:
			if strings.HasPrefix(args[i], "--") {
				g.errorf("unknown flag %q", args[i])
				return 2
			}
		}
	}

	configPath := extractConfig(args)
	cfg, err := resolveConfig(configPath)
	if err != nil {
		g.errorf("%v", err)
		return 1
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: g.slogLevel()}))
	client, err := email.Dial(cfg, logger)
	if err != nil {
		g.errorf("connect: %v", err)
		return 1
	}
	defer client.Close()

	messages, err := client.ListMessages(folder, count, unreadOnly)
	if err != nil {
		g.errorf("list messages: %v", err)
		return 1
	}

	g.printResult(messages, func() {
		for _, m := range messages {
			unread := " "
			if m.Unread {
				unread = "*"
			}
			fmt.Printf("%s [%s] %s — %s (%s)\n", unread, m.ID, m.From, m.Subject, m.Date)
		}
	})
	return 0
}

func runEmailRead(g globalOpts, args []string) int {
	folder := "INBOX"
	var uid string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--folder":
			if i+1 < len(args) {
				folder = args[i+1]
				i++
			}
		case "--config", "-c":
			i++
		default:
			if !strings.HasPrefix(args[i], "--") {
				uid = args[i]
			} else {
				g.errorf("unknown flag %q", args[i])
				return 2
			}
		}
	}
	if uid == "" {
		g.errorf("message UID is required")
		return 2
	}

	uidNum, err := strconv.ParseUint(uid, 10, 32)
	if err != nil {
		g.errorf("invalid UID %q", uid)
		return 2
	}

	configPath := extractConfig(args)
	cfg, err := resolveConfig(configPath)
	if err != nil {
		g.errorf("%v", err)
		return 1
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: g.slogLevel()}))
	client, err := email.Dial(cfg, logger)
	if err != nil {
		g.errorf("connect: %v", err)
		return 1
	}
	defer client.Close()

	msg, err := client.FetchMessage(folder, uint32(uidNum))
	if err != nil {
		g.errorf("read message: %v", err)
		return 1
	}

	g.printResult(msg, func() {
		fmt.Printf("From: %s\n", msg.From)
		fmt.Printf("To: %s\n", msg.To)
		fmt.Printf("Date: %s\n", msg.Date)
		fmt.Printf("Subject: %s\n", msg.Subject)
		fmt.Printf("Trust: %s\n", msg.TrustLevel)
		fmt.Println()
		fmt.Println(msg.Body)
	})
	return 0
}

func runEmailSend(g globalOpts, args []string) int {
	var toRaw, ccRaw, bccRaw, subject, body string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--to":
			if i+1 < len(args) {
				toRaw = args[i+1]
				i++
			}
		case "--cc":
			if i+1 < len(args) {
				ccRaw = args[i+1]
				i++
			}
		case "--bcc":
			if i+1 < len(args) {
				bccRaw = args[i+1]
				i++
			}
		case "--subject":
			if i+1 < len(args) {
				subject = args[i+1]
				i++
			}
		case "--body":
			if i+1 < len(args) {
				body = args[i+1]
				i++
			}
		case "--config", "-c":
			i++
		default:
			if strings.HasPrefix(args[i], "--") {
				g.errorf("unknown flag %q", args[i])
				return 2
			}
		}
	}
	if toRaw == "" || subject == "" || body == "" {
		g.errorf("--to, --subject, and --body are required")
		return 2
	}

	// Resolve contact names using identity-scoped contacts
	contactsPath := resolveContactsPath()
	store, storeErr := email.LoadContactsIfNeeded(contactsPath, toRaw, ccRaw, bccRaw)
	toResolved, err := email.ResolveField(store, storeErr, toRaw)
	if err != nil {
		g.errorf("to: %v", err)
		return 1
	}
	ccResolved, err := email.ResolveField(store, storeErr, ccRaw)
	if err != nil {
		g.errorf("cc: %v", err)
		return 1
	}
	bccResolved, err := email.ResolveField(store, storeErr, bccRaw)
	if err != nil {
		g.errorf("bcc: %v", err)
		return 1
	}

	to := splitAddresses(toResolved)
	cc := splitAddresses(ccResolved)
	bcc := splitAddresses(bccResolved)

	if len(to) == 0 {
		g.errorf("at least one recipient is required")
		return 2
	}

	configPath := extractConfig(args)
	cfg, err := resolveConfig(configPath)
	if err != nil {
		g.errorf("%v", err)
		return 1
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: g.slogLevel()}))
	result, err := email.TrySendChain(cfg, logger, to, cc, bcc, subject, body, "", nil)
	if err != nil {
		g.errorf("send: %v", err)
		return 1
	}

	g.printResult(result, func() {
		fmt.Printf("sent to %s via %s\n", result.To, result.Method)
	})
	return 0
}

func runEmailMove(g globalOpts, args []string) int {
	folder := "INBOX"
	dest := "Archive"
	var uid string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--folder":
			if i+1 < len(args) {
				folder = args[i+1]
				i++
			}
		case "--to":
			if i+1 < len(args) {
				dest = args[i+1]
				i++
			}
		case "--config", "-c":
			i++
		default:
			if !strings.HasPrefix(args[i], "--") {
				uid = args[i]
			} else {
				g.errorf("unknown flag %q", args[i])
				return 2
			}
		}
	}
	if uid == "" {
		g.errorf("message UID is required")
		return 2
	}

	uidNum, err := strconv.ParseUint(uid, 10, 32)
	if err != nil {
		g.errorf("invalid UID %q", uid)
		return 2
	}

	configPath := extractConfig(args)
	cfg, err := resolveConfig(configPath)
	if err != nil {
		g.errorf("%v", err)
		return 1
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: g.slogLevel()}))
	client, err := email.Dial(cfg, logger)
	if err != nil {
		g.errorf("connect: %v", err)
		return 1
	}
	defer client.Close()

	if err := client.MoveMessage(folder, uint32(uidNum), dest); err != nil {
		g.errorf("move: %v", err)
		return 1
	}

	result := map[string]string{"status": "moved", "uid": uid, "source": folder, "destination": dest}
	g.printResult(result, func() {
		fmt.Printf("moved %s from %s to %s\n", uid, folder, dest)
	})
	return 0
}

func runEmailFolders(g globalOpts, args []string) int {
	configPath := extractConfig(args)
	cfg, err := resolveConfig(configPath)
	if err != nil {
		g.errorf("%v", err)
		return 1
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: g.slogLevel()}))
	client, err := email.Dial(cfg, logger)
	if err != nil {
		g.errorf("connect: %v", err)
		return 1
	}
	defer client.Close()

	folders, err := client.ListFolders()
	if err != nil {
		g.errorf("list folders: %v", err)
		return 1
	}

	g.printResult(folders, func() {
		for _, f := range folders {
			fmt.Println(f.Name)
		}
	})
	return 0
}

// splitAddresses splits a comma-separated address string.
func splitAddresses(s string) []string {
	if s == "" {
		return nil
	}
	var result []string
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			result = append(result, part)
		}
	}
	return result
}
