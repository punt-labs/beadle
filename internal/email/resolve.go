package email

import (
	"fmt"
	"strings"

	"github.com/punt-labs/beadle/internal/contacts"
)

// LoadContactsIfNeeded loads the contacts store only if any token across
// the given address fields lacks @. Returns nil store (no error) when no
// resolution is needed. This ensures a corrupted contacts file does not
// break sending to raw email addresses.
func LoadContactsIfNeeded(contactsPath string, fields ...string) (*contacts.Store, error) {
	for _, raw := range fields {
		for _, tok := range strings.Split(raw, ",") {
			if t := strings.TrimSpace(tok); t != "" && !strings.Contains(t, "@") {
				s := contacts.NewStore(contactsPath)
				if err := s.Load(); err != nil {
					return nil, err
				}
				return s, nil
			}
		}
	}
	return nil, nil
}

// ResolveField resolves names in a comma-separated address string using
// a pre-loaded contacts store. If store is nil, returns raw unchanged.
func ResolveField(store *contacts.Store, storeErr error, raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	if store == nil {
		if storeErr != nil {
			return "", fmt.Errorf("load contacts: %w", storeErr)
		}
		return raw, nil
	}
	return store.ResolveAddresses(raw)
}
