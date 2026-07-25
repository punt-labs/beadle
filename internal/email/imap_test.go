package email

import (
	"testing"
	"time"

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

func TestDedupUIDs(t *testing.T) {
	tests := []struct {
		name string
		in   []uint32
		want []uint32
	}{
		{"no dups unchanged", []uint32{1, 2, 3}, []uint32{1, 2, 3}},
		{"adjacent dup collapsed", []uint32{1, 1, 2}, []uint32{1, 2}},
		{"repeat preserves first-seen order", []uint32{3, 1, 3, 2, 1}, []uint32{3, 1, 2}},
		{"all identical", []uint32{7, 7, 7}, []uint32{7}},
		{"empty", []uint32{}, []uint32{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, DedupUIDs(tt.in))
		})
	}
}

func TestWindow(t *testing.T) {
	uids := []imap.UID{1, 2, 3, 4, 5}
	tests := []struct {
		name   string
		in     []imap.UID
		count  int
		offset int
		want   []imap.UID
	}{
		{"newest page, offset 0", uids, 2, 0, []imap.UID{4, 5}},
		{"offset 0 equals lastN", uids, 5, 0, uids},
		{"count above length", uids, 10, 0, uids},
		{"single newest", uids, 1, 0, []imap.UID{5}},
		{"mid page", uids, 2, 2, []imap.UID{2, 3}},
		{"count runs off the front", uids, 4, 3, []imap.UID{1, 2}},
		{"offset at end is empty", uids, 2, 5, nil},
		{"offset past end is empty", uids, 2, 9, nil},
		{"count zero is empty", uids, 0, 0, nil},
		{"empty input", []imap.UID{}, 3, 0, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, window(tt.in, tt.count, tt.offset))
		})
	}
}

// TestSearchCriteria covers the generalized builder: each field maps to the
// matching IMAP criterion, and combinations compose. Pure unit, runs under
// make check.
func TestSearchCriteria(t *testing.T) {
	const slug = "punt-labs/beadle"
	since := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)

	t.Run("zero query returns nil", func(t *testing.T) {
		assert.Nil(t, searchCriteria(SearchQuery{}))
	})

	t.Run("from maps to a From header", func(t *testing.T) {
		crit := searchCriteria(SearchQuery{From: "alice@x.com"})
		require.NotNil(t, crit)
		require.Len(t, crit.Header, 1)
		assert.Equal(t, "From", crit.Header[0].Key)
		assert.Equal(t, "alice@x.com", crit.Header[0].Value)
	})

	t.Run("subject maps to a Subject header", func(t *testing.T) {
		crit := searchCriteria(SearchQuery{Subject: "release"})
		require.NotNil(t, crit)
		require.Len(t, crit.Header, 1)
		assert.Equal(t, "Subject", crit.Header[0].Key)
		assert.Equal(t, "release", crit.Header[0].Value)
	})

	t.Run("since maps to SentSince", func(t *testing.T) {
		crit := searchCriteria(SearchQuery{Since: since})
		require.NotNil(t, crit)
		assert.Equal(t, since, crit.SentSince)
	})

	t.Run("text maps to Text", func(t *testing.T) {
		crit := searchCriteria(SearchQuery{Text: "beadle-6i0"})
		require.NotNil(t, crit)
		assert.Equal(t, []string{"beadle-6i0"}, crit.Text)
	})

	t.Run("all fields compose", func(t *testing.T) {
		crit := searchCriteria(SearchQuery{
			From: "alice@x.com", Subject: "release", Since: since,
			Text: "tok", RepoSlug: slug, UnreadOnly: true,
		})
		require.NotNil(t, crit)
		assert.Equal(t, []imap.Flag{imap.FlagSeen}, crit.NotFlag)
		assertRepoOr(t, crit, slug)
		assert.Equal(t, since, crit.SentSince)
		assert.Equal(t, []string{"tok"}, crit.Text)
		// From and Subject both land as top-level Header substrings.
		require.Len(t, crit.Header, 2)
		assert.Equal(t, "From", crit.Header[0].Key)
		assert.Equal(t, "Subject", crit.Header[1].Key)
	})
}

func TestParseSearchSince(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    time.Time
		wantErr bool
	}{
		{"date only", "2026-07-01", time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC), false},
		{"rfc3339", "2026-07-01T12:30:00Z", time.Date(2026, 7, 1, 12, 30, 0, 0, time.UTC), false},
		{"garbage", "last tuesday", time.Time{}, true},
		{"empty", "", time.Time{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseSearchSince(tt.in)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.True(t, got.Equal(tt.want), "got %v want %v", got, tt.want)
		})
	}
}
