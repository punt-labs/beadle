// Package contacts provides address book storage and lookup for Beadle.
// Contact resolution enables name-based addressing: "/mail jim" resolves
// to the stored email address without a separate MCP roundtrip.
package contacts

import (
	"fmt"
	"strings"
)

// Contact represents a person in the address book.
type Contact struct {
	Name     string   `json:"name"`
	Email    string   `json:"email"`
	Aliases  []string `json:"aliases,omitempty"`
	GPGKeyID string   `json:"gpg_key_id,omitempty"`
	Notes    string   `json:"notes,omitempty"`
}

// Validate checks that required fields are present and well-formed.
func Validate(c Contact) error {
	if strings.TrimSpace(c.Name) == "" {
		return fmt.Errorf("name is required")
	}
	if strings.TrimSpace(c.Email) == "" {
		return fmt.Errorf("email is required")
	}
	if !strings.Contains(c.Email, "@") {
		return fmt.Errorf("email %q must contain @", c.Email)
	}
	return nil
}

// Find returns all contacts whose Name, Email, or any Alias matches
// the query (case-insensitive substring is NOT used — exact match only
// against Name, Email, and each Alias).
func Find(contacts []Contact, query string) []Contact {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return nil
	}
	var matches []Contact
	for _, c := range contacts {
		if matchesContact(c, q) {
			matches = append(matches, c)
		}
	}
	return matches
}

// matchesContact returns true if query matches the contact's name, email,
// or any alias (case-insensitive exact match).
func matchesContact(c Contact, query string) bool {
	if strings.ToLower(c.Name) == query {
		return true
	}
	if strings.ToLower(c.Email) == query {
		return true
	}
	for _, alias := range c.Aliases {
		if strings.ToLower(alias) == query {
			return true
		}
	}
	return false
}

// ResolveAddress resolves a single token to an email address.
// If the token contains @, it is returned unchanged (assumed to be an email).
// Otherwise, it is looked up in the contacts list.
// Returns the resolved email, the list of matches (for error reporting), and an error.
func ResolveAddress(contacts []Contact, token string) (string, []Contact, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return "", nil, fmt.Errorf("empty address")
	}
	if strings.Contains(token, "@") {
		return token, nil, nil
	}
	matches := Find(contacts, token)
	switch len(matches) {
	case 0:
		return "", nil, fmt.Errorf("no contact matching %q", token)
	case 1:
		return matches[0].Email, matches, nil
	default:
		return "", matches, fmt.Errorf("ambiguous contact %q: %s", token, formatMatches(matches))
	}
}

// ResolveAddresses resolves a comma-separated string of names/emails.
// Each token is resolved independently. Returns the resolved comma-separated
// email string, or an error on the first failure.
func ResolveAddresses(contacts []Contact, raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	tokens := strings.Split(raw, ",")
	resolved := make([]string, 0, len(tokens))
	for _, tok := range tokens {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		addr, _, err := ResolveAddress(contacts, tok)
		if err != nil {
			return "", err
		}
		resolved = append(resolved, addr)
	}
	return strings.Join(resolved, ","), nil
}

func formatMatches(matches []Contact) string {
	parts := make([]string, len(matches))
	for i, m := range matches {
		parts[i] = fmt.Sprintf("%s <%s>", m.Name, m.Email)
	}
	return strings.Join(parts, ", ")
}

// CheckNameConflict returns an error if the given contact's name or any
// alias conflicts with an existing contact's name or alias.
func CheckNameConflict(existing []Contact, c Contact) error {
	names := make(map[string]string) // lowercase token → owning contact name
	for _, e := range existing {
		names[strings.ToLower(e.Name)] = e.Name
		for _, a := range e.Aliases {
			names[strings.ToLower(a)] = e.Name
		}
	}
	if owner, ok := names[strings.ToLower(c.Name)]; ok {
		return fmt.Errorf("contact name %q conflicts with existing contact %q", c.Name, owner)
	}
	for _, a := range c.Aliases {
		if owner, ok := names[strings.ToLower(a)]; ok {
			return fmt.Errorf("alias %q conflicts with existing contact %q", a, owner)
		}
	}
	return nil
}
