package contacts

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testContacts = []Contact{
	{Name: "Alice Smith", Email: "alice@example.com", Aliases: []string{"alice", "asmith"}},
	{Name: "Alice Chen", Email: "alice.chen@example.com", Aliases: []string{"achen"}},
	{Name: "Kai", Email: "kai@example.com", GPGKeyID: "ABCD1234"},
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		contact Contact
		wantErr string
	}{
		{"valid", Contact{Name: "Sam", Email: "sam@test.com"}, ""},
		{"missing name", Contact{Email: "sam@test.com"}, "name is required"},
		{"blank name", Contact{Name: "  ", Email: "sam@test.com"}, "name is required"},
		{"missing email", Contact{Name: "Sam"}, "email is required"},
		{"no at sign", Contact{Name: "Sam", Email: "not-an-email"}, "must contain @"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Validate(tt.contact)
			if tt.wantErr == "" {
				assert.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			}
		})
	}
}

func TestFind(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  int
	}{
		{"by name", "Alice Smith", 1},
		{"by name case insensitive", "alice smith", 1},
		{"by alias", "alice", 1},
		{"by alias asmith", "asmith", 1},
		{"by email", "kai@example.com", 1},
		{"no match", "nobody", 0},
		{"empty query", "", 0},
		{"full name exact match", "Alice Smith", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matches := Find(testContacts, tt.query)
			assert.Len(t, matches, tt.want)
		})
	}
}

func TestResolveAddress(t *testing.T) {
	tests := []struct {
		name      string
		token     string
		wantEmail string
		wantErr   string
		wantN     int // expected number of matches on error
	}{
		{"email passthrough", "kai@example.com", "kai@example.com", "", 0},
		{"exact name", "Kai", "kai@example.com", "", 1},
		{"alias", "asmith", "alice@example.com", "", 1},
		{"case insensitive", "ASMITH", "alice@example.com", "", 1},
		{"no match", "nobody", "", "no contact matching", 0},
		{"empty", "", "", "empty address", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			email, matches, err := ResolveAddress(testContacts, tt.token)
			if tt.wantErr == "" {
				require.NoError(t, err)
				assert.Equal(t, tt.wantEmail, email)
			} else {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			}
			if tt.wantN > 0 {
				assert.Len(t, matches, tt.wantN)
			}
		})
	}
}

func TestResolveAddresses(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    string
		wantErr string
	}{
		{"single email", "kai@example.com", "kai@example.com", ""},
		{"single name", "Kai", "kai@example.com", ""},
		{"multiple mixed", "alice, kai@example.com", "alice@example.com,kai@example.com", ""},
		{"alias", "asmith,Kai", "alice@example.com,kai@example.com", ""},
		{"empty", "", "", ""},
		{"no match", "nobody", "", "no contact matching"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ResolveAddresses(testContacts, tt.raw)
			if tt.wantErr == "" {
				require.NoError(t, err)
				assert.Equal(t, tt.want, result)
			} else {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			}
		})
	}
}

func TestCheckNameConflict(t *testing.T) {
	tests := []struct {
		name    string
		contact Contact
		wantErr string
	}{
		{"no conflict", Contact{Name: "Bob", Email: "b@test.com"}, ""},
		{"name conflicts with existing name", Contact{Name: "alice smith", Email: "b@test.com"}, "conflicts with existing contact"},
		{"name conflicts with existing alias", Contact{Name: "asmith", Email: "b@test.com"}, "conflicts with existing contact"},
		{"alias conflicts with existing name", Contact{Name: "Bob", Email: "b@test.com", Aliases: []string{"kai"}}, "conflicts with existing contact"},
		{"alias conflicts with existing alias", Contact{Name: "Bob", Email: "b@test.com", Aliases: []string{"achen"}}, "conflicts with existing contact"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CheckNameConflict(testContacts, tt.contact)
			if tt.wantErr == "" {
				assert.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			}
		})
	}
}
