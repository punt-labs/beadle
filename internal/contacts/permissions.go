package contacts

import (
	"fmt"
	"strings"
)

// Permission represents the rwx permission set for a (identity, contact) pair.
type Permission struct {
	Read    bool
	Write   bool
	Execute bool
}

// ParsePermission parses a permission string like "rwx", "rw-", "r--", "---".
// Each position is either the letter or '-'. Length must be exactly 3.
func ParsePermission(s string) (Permission, error) {
	if len(s) != 3 {
		return Permission{}, fmt.Errorf("permission string must be 3 characters, got %q", s)
	}
	s = strings.ToLower(s)

	var p Permission
	switch s[0] {
	case 'r':
		p.Read = true
	case '-':
	default:
		return Permission{}, fmt.Errorf("invalid read permission %q (expected 'r' or '-')", string(s[0]))
	}
	switch s[1] {
	case 'w':
		p.Write = true
	case '-':
	default:
		return Permission{}, fmt.Errorf("invalid write permission %q (expected 'w' or '-')", string(s[1]))
	}
	switch s[2] {
	case 'x':
		p.Execute = true
	case '-':
	default:
		return Permission{}, fmt.Errorf("invalid execute permission %q (expected 'x' or '-')", string(s[2]))
	}
	return p, nil
}

// String returns the canonical permission string (e.g., "rwx", "r--").
func (p Permission) String() string {
	var b [3]byte
	if p.Read {
		b[0] = 'r'
	} else {
		b[0] = '-'
	}
	if p.Write {
		b[1] = 'w'
	} else {
		b[1] = '-'
	}
	if p.Execute {
		b[2] = 'x'
	} else {
		b[2] = '-'
	}
	return string(b[:])
}

// CheckPermission returns the effective permission for a contact given
// the active identity email.
//
// Rules:
//   - Explicit entry in contact.Permissions[identityEmail] is parsed.
//   - Default: r-- (read only).
func CheckPermission(c Contact, identityEmail string) Permission {
	// Explicit permission for this identity
	if c.Permissions != nil {
		if perm, ok := c.Permissions[strings.ToLower(identityEmail)]; ok {
			p, err := ParsePermission(perm)
			if err == nil {
				return p
			}
			// Malformed permission string — fall through to default
		}
	}

	// Default: read only
	return Permission{Read: true}
}
