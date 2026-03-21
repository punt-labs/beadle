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

// resolveConfig loads email config using identity resolution, falling back
// to the explicit --config path.
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
	listFolder string
	listCount  int
	listUnread bool
	listConfig string
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List messages",
	Long:  "List messages from the inbox or a specified IMAP folder.",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := resolveConfig(listConfig)
		if err != nil {
			return err
		}
		logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: g.slogLevel()}))
		client, err := email.Dial(cfg, logger)
		if err != nil {
			return fmt.Errorf("connect: %w", err)
		}
		defer client.Close()

		lr, err := client.ListMessages(listFolder, listCount, listUnread)
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
	listCmd.Flags().BoolVar(&listUnread, "unread", false, "Show only unread messages")
	listCmd.Flags().StringVarP(&listConfig, "config", "c", email.DefaultConfigPath(), "Config file path")
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

		cfg, err := resolveConfig(readConfig)
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
		if sendTo == "" || sendSubject == "" || sendBody == "" {
			return fmt.Errorf("--to, --subject, and --body are required")
		}

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

		cfg, err := resolveConfig(sendConfig)
		if err != nil {
			return err
		}
		logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: g.slogLevel()}))
		result, err := email.TrySendChain(cfg, logger, to, cc, bcc, sendSubject, sendBody, "", nil)
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

		cfg, err := resolveConfig(moveConfig)
		if err != nil {
			return err
		}
		logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: g.slogLevel()}))
		client, err := email.Dial(cfg, logger)
		if err != nil {
			return fmt.Errorf("connect: %w", err)
		}
		defer client.Close()

		if err := client.MoveMessage(moveFolder, uint32(uidNum), moveDest); err != nil {
			return fmt.Errorf("move: %w", err)
		}
		result := map[string]string{"status": "moved", "uid": args[0], "source": moveFolder, "destination": moveDest}
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

// --- folders ---

var foldersConfig string

var foldersCmd = &cobra.Command{
	Use:   "folders",
	Short: "List IMAP folders",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := resolveConfig(foldersConfig)
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
