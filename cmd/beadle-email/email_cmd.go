package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/punt-labs/beadle/internal/email"
	"github.com/punt-labs/beadle/internal/identity"
	"github.com/punt-labs/beadle/internal/paths"
)

// --- Shared helpers ---

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

// resolveConfig loads email config using identity resolution, falling back to
// the explicit --config path. It also returns the resolved identity so callers
// (e.g. send, for repo tagging) need not resolve it a second time; the identity
// is nil when resolution itself fails.
func resolveConfig(explicitPath string) (*email.Config, *identity.Identity, error) {
	resolver, err := newResolver()
	if err != nil {
		slog.Warn("identity resolution unavailable, using explicit config", "error", err, "config", explicitPath)
		cfg, err := email.LoadConfig(explicitPath)
		return cfg, nil, err
	}
	id, err := resolver.Resolve()
	if err != nil {
		slog.Warn("identity resolution failed, using explicit config", "error", err, "config", explicitPath)
		cfg, err := email.LoadConfig(explicitPath)
		return cfg, nil, err
	}
	beadleDir, err := paths.DataDir()
	if err != nil {
		slog.Warn("data dir unavailable, using explicit config", "error", err, "config", explicitPath)
		cfg, err := email.LoadConfig(explicitPath)
		return cfg, id, err
	}
	idDir, err := identity.EnsureIdentityDir(beadleDir, id.Email)
	if err != nil {
		slog.Warn("identity dir unavailable, using explicit config", "error", err, "config", explicitPath)
		cfg, err := email.LoadConfig(explicitPath)
		return cfg, id, err
	}
	cfg, err := email.LoadConfig(filepath.Join(idDir, "email.json"))
	if err != nil {
		slog.Warn("identity config unavailable, using explicit config", "error", err, "config", explicitPath)
		cfg, err := email.LoadConfig(explicitPath)
		return cfg, id, err
	}
	return cfg, id, nil
}

// resolveContactsPath returns the identity-scoped contacts path, or the default.
func resolveContactsPath() string {
	resolver, err := newResolver()
	if err != nil {
		slog.Warn("identity resolution unavailable, using default contacts", "error", err)
		return defaultContactsPath()
	}
	id, err := resolver.Resolve()
	if err != nil {
		slog.Warn("identity resolution failed, using default contacts", "error", err)
		return defaultContactsPath()
	}
	beadleDir, err := paths.DataDir()
	if err != nil {
		slog.Warn("data dir unavailable, using default contacts", "error", err)
		return defaultContactsPath()
	}
	idDir, err := identity.EnsureIdentityDir(beadleDir, id.Email)
	if err != nil {
		slog.Warn("identity dir unavailable, using default contacts", "error", err)
		return defaultContactsPath()
	}
	return filepath.Join(idDir, "contacts.json")
}

func defaultContactsPath() string {
	beadleDir, err := paths.DataDir()
	if err != nil {
		slog.Error("data dir unavailable for default contacts", "error", err)
		return "contacts.json"
	}
	return filepath.Join(beadleDir, "contacts.json")
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

// --- list ---

var (
	listFolder   string
	listCount    int
	listOffset   int
	listUnread   bool
	listAllRepos bool
	listConfig   string
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List messages",
	Long:  "List messages from the inbox or a specified IMAP folder.",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, id, err := resolveConfig(listConfig)
		if err != nil {
			return err
		}
		logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: g.slogLevel()}))
		client, err := email.Dial(cfg, logger)
		if err != nil {
			return fmt.Errorf("connect: %w", err)
		}
		defer client.Close()

		// Scope to the current repo unless --all-repos is set. An empty slug
		// (no git remote) leaves the listing unfiltered.
		repoSlug := ""
		if !listAllRepos {
			agent := ""
			if id != nil {
				agent = id.Handle
			}
			repoSlug = email.ResolveRepoTag(cmd.Context(), logger, agent).Slug
		}

		if listOffset < 0 {
			return fmt.Errorf("--offset must be non-negative")
		}
		lr, err := client.SearchMessages(listFolder, email.SearchQuery{RepoSlug: repoSlug, UnreadOnly: listUnread}, listCount, listOffset)
		if err != nil {
			return fmt.Errorf("list messages: %w", err)
		}
		g.printResult(lr.Messages, func() {
			for _, m := range lr.Messages {
				unread := " "
				if m.Unread {
					unread = "*"
				}
				fmt.Printf("%s [%s] %s — %s (%s)\n", unread, m.ID, m.From, m.Subject, m.Date)
			}
		})
		return nil
	},
}

