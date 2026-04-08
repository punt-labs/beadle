package contacts

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/punt-labs/beadle/internal/paths"
)

// DefaultPath returns ~/.punt-labs/beadle/contacts.json.
func DefaultPath() string {
	return filepath.Join(paths.MustDataDir(), "contacts.json")
}

// Store manages the contacts file on disk.
type Store struct {
	path     string
	contacts []Contact
}

// NewStore creates a Store for the given file path.
func NewStore(path string) *Store {
	return &Store{path: path}
}

// Path returns the file path this store reads from and writes to.
func (s *Store) Path() string {
	return s.path
}

// Load reads contacts from disk. A missing file is not an error —
// it produces an empty store.
func (s *Store) Load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			s.contacts = nil
			return nil
		}
		return fmt.Errorf("read contacts %s: %w", s.path, err)
	}
	var contacts []Contact
	if err := json.Unmarshal(data, &contacts); err != nil {
		return fmt.Errorf("parse contacts %s: %w", s.path, err)
	}
	s.contacts = contacts
	return nil
}

// Contacts returns a copy of the loaded contacts, sorted alphabetically
// by name (case-insensitive). Sorting at the storage layer means every
// caller — list_contacts MCP tool, contact list CLI subcommand — gets
// consistent, scannable output without each handler re-sorting.
func (s *Store) Contacts() []Contact {
	out := make([]Contact, len(s.contacts))
	copy(out, s.contacts)
	slices.SortFunc(out, func(a, b Contact) int {
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return out
}

// Count returns the number of loaded contacts.
func (s *Store) Count() int {
	return len(s.contacts)
}

// Add validates the contact, checks for name/alias conflicts, appends it,
// and writes the file atomically.
// Add validates the contact, checks for name/alias conflicts, appends it,
// and writes the file atomically. Returns the normalized contact (whitespace
// trimmed) so callers can use the persisted values in responses.
func (s *Store) Add(c Contact) (Contact, error) {
	// Normalize whitespace on input.
	c.Name = strings.TrimSpace(c.Name)
	c.Email = strings.TrimSpace(c.Email)
	var aliases []string
	for _, a := range c.Aliases {
		a = strings.TrimSpace(a)
		if a != "" {
			aliases = append(aliases, a)
		}
	}
	c.Aliases = aliases
	c.GPGKeyID = strings.TrimSpace(c.GPGKeyID)

	if err := Validate(c); err != nil {
		return c, err
	}
	if err := CheckNameConflict(s.contacts, c); err != nil {
		return c, err
	}
	s.contacts = append(s.contacts, c)
	return c, s.write()
}

// Remove deletes the contact with the given name (case-insensitive).
func (s *Store) Remove(name string) error {
	name = strings.TrimSpace(name)
	idx := -1
	for i, c := range s.contacts {
		if equalsIgnoreCase(c.Name, name) {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("contact %q not found", name)
	}
	s.contacts = append(s.contacts[:idx], s.contacts[idx+1:]...)
	if s.contacts == nil {
		s.contacts = []Contact{}
	}
	return s.write()
}

// Find delegates to the package-level Find using the loaded contacts.
func (s *Store) Find(query string) []Contact {
	return Find(s.contacts, query)
}

// ResolveAddresses delegates to the package-level ResolveAddresses.
func (s *Store) ResolveAddresses(raw string) (string, error) {
	return ResolveAddresses(s.contacts, raw)
}

// write serializes contacts to disk atomically via temp file + rename.
func (s *Store) write() error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("create contacts directory %s: %w", dir, err)
	}
	data, err := json.MarshalIndent(s.contacts, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal contacts: %w", err)
	}
	data = append(data, '\n')
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o640); err != nil {
		return fmt.Errorf("write contacts temp file: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename contacts file: %w", err)
	}
	return nil
}

func equalsIgnoreCase(a, b string) bool {
	return strings.EqualFold(a, b)
}
