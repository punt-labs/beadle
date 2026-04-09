package contacts

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContact_IsPattern(t *testing.T) {
	tests := []struct {
		name  string
		email string
		want  bool
	}{
		{"exact address", "alice@example.com", false},
		{"star local part", "*@mail.anthropic.com", true},
		{"star prefix", "no-reply-*@mail.anthropic.com", true},
		{"question mark", "a?b@example.com", true},
		{"empty", "", false},
		{"no at sign", "alice", false},
		{"star in domain", "alice@*.example.com", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := Contact{Name: "x", Email: tt.email}
			assert.Equal(t, tt.want, c.IsPattern())
		})
	}
}

func TestValidate_Patterns(t *testing.T) {
	tests := []struct {
		name    string
		contact Contact
		wantErr string
	}{
		{
			"pattern with r-- ok",
			Contact{Name: "Anthropic", Email: "*@mail.anthropic.com", Permissions: map[string]string{"claude@punt-labs.com": "r--"}},
			"",
		},
		{
			"pattern with rw- rejected",
			Contact{Name: "Anthropic", Email: "*@mail.anthropic.com", Permissions: map[string]string{"claude@punt-labs.com": "rw-"}},
			"pattern contacts may only grant read permission",
		},
		{
			"pattern with rwx rejected",
			Contact{Name: "Anthropic", Email: "*@mail.anthropic.com", Permissions: map[string]string{"claude@punt-labs.com": "rwx"}},
			"pattern contacts may only grant read permission",
		},
		{
			"pattern with r-x rejected",
			Contact{Name: "Anthropic", Email: "*@mail.anthropic.com", Permissions: map[string]string{"claude@punt-labs.com": "r-x"}},
			"pattern contacts may only grant read permission",
		},
		{
			"pattern with --- ok",
			Contact{Name: "Anthropic", Email: "*@mail.anthropic.com", Permissions: map[string]string{"claude@punt-labs.com": "---"}},
			"",
		},
		{
			"pattern with empty permissions ok",
			Contact{Name: "Anthropic", Email: "*@mail.anthropic.com"},
			"",
		},
		{
			"exact contact with rwx ok",
			Contact{Name: "Alice", Email: "alice@example.com", Permissions: map[string]string{"claude@punt-labs.com": "rwx"}},
			"",
		},
		{
			"pattern accepted by validate rule",
			Contact{Name: "Anthropic", Email: "no-reply-*@mail.anthropic.com", Permissions: map[string]string{"claude@punt-labs.com": "r--"}},
			"",
		},
		{
			"malformed pattern rejected",
			Contact{Name: "Broken", Email: "[abc*@example.com", Permissions: map[string]string{"claude@punt-labs.com": "r--"}},
			"invalid pattern syntax",
		},
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

func TestFindByAddress_ExactWinsOverPattern(t *testing.T) {
	store := newStoreFromList(t, []Contact{
		{Name: "Anthropic", Email: "*@mail.anthropic.com", Permissions: map[string]string{"claude@punt-labs.com": "r--"}},
		{Name: "Attacker", Email: "attacker@mail.anthropic.com", Permissions: map[string]string{"claude@punt-labs.com": "---"}},
	})

	c, ok := store.FindByAddress("attacker@mail.anthropic.com")
	require.True(t, ok)
	assert.Equal(t, "Attacker", c.Name)
	assert.Equal(t, "attacker@mail.anthropic.com", c.Email)
}

func TestFindByAddress_PatternMatch(t *testing.T) {
	store := newStoreFromList(t, []Contact{
		{Name: "Anthropic", Email: "*@mail.anthropic.com", Permissions: map[string]string{"claude@punt-labs.com": "r--"}},
	})

	c, ok := store.FindByAddress("no-reply-abc123@mail.anthropic.com")
	require.True(t, ok)
	assert.Equal(t, "Anthropic", c.Name)
}

func TestFindByAddress_LongestPatternWins(t *testing.T) {
	store := newStoreFromList(t, []Contact{
		{Name: "Vercel All", Email: "*@vercel.app", Permissions: map[string]string{"claude@punt-labs.com": "r--"}},
		{Name: "Vercel CI", Email: "*@ci.vercel.app", Permissions: map[string]string{"claude@punt-labs.com": "r--"}},
	})

	c, ok := store.FindByAddress("sam@ci.vercel.app")
	require.True(t, ok)
	assert.Equal(t, "Vercel CI", c.Name)

	// Other domain: matches the shorter pattern only.
	c, ok = store.FindByAddress("sam@vercel.app")
	require.True(t, ok)
	assert.Equal(t, "Vercel All", c.Name)
}

func TestFindByAddress_TiesBreakByStoreOrder(t *testing.T) {
	// Two patterns of equal length that both match; the first-added wins.
	store := newStoreFromList(t, []Contact{
		{Name: "First", Email: "*@example.com", Permissions: map[string]string{"claude@punt-labs.com": "r--"}},
		{Name: "Second", Email: "a*@example.com", Permissions: map[string]string{"claude@punt-labs.com": "r--"}},
	})
	// "a*@example.com" is longer (len 14 vs 13), so Second wins for "alice@example.com".
	c, ok := store.FindByAddress("alice@example.com")
	require.True(t, ok)
	assert.Equal(t, "Second", c.Name)
}

func TestFindByAddress_NoMatch(t *testing.T) {
	store := newStoreFromList(t, []Contact{
		{Name: "Alice", Email: "alice@example.com"},
	})

	_, ok := store.FindByAddress("stranger@evil.com")
	assert.False(t, ok)
}

func TestFindByAddress_CaseInsensitive(t *testing.T) {
	store := newStoreFromList(t, []Contact{
		{Name: "Anthropic", Email: "*@MAIL.ANTHROPIC.COM", Permissions: map[string]string{"claude@punt-labs.com": "r--"}},
	})

	c, ok := store.FindByAddress("no-reply@mail.anthropic.com")
	require.True(t, ok)
	assert.Equal(t, "Anthropic", c.Name)
}

func TestFindByAddress_EmptyStore(t *testing.T) {
	store := NewStore("/nonexistent/contacts.json")
	require.NoError(t, store.Load())

	_, ok := store.FindByAddress("anyone@example.com")
	assert.False(t, ok)
}

func TestFindByAddress_ExactBeatsLongerPattern(t *testing.T) {
	store := newStoreFromList(t, []Contact{
		{Name: "Pattern", Email: "*@example.com", Permissions: map[string]string{"claude@punt-labs.com": "r--"}},
		{Name: "Exact", Email: "alice@example.com", Permissions: map[string]string{"claude@punt-labs.com": "rwx"}},
	})

	c, ok := store.FindByAddress("alice@example.com")
	require.True(t, ok)
	assert.Equal(t, "Exact", c.Name)
}

// newStoreFromList builds an in-memory store by injecting the contacts
// directly, bypassing the disk write. Tests use this to avoid tempdir plumbing.
func newStoreFromList(t *testing.T, list []Contact) *Store {
	t.Helper()
	s := NewStore("/nonexistent/contacts.json")
	s.contacts = list
	return s
}