func init() {
	listCmd.Flags().StringVar(&listFolder, "folder", "INBOX", "IMAP folder")
	listCmd.Flags().IntVar(&listCount, "count", 10, "Maximum messages to return")
	listCmd.Flags().IntVar(&listOffset, "offset", 0, "Skip this many of the most recent messages")
	listCmd.Flags().BoolVar(&listUnread, "unread", false, "Show only unread messages")
	listCmd.Flags().BoolVar(&listAllRepos, "all-repos", false, "Always list mail from every repo (default: current repo when one resolves, otherwise all)")
	listCmd.Flags().StringVarP(&listConfig, "config", "c", email.DefaultConfigPath(), "Config file path")
}

// --- search ---

var (
	searchFolder   string
	searchFrom     string
	searchSubject  string
	searchSince    string
	searchText     string
	searchCount    int
	searchOffset   int
	searchUnread   bool
	searchAllRepos bool
	searchConfig   string
)

var searchCmd = &cobra.Command{
	Use:   "search",
	Short: "Search messages",
	Long: "Search a mailbox folder by sender, subject, date, or free text. " +
		"At least one of --from/--subject/--since/--text is required.",
	RunE: func(cmd *cobra.Command, args []string) error {
		q := email.SearchQuery{
			From:       searchFrom,
			Subject:    searchSubject,
			Text:       searchText,
			UnreadOnly: searchUnread,
		}
		if searchSince != "" {
			t, err := email.ParseSearchSince(searchSince)
			if err != nil {
				return err
			}
			q.Since = t
		}
		if q.From == "" && q.Subject == "" && q.Text == "" && q.Since.IsZero() {
			return fmt.Errorf("at least one of --from, --subject, --since, or --text is required")
		}
		if searchOffset < 0 {
			return fmt.Errorf("--offset must be non-negative")
		}

		cfg, id, err := resolveConfig(searchConfig)
		if err != nil {
			return err
		}
		logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: g.slogLevel()}))
		client, err := email.Dial(cfg, logger)
		if err != nil {
			return fmt.Errorf("connect: %w", err)
		}
		defer client.Close()

		// Scope to the current repo unless --all-repos is set.
		if !searchAllRepos {
			agent := ""
			if id != nil {
				agent = id.Handle
			}
			q.RepoSlug = email.ResolveRepoTag(cmd.Context(), logger, agent).Slug
		}

		lr, err := client.SearchMessages(searchFolder, q, searchCount, searchOffset)
		if err != nil {
			return fmt.Errorf("search messages: %w", err)
		}
		g.printResult(lr.Messages, func() {
			for _, m := range lr.Messages {
				unread := " "
				if m.Unread {
					unread = "*"
				}
				fmt.Printf("%s [%s] %s — %s (%s)\n", unread, m.ID, m.From, m.Subject, m.Date)
			}
		})
		return nil
	},
}

func init() {
	searchCmd.Flags().StringVar(&searchFolder, "folder", "INBOX", "IMAP folder")
	searchCmd.Flags().StringVar(&searchFrom, "from", "", "Sender substring")
	searchCmd.Flags().StringVar(&searchSubject, "subject", "", "Subject substring")
	searchCmd.Flags().StringVar(&searchSince, "since", "", "On/after date: RFC3339 or YYYY-MM-DD")
	searchCmd.Flags().StringVar(&searchText, "text", "", "Free text over the whole message")
	searchCmd.Flags().IntVar(&searchCount, "count", 10, "Maximum messages to return")
	searchCmd.Flags().IntVar(&searchOffset, "offset", 0, "Skip this many of the most recent matches")
	searchCmd.Flags().BoolVar(&searchUnread, "unread", false, "Show only unread messages")
	searchCmd.Flags().BoolVar(&searchAllRepos, "all-repos", false, "Search mail from every repo (default: current repo when one resolves)")
	searchCmd.Flags().StringVarP(&searchConfig, "config", "c", email.DefaultConfigPath(), "Config file path")
}

// --- read ---

var (
	readFolder string
	readConfig string
)

