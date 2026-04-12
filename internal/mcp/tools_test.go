package mcp

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/punt-labs/beadle/internal/contacts"
	"github.com/punt-labs/beadle/internal/identity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeRequest builds a CallToolRequest from the given arguments map.
func makeRequest(t *testing.T, args map[string]any) mcplib.CallToolRequest {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"params": map[string]any{
			"name":      "test",
			"arguments": args,
		},
	})
	require.NoError(t, err)
	var req mcplib.CallToolRequest
	require.NoError(t, json.Unmarshal(raw, &req))
	return req
}

func TestIntParam(t *testing.T) {
	tests := []struct {
		name     string
		args     map[string]any
		key      string
		fallback int
		want     int
		wantErr  string
	}{
		{
			name:     "missing key returns fallback",
			args:     map[string]any{},
			key:      "count",
			fallback: 10,
			want:     10,
		},
		{
			name:     "whole float64 returns int",
			args:     map[string]any{"count": float64(5)},
			key:      "count",
			fallback: 0,
			want:     5,
		},
		{
			name:    "fractional float64 returns error",
			args:    map[string]any{"count": float64(10.5)},
			key:     "count",
			fallback: 0,
			wantErr: "count",
		},
		{
			name:    "negative fractional float64 returns error",
			args:    map[string]any{"count": float64(-2.1)},
			key:     "count",
			fallback: 0,
			wantErr: "count",
		},
		{
			name:    "string value returns error",
			args:    map[string]any{"count": "five"},
			key:     "count",
			fallback: 0,
			wantErr: "count",
		},
		{
			name:    "float64 outside int32 range returns error",
			args:    map[string]any{"count": math.MaxFloat64},
			key:     "count",
			fallback: 0,
			wantErr: "out of int32 range",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := makeRequest(t, tc.args)
			got, err := intParam(req, tc.key, tc.fallback)
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestSenderPermission(t *testing.T) {
	// Create an empty store (nonexistent file, no contacts) to test unknown-sender defaults
	store := contacts.NewStore("/nonexistent/contacts.json")
	// Load returns nil error for missing file (empty store)
	_ = store.Load()

	// We can't use store.Add (writes to disk), so test via the permission
	// layer directly. senderPermission calls store.Find + CheckPermission.
	// With an empty store, all senders should get --- (no permissions).
	id := &identity.Identity{Email: "claude@punt-labs.com"}

	t.Run("unknown sender gets no permissions", func(t *testing.T) {
		perm, senderEmail := senderPermission(store, id, "stranger@example.com")
		assert.Equal(t, "---", perm.String())
		assert.Equal(t, "stranger@example.com", senderEmail)
	})

	t.Run("RFC 5322 from header", func(t *testing.T) {
		perm, senderEmail := senderPermission(store, id, "Stranger <stranger@example.com>")
		assert.Equal(t, "---", perm.String())
		assert.Equal(t, "stranger@example.com", senderEmail)
	})

	t.Run("empty from", func(t *testing.T) {
		perm, senderEmail := senderPermission(store, id, "")
		assert.Equal(t, "---", perm.String())
		assert.Equal(t, "", senderEmail)
	})
}

func TestSenderPermission_WithContacts(t *testing.T) {
	// Write a contacts.json to a temp file
	dir := t.TempDir()
	contactsPath := filepath.Join(dir, "contacts.json")
	data := `[
		{"name":"Sam","email":"sam@example.com","permissions":{"claude@punt-labs.com":"rwx"}},
		{"name":"Vendor","email":"vendor@example.com","permissions":{"claude@punt-labs.com":"r--"}}
	]`
	require.NoError(t, os.WriteFile(contactsPath, []byte(data), 0o600))

	store := contacts.NewStore(contactsPath)
	require.NoError(t, store.Load())
	id := &identity.Identity{Email: "claude@punt-labs.com"}

	t.Run("rwx contact has read", func(t *testing.T) {
		perm, _ := senderPermission(store, id, "sam@example.com")
		assert.True(t, perm.Read)
		assert.True(t, perm.Write)
	})

	t.Run("r-- contact has read but not write", func(t *testing.T) {
		perm, _ := senderPermission(store, id, "vendor@example.com")
		assert.True(t, perm.Read)
		assert.False(t, perm.Write)
	})

	t.Run("unknown contact has no permissions", func(t *testing.T) {
		perm, _ := senderPermission(store, id, "stranger@example.com")
		assert.False(t, perm.Read)
		assert.False(t, perm.Write)
	})
}

func TestSplitAddresses(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", "", nil},
		{"single", "a@b.com", []string{"a@b.com"}},
		{"two comma-separated", "a@b.com,c@d.com", []string{"a@b.com", "c@d.com"}},
		{"whitespace around commas", " a@b.com , c@d.com , e@f.com ", []string{"a@b.com", "c@d.com", "e@f.com"}},
		{"trailing comma", "a@b.com,", []string{"a@b.com"}},
		{"only commas", ",,", nil},
		{"spaces only", "  ", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := splitAddresses(tc.in)
			assert.Equal(t, tc.want, got)
		})
	}
}
