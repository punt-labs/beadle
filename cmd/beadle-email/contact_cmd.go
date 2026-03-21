package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/punt-labs/beadle/internal/contacts"
)

// --- contact (parent) ---

var contactCmd = &cobra.Command{
	Use:   "contact",
	Short: "Manage contacts",
	Long:  "Manage the address book: list, add, remove, or search contacts.",
}

func init() {
	contactCmd.AddCommand(contactListCmd)
	contactCmd.AddCommand(contactAddCmd)
	contactCmd.AddCommand(contactRemoveCmd)
	contactCmd.AddCommand(contactFindCmd)
}

// --- contact list ---

var contactListContacts string

var contactListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all contacts",
	RunE: func(cmd *cobra.Command, args []string) error {
		path := contactListContacts
		if path == "" {
			path = resolveContactsPath()
		}
		store := contacts.NewStore(path)
		if err := store.Load(); err != nil {
			return err
		}
		g.printResult(store.Contacts(), func() {
			for _, c := range store.Contacts() {
				aliases := ""
				if len(c.Aliases) > 0 {
					aliases = " (" + strings.Join(c.Aliases, ", ") + ")"
				}
				fmt.Printf("  %s <%s>%s\n", c.Name, c.Email, aliases)
			}
		})
		return nil
	},
}

func init() {
	contactListCmd.Flags().StringVar(&contactListContacts, "contacts", "", "Contacts file path (default: identity-scoped)")
}

// --- contact add ---

var (
	contactAddName     string
	contactAddEmail    string
	contactAddAliases  []string
	contactAddGPGKeyID string
	contactAddNotes    string
	contactAddContacts string
)

var contactAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Add a new contact",
	RunE: func(cmd *cobra.Command, args []string) error {
		if contactAddName == "" || contactAddEmail == "" {
			return fmt.Errorf("--name and --email are required")
		}
		path := contactAddContacts
		if path == "" {
			path = resolveContactsPath()
		}
		store := contacts.NewStore(path)
		if err := store.Load(); err != nil {
			return err
		}
		c := contacts.Contact{
			Name:     contactAddName,
			Email:    contactAddEmail,
			Aliases:  contactAddAliases,
			GPGKeyID: contactAddGPGKeyID,
			Notes:    contactAddNotes,
		}
		normalized, err := store.Add(c)
		if err != nil {
			return err
		}
		g.printResult(normalized, func() {
			fmt.Printf("added %s <%s>\n", normalized.Name, normalized.Email)
		})
		return nil
	},
}

func init() {
	contactAddCmd.Flags().StringVar(&contactAddName, "name", "", "Contact name (required)")
	contactAddCmd.Flags().StringVar(&contactAddEmail, "email", "", "Email address (required)")
	contactAddCmd.Flags().StringSliceVar(&contactAddAliases, "alias", nil, "Alternate name (repeatable)")
	contactAddCmd.Flags().StringVar(&contactAddGPGKeyID, "gpg-key-id", "", "GPG key ID for signature verification")
	contactAddCmd.Flags().StringVar(&contactAddNotes, "notes", "", "Freeform notes")
	contactAddCmd.Flags().StringVar(&contactAddContacts, "contacts", "", "Contacts file path (default: identity-scoped)")
}

// --- contact remove ---

var contactRemoveContacts string

var contactRemoveCmd = &cobra.Command{
	Use:   "remove <name>",
	Short: "Remove a contact by name",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		path := contactRemoveContacts
		if path == "" {
			path = resolveContactsPath()
		}
		store := contacts.NewStore(path)
		if err := store.Load(); err != nil {
			return err
		}
		if err := store.Remove(name); err != nil {
			return err
		}
		result := map[string]string{"status": "removed", "name": name}
		g.printResult(result, func() {
			fmt.Printf("removed %s\n", name)
		})
		return nil
	},
}

func init() {
	contactRemoveCmd.Flags().StringVar(&contactRemoveContacts, "contacts", "", "Contacts file path (default: identity-scoped)")
}

// --- contact find ---

var contactFindContacts string

var contactFindCmd = &cobra.Command{
	Use:   "find <query>",
	Short: "Search contacts by query",
	Long:  "Search contacts by name, email, or aliases.",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		query := strings.Join(args, " ")
		path := contactFindContacts
		if path == "" {
			path = resolveContactsPath()
		}
		store := contacts.NewStore(path)
		if err := store.Load(); err != nil {
			return err
		}
		matches := store.Find(query)
		g.printResult(matches, func() {
			if len(matches) == 0 {
				fmt.Println("no matches")
				return
			}
			for _, c := range matches {
				fmt.Printf("  %s <%s>\n", c.Name, c.Email)
			}
		})
		return nil
	},
}

func init() {
	contactFindCmd.Flags().StringVar(&contactFindContacts, "contacts", "", "Contacts file path (default: identity-scoped)")
}