var readCmd = &cobra.Command{
	Use:   "read <uid>",
	Short: "Read a message",
	Long:  "Fetch and display a message by its UID.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		uidNum, err := strconv.ParseUint(args[0], 10, 32)
		if err != nil {
			return fmt.Errorf("invalid UID %q", args[0])
		}

		cfg, _, err := resolveConfig(readConfig)
		if err != nil {
			return err
		}
		logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: g.slogLevel()}))
		client, err := email.Dial(cfg, logger)
		if err != nil {
			return fmt.Errorf("connect: %w", err)
		}
		defer client.Close()

		msg, err := client.FetchMessage(readFolder, uint32(uidNum))
		if err != nil {
			return fmt.Errorf("read message: %w", err)
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
		return nil
	},
}

func init() {
	readCmd.Flags().StringVar(&readFolder, "folder", "INBOX", "IMAP folder")
	readCmd.Flags().StringVarP(&readConfig, "config", "c", email.DefaultConfigPath(), "Config file path")
}

// --- send ---

var (
	sendTo      string
	sendCc      string
	sendBcc     string
	sendSubject string
	sendBody    string
	sendConfig  string
)

var sendCmd = &cobra.Command{
	Use:   "send",
	Short: "Send an email",
	Long:  "Send an email via Proton Bridge SMTP or Resend API fallback.",
	RunE: func(cmd *cobra.Command, args []string) error {
		contactsPath := resolveContactsPath()
		store, storeErr := email.LoadContactsIfNeeded(contactsPath, sendTo, sendCc, sendBcc)
		toResolved, err := email.ResolveField(store, storeErr, sendTo)
		if err != nil {
			return fmt.Errorf("to: %w", err)
		}
		ccResolved, err := email.ResolveField(store, storeErr, sendCc)
		if err != nil {
			return fmt.Errorf("cc: %w", err)
		}
		bccResolved, err := email.ResolveField(store, storeErr, sendBcc)
		if err != nil {
			return fmt.Errorf("bcc: %w", err)
		}

		to := splitAddresses(toResolved)
		cc := splitAddresses(ccResolved)
		bcc := splitAddresses(bccResolved)

		if len(to) == 0 {
			return fmt.Errorf("at least one recipient is required")
		}

		cfg, id, err := resolveConfig(sendConfig)
		if err != nil {
			return err
		}
		logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: g.slogLevel()}))
		agent := ""
		if id != nil {
			agent = id.Handle
		}
		tag := email.ResolveRepoTag(cmd.Context(), logger, agent)
		result, err := email.TrySendChain(cfg, logger, to, cc, bcc, sendSubject, sendBody, "", nil, nil, tag)
		if err != nil {
			return fmt.Errorf("send: %w", err)
		}
		g.printResult(result, func() {
			fmt.Printf("sent to %s via %s\n", result.To, result.Method)
		})
		return nil
	},
}

func init() {
	sendCmd.Flags().StringVar(&sendTo, "to", "", "Recipient address (required)")
	sendCmd.Flags().StringVar(&sendCc, "cc", "", "CC address")
	sendCmd.Flags().StringVar(&sendBcc, "bcc", "", "BCC address")
	sendCmd.Flags().StringVar(&sendSubject, "subject", "", "Subject line (required)")
	sendCmd.Flags().StringVar(&sendBody, "body", "", "Message body (required)")
	sendCmd.Flags().StringVarP(&sendConfig, "config", "c", email.DefaultConfigPath(), "Config file path")
	_ = sendCmd.MarkFlagRequired("to")
	_ = sendCmd.MarkFlagRequired("subject")
	_ = sendCmd.MarkFlagRequired("body")
}

// --- move ---

var (
	moveFolder string
	moveDest   string
	moveConfig string
)

