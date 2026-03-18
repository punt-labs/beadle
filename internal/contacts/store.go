package contacts

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DefaultPath returns ~/.punt-labs/beadle/contacts.json.
func DefaultPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".punt-labs", "beadle", "contacts.json")
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

// Contacts returns a copy of the loaded contacts.
func (s *Store) Contacts() []Contact {
	out := make([]Contact, len(s.contacts))
	copy(out, s.contacts)
	return out
}

// Count returns the number of loaded contacts.
func (s *Store) Count() int {
	return len(s.contacts)
}

// Add validates the contact, checks for name/alias conflicts, appends it,
// and writes the file atomically.
func (s *Store) Add(c Contact) error {
	if err := Validate(c); err != nil {
		return err
	}
	if err := CheckNameConflict(s.contacts, c); err != nil {
		return err
	}
	s.contacts = append(s.contacts, c)
	return s.write()
}

// Remove deletes the contact with the given name (case-insensitive).
func (s *Store) Remove(name string) error {
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
