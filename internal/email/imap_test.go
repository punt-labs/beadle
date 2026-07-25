package email

import (
	"testing"

	"github.com/emersion/go-imap/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRepoSearchCriteria covers the pure builder that decides whether a listing
// searches the server and how. It is the core of the repo-scoping filter, so it
// is an untagged unit test that runs under `make check`.
func TestRepoSearchCriteria(t *testing.T) {
	const slug = "punt-labs/beadle"

	t.Run("no filter returns nil", func(t *testing.T) {
		assert.Nil(t, repoSearchCriteria("", false))
	})

	t.Run("unread only, no repo", func(t *testing.T) {
		crit := repoSearchCriteria("", true)
		require.NotNil(t, crit)
		assert.Equal(t, []imap.Flag{imap.FlagSeen}, crit.NotFlag)
		assert.Empty(t, crit.Or)
	})

	t.Run("repo only", func(t *testing.T) {
		crit := repoSearchCriteria(slug, false)
		require.NotNil(t, crit)
		assert.Empty(t, crit.NotFlag)
		assertRepoOr(t, crit, slug)
	})

	t.Run("repo and unread compose", func(t *testing.T) {
		crit := repoSearchCriteria(slug, true)
		require.NotNil(t, crit)
		assert.Equal(t, []imap.Flag{imap.FlagSeen}, crit.NotFlag)
		assertRepoOr(t, crit, slug)
	})
}

// assertRepoOr asserts that crit carries exactly one OR pair whose first arm is
// the X-Beadle-Repo header and whose second arm is the "[slug]" subject tag.
func assertRepoOr(t *testing.T, crit *imap.SearchCriteria, slug string) {
	t.Helper()
	require.Len(t, crit.Or, 1)
	header := crit.Or[0][0]
	subject := crit.Or[0][1]
	require.Len(t, header.Header, 1)
	assert.Equal(t, HeaderRepo, header.Header[0].Key)
	assert.Equal(t, slug, header.Header[0].Value)
	require.Len(t, subject.Header, 1)
	assert.Equal(t, "Subject", subject.Header[0].Key)
	assert.Equal(t, "["+slug+"]", subject.Header[0].Value)
}

func TestRecencySet(t *testing.T) {
	tests := []struct {
		name        string
		numMessages uint32
		count       int
		wantStart   uint32
		wantStop    uint32
	}{
		{"count below total", 10, 3, 8, 10},
		{"count equals total", 5, 5, 1, 5},
		{"count above total", 4, 10, 1, 4},
		{"single message", 1, 1, 1, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			set := recencySet(tt.numMessages, tt.count)
			require.Len(t, set, 1)
			assert.Equal(t, tt.wantStart, set[0].Start)
			assert.Equal(t, tt.wantStop, set[0].Stop)
		})
	}
}

func TestLastN(t *testing.T) {
	uids := []imap.UID{1, 2, 3, 4, 5}
	tests := []struct {
		name  string
		in    []imap.UID
		count int
		want  []imap.UID
	}{
		{"keep last two", uids, 2, []imap.UID{4, 5}},
		{"count equals length", uids, 5, uids},
		{"count above length", uids, 10, uids},
		{"single", uids, 1, []imap.UID{5}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, lastN(tt.in, tt.count))
		})
	}
}
