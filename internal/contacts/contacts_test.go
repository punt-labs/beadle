package contacts

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testContacts = []Contact{
	{Name: "Jim Freeman", Email: "jim@punt-labs.com", Aliases: []string{"jim", "jmf"}},
	{Name: "Jim Chen", Email: "jim.chen@example.com", Aliases: []string{"jimchen"}},
	{Name: "Kai", Email: "kai@example.com", GPGKeyID: "ABCD1234"},
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		contact Contact
		wantErr string
	}{
		{"valid", Contact{Name: "Jim", Email: "jim@test.com"}, ""},
		{"missing name", Contact{Email: "jim@test.com"}, "name is required"},
		{"blank name", Contact{Name: "  ", Email: "jim@test.com"}, "name is required"},
		{"missing email", Contact{Name: "Jim"}, "email is required"},
		{"no at sign", Contact{Name: "Jim", Email: "not-an-email"}, "must contain @"},
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
		{"by name", "Jim Freeman", 1},
		{"by name case insensitive", "jim freeman", 1},
		{"by alias", "jim", 1},
		{"by alias jmf", "jmf", 1},
		{"by email", "kai@example.com", 1},
		{"no match", "nobody", 0},
		{"empty query", "", 0},
		{"full name exact match", "Jim Freeman", 1},
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
		{"alias", "jmf", "jim@punt-labs.com", "", 1},
		{"case insensitive", "JMF", "jim@punt-labs.com", "", 1},
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
		{"multiple mixed", "jim, kai@example.com", "jim@punt-labs.com,kai@example.com", ""},
		{"alias", "jmf,Kai", "jim@punt-labs.com,kai@example.com", ""},
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
		{"no conflict", Contact{Name: "Alice", Email: "a@test.com"}, ""},
		{"name conflicts with existing name", Contact{Name: "jim freeman", Email: "a@test.com"}, "conflicts with existing contact"},
		{"name conflicts with existing alias", Contact{Name: "jmf", Email: "a@test.com"}, "conflicts with existing contact"},
		{"alias conflicts with existing name", Contact{Name: "Alice", Email: "a@test.com", Aliases: []string{"kai"}}, "conflicts with existing contact"},
		{"alias conflicts with existing alias", Contact{Name: "Alice", Email: "a@test.com", Aliases: []string{"jimchen"}}, "conflicts with existing contact"},
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
