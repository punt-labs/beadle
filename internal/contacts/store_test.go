package contacts

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mustAdd is a test helper that calls Add and fails the test on error.
func mustAdd(t *testing.T, s *Store, c Contact) {
	t.Helper()
	_, err := s.Add(c)
	require.NoError(t, err)
}

func TestDefaultPath(t *testing.T) {
	p := DefaultPath()
	assert.Contains(t, p, ".punt-labs")
	assert.Contains(t, p, "beadle")
	assert.Contains(t, p, "contacts.json")
}

func TestStore_LoadMissing(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "nonexistent.json"))
	require.NoError(t, s.Load())
	assert.Empty(t, s.Contacts())
	assert.Equal(t, 0, s.Count())
}

func TestStore_AddAndLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "contacts.json")
	s := NewStore(path)
	require.NoError(t, s.Load())

	normalized, err := s.Add(Contact{Name: "Jim", Email: "jim@test.com", Aliases: []string{"jmf"}})
	require.NoError(t, err)
	assert.Equal(t, "Jim", normalized.Name)
	assert.Equal(t, 1, s.Count())

	// Reload from disk
	s2 := NewStore(path)
	require.NoError(t, s2.Load())
	assert.Equal(t, 1, s2.Count())
	assert.Equal(t, "Jim", s2.Contacts()[0].Name)
	assert.Equal(t, "jim@test.com", s2.Contacts()[0].Email)
	assert.Equal(t, []string{"jmf"}, s2.Contacts()[0].Aliases)
}

func TestStore_AddNormalization(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "contacts.json"))
	require.NoError(t, s.Load())

	normalized, err := s.Add(Contact{Name: "  Jim  ", Email: " jim@test.com ", Aliases: []string{" jmf "}})
	require.NoError(t, err)
	assert.Equal(t, "Jim", normalized.Name)
	assert.Equal(t, "jim@test.com", normalized.Email)
	assert.Equal(t, []string{"jmf"}, normalized.Aliases)
}

func TestStore_AddValidation(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "contacts.json"))
	require.NoError(t, s.Load())

	_, err := s.Add(Contact{Name: "", Email: "jim@test.com"})
	assert.ErrorContains(t, err, "name is required")

	_, err = s.Add(Contact{Name: "Jim", Email: "not-email"})
	assert.ErrorContains(t, err, "must contain @")
}

func TestStore_AddDuplicate(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "contacts.json"))
	require.NoError(t, s.Load())

	mustAdd(t, s, Contact{Name: "Jim", Email: "jim@test.com", Aliases: []string{"jmf"}})

	// Duplicate name (case insensitive)
	_, err := s.Add(Contact{Name: "jim", Email: "other@test.com"})
	assert.ErrorContains(t, err, "conflicts with existing contact")

	// Alias conflicts with existing name
	_, err = s.Add(Contact{Name: "Other", Email: "other@test.com", Aliases: []string{"jim"}})
	assert.ErrorContains(t, err, "conflicts with existing contact")

	// Name conflicts with existing alias
	_, err = s.Add(Contact{Name: "jmf", Email: "other@test.com"})
	assert.ErrorContains(t, err, "conflicts with existing contact")
}

func TestStore_Remove(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "contacts.json"))
	require.NoError(t, s.Load())

	mustAdd(t, s, Contact{Name: "Jim", Email: "jim@test.com"})
	mustAdd(t, s, Contact{Name: "Kai", Email: "kai@test.com"})
	assert.Equal(t, 2, s.Count())

	require.NoError(t, s.Remove("jim")) // case insensitive
	assert.Equal(t, 1, s.Count())
	assert.Equal(t, "Kai", s.Contacts()[0].Name)

	err := s.Remove("nobody")
	assert.ErrorContains(t, err, "not found")
}

func TestStore_Find(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "contacts.json"))
	require.NoError(t, s.Load())

	mustAdd(t, s, Contact{Name: "Jim", Email: "jim@test.com", Aliases: []string{"jmf"}})
	mustAdd(t, s, Contact{Name: "Kai", Email: "kai@test.com"})

	assert.Len(t, s.Find("jim"), 1)
	assert.Len(t, s.Find("jmf"), 1)
	assert.Len(t, s.Find("kai"), 1)
	assert.Len(t, s.Find("nobody"), 0)
}

func TestStore_ResolveAddresses(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "contacts.json"))
	require.NoError(t, s.Load())

	mustAdd(t, s, Contact{Name: "Jim", Email: "jim@test.com"})
	mustAdd(t, s, Contact{Name: "Kai", Email: "kai@test.com"})

	resolved, err := s.ResolveAddresses("Jim,kai@test.com")
	require.NoError(t, err)
	assert.Equal(t, "jim@test.com,kai@test.com", resolved)
}

func TestStore_AtomicWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "contacts.json")
	s := NewStore(path)
	require.NoError(t, s.Load())

	mustAdd(t, s, Contact{Name: "Jim", Email: "jim@test.com"})

	// Verify the directory was created
	info, err := os.Stat(filepath.Dir(path))
	require.NoError(t, err)
	assert.True(t, info.IsDir())

	// Verify no temp file left behind
	_, err = os.Stat(path + ".tmp")
	assert.True(t, os.IsNotExist(err))
}