var moveCmd = &cobra.Command{
	Use:   "move <uid>",
	Short: "Move a message",
	Long:  "Move a message to a different IMAP folder.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		uidNum, err := strconv.ParseUint(args[0], 10, 32)
		if err != nil {
			return fmt.Errorf("invalid UID %q", args[0])
		}

		cfg, _, err := resolveConfig(moveConfig)
		if err != nil {
			return err
		}
		logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: g.slogLevel()}))
		client, err := email.Dial(cfg, logger)
		if err != nil {
			return fmt.Errorf("connect: %w", err)
		}
		defer client.Close()

		moved, err := client.MoveMessage(moveFolder, uint32(uidNum), moveDest)
		if err != nil {
			return fmt.Errorf("move: %w", err)
		}
		if moved == 0 {
			result := map[string]any{"status": "not_found", "uid": args[0], "source": moveFolder, "moved": 0}
			g.printResult(result, func() {
				fmt.Printf("%s not found in %s — not moved\n", args[0], moveFolder)
			})
			return nil
		}
		result := map[string]any{"status": "moved", "uid": args[0], "source": moveFolder, "destination": moveDest, "moved": moved}
		g.printResult(result, func() {
			fmt.Printf("moved %s from %s to %s\n", args[0], moveFolder, moveDest)
		})
		return nil
	},
}

func init() {
	moveCmd.Flags().StringVar(&moveFolder, "folder", "INBOX", "Source IMAP folder")
	moveCmd.Flags().StringVar(&moveDest, "to", "Archive", "Destination folder")
	moveCmd.Flags().StringVarP(&moveConfig, "config", "c", email.DefaultConfigPath(), "Config file path")
}

// --- mark ---

var (
	markFolder string
	markIDs    string
	markUnread bool
	markConfig string
)

var markCmd = &cobra.Command{
	Use:   "mark [uid]",
	Short: "Mark messages read or unread",
	Long: "Mark one or more messages read (default) or unread (--unread). " +
		"Pass a single UID as an argument, or a comma-separated list with --ids.",
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ids := splitAddresses(markIDs)
		if len(args) == 1 {
			ids = append(ids, args[0])
		}
		if len(ids) == 0 {
			return fmt.Errorf("a UID argument or --ids is required")
		}

		// Parse UIDs up front so an invalid id is reported before connecting.
		uids := make([]uint32, 0, len(ids))
		for _, id := range ids {
			n, err := strconv.ParseUint(id, 10, 32)
			if err != nil || n == 0 {
				return fmt.Errorf("invalid UID %q", id)
			}
			uids = append(uids, uint32(n))
		}

		cfg, _, err := resolveConfig(markConfig)
		if err != nil {
			return err
		}
		logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: g.slogLevel()}))
		client, err := email.Dial(cfg, logger)
		if err != nil {
			return fmt.Errorf("connect: %w", err)
		}
		defer client.Close()

		seen := !markUnread
		modified, err := client.SetSeenBatch(markFolder, uids, seen)
		if err != nil {
			return fmt.Errorf("mark: %w", err)
		}
		state := "read"
		if markUnread {
			state = "unread"
		}
		requested := len(uids)
		result := map[string]any{"status": "marked", "state": state, "modified": modified, "requested": requested}
		g.printResult(result, func() {
			if modified < requested {
				fmt.Printf("marked %d of %d message(s) %s (%d not found)\n", modified, requested, state, requested-modified)
				return
			}
			fmt.Printf("marked %d message(s) %s\n", modified, state)
		})
		return nil
	},
}

func init() {
	markCmd.Flags().StringVar(&markFolder, "folder", "INBOX", "IMAP folder")
	markCmd.Flags().StringVar(&markIDs, "ids", "", "Comma-separated UIDs to mark")
	markCmd.Flags().BoolVar(&markUnread, "unread", false, "Mark unread instead of read")
	markCmd.Flags().StringVarP(&markConfig, "config", "c", email.DefaultConfigPath(), "Config file path")
}

// --- folders ---

var foldersConfig string

var foldersCmd = &cobra.Command{
	Use:   "folders",
	Short: "List IMAP folders",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, _, err := resolveConfig(foldersConfig)
		if err != nil {
			return err
		}
		logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: g.slogLevel()}))
		client, err := email.Dial(cfg, logger)
		if err != nil {
			return fmt.Errorf("connect: %w", err)
		}
		defer client.Close()

		folders, err := client.ListFolders()
		if err != nil {
			return fmt.Errorf("list folders: %w", err)
		}
		g.printResult(folders, func() {
			for _, f := range folders {
				fmt.Println(f.Name)
			}
		})
		return nil
	},
}

func init() {
	foldersCmd.Flags().StringVarP(&foldersConfig, "config", "c", email.DefaultConfigPath(), "Config file path")
}
