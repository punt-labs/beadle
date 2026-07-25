//go:build integration

package email_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/punt-labs/beadle/internal/email"
	"github.com/punt-labs/beadle/internal/testserver"
)

// rawDated builds a raw RFC822 message with an explicit From, Subject, and Date.
// The Date header uses RFC1123Z, which the testserver parses for SENTSINCE.
func rawDated(from, subject string, date time.Time) []byte {
	return []byte(fmt.Sprintf(
		"From: %s\r\nSubject: %s\r\nDate: %s\r\nContent-Type: text/plain\r\n\r\nbody",
		from, subject, date.Format(time.RFC1123Z),
	))
}

func TestSearchMessages_ByFrom(t *testing.T) {
	f := testserver.NewFixture(t)
	f.AddMessage("INBOX", "alice@test.com", "one", "body")
	f.AddMessage("INBOX", "bob@test.com", "two", "body")
	f.AddMessage("INBOX", "alice@test.com", "three", "body")

	client := dialFixture(t, f)
	lr, err := client.SearchMessages("INBOX", email.SearchQuery{From: "alice@test.com"}, 10, 0)
	require.NoError(t, err)

	assert.Equal(t, 2, lr.Total)
	assert.ElementsMatch(t, []string{"one", "three"}, subjects(lr))
}

func TestSearchMessages_BySubject(t *testing.T) {
	f := testserver.NewFixture(t)
	f.AddMessage("INBOX", "alice@test.com", "release plan", "body")
	f.AddMessage("INBOX", "bob@test.com", "lunch menu", "body")

	client := dialFixture(t, f)
	lr, err := client.SearchMessages("INBOX", email.SearchQuery{Subject: "release"}, 10, 0)
	require.NoError(t, err)

	assert.Equal(t, []string{"release plan"}, subjects(lr))
}

func TestSearchMessages_BySince(t *testing.T) {
	f := testserver.NewFixture(t)
	old := time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)
	recent := time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC)
	f.AddRawMessage("INBOX", rawDated("a@test.com", "old news", old))
	f.AddRawMessage("INBOX", rawDated("b@test.com", "fresh news", recent))

	client := dialFixture(t, f)
	cutoff := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	lr, err := client.SearchMessages("INBOX", email.SearchQuery{Since: cutoff}, 10, 0)
	require.NoError(t, err)

	assert.Equal(t, []string{"fresh news"}, subjects(lr))
}

func TestSearchMessages_ByText(t *testing.T) {
	f := testserver.NewFixture(t)
	f.AddMessage("INBOX", "a@test.com", "subject one", "the token beadle-6i0 is here")
	f.AddMessage("INBOX", "b@test.com", "subject two", "nothing relevant")

	client := dialFixture(t, f)
	lr, err := client.SearchMessages("INBOX", email.SearchQuery{Text: "beadle-6i0"}, 10, 0)
	require.NoError(t, err)

	assert.Equal(t, []string{"subject one"}, subjects(lr))
}

func TestSearchMessages_RepoScopeExcludesOtherRepos(t *testing.T) {
	f := testserver.NewFixture(t)
	f.AddRawMessage("INBOX", rawMsg("a@test.com", scopeSlug, "release beadle"))
	f.AddRawMessage("INBOX", rawMsg("b@test.com", "punt-labs/other", "release other"))

	client := dialFixture(t, f)

	scoped, err := client.SearchMessages("INBOX", email.SearchQuery{Subject: "release", RepoSlug: scopeSlug}, 10, 0)
	require.NoError(t, err)
	assert.Equal(t, []string{"release beadle"}, subjects(scoped))

	all, err := client.SearchMessages("INBOX", email.SearchQuery{Subject: "release"}, 10, 0)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"release beadle", "release other"}, subjects(all))
}

func TestSearchMessages_OffsetPaging(t *testing.T) {
	f := testserver.NewFixture(t)
	for i := range 30 {
		f.AddMessage("INBOX", "alice@test.com", fmt.Sprintf("match-%02d", i), "body")
	}

	client := dialFixture(t, f)

	// Walk pages of 10 from newest (offset 0) to oldest.
	for page, off := range []int{0, 10, 20} {
		lr, err := client.SearchMessages("INBOX", email.SearchQuery{From: "alice@test.com"}, 10, off)
		require.NoError(t, err)
		assert.Equal(t, 30, lr.Total, "Total counts every match on page %d", page)
		require.Len(t, lr.Messages, 10)

		want := make([]string, 0, 10)
		for i := 29 - off; i > 29-off-10; i-- {
			want = append(want, fmt.Sprintf("match-%02d", i))
		}
		assert.ElementsMatch(t, want, subjects(lr), "page %d window", page)
	}

	// A page past the end is empty but reports the true total.
	lr, err := client.SearchMessages("INBOX", email.SearchQuery{From: "alice@test.com"}, 10, 30)
	require.NoError(t, err)
	assert.Equal(t, 30, lr.Total)
	assert.Empty(t, lr.Messages)
}

// TestListMessages_Offset0NoSearch pins the fast path: a plain listing with no
// filter and offset 0 must not issue a SEARCH.
func TestListMessages_Offset0NoSearch(t *testing.T) {
	f := testserver.NewFixture(t)
	f.AddMessage("INBOX", "alice@test.com", "one", "body")

	var searches int
	f.SetSearchError(func(*imap.SearchCriteria) error {
		searches++
		return nil
	})

	client := dialFixture(t, f)
	lr, err := client.SearchMessages("INBOX", email.SearchQuery{}, 10, 0)
	require.NoError(t, err)
	assert.Len(t, lr.Messages, 1)
	assert.Equal(t, 0, searches, "offset-0 no-filter listing must not SEARCH")

	// offset > 0 forces a SEARCH for exact windowing.
	_, err = client.SearchMessages("INBOX", email.SearchQuery{}, 10, 1)
	require.NoError(t, err)
	assert.Positive(t, searches, "offset>0 must issue a SEARCH")
}

func TestSetSeen_RoundTrip(t *testing.T) {
	f := testserver.NewFixture(t)
	uid := f.AddMessage("INBOX", "alice@test.com", "mark me", "body")

	client := dialFixture(t, f)

	// Seeded unread.
	lr, err := client.ListMessages("INBOX", 10, true, "")
	require.NoError(t, err)
	assert.Equal(t, 1, lr.Total, "starts unread")

	// Mark read: it drops out of an unread listing.
	n, err := client.SetSeen("INBOX", uid, true)
	require.NoError(t, err)
	assert.Equal(t, 1, n, "one message marked read")
	lr, err = client.ListMessages("INBOX", 10, true, "")
	require.NoError(t, err)
	assert.Equal(t, 0, lr.Total, "read after SetSeen(true)")

	// Mark unread again: it returns.
	n, err = client.SetSeen("INBOX", uid, false)
	require.NoError(t, err)
	assert.Equal(t, 1, n, "one message marked unread")
	lr, err = client.ListMessages("INBOX", 10, true, "")
	require.NoError(t, err)
	assert.Equal(t, 1, lr.Total, "unread after SetSeen(false)")
}

func TestFetchMessage_DoesNotMarkSeen(t *testing.T) {
	f := testserver.NewFixture(t)
	uid := f.AddMessage("INBOX", "alice@test.com", "peek me", "body")

	client := dialFixture(t, f)

	_, err := client.FetchMessage("INBOX", uid)
	require.NoError(t, err)

	lr, err := client.ListMessages("INBOX", 10, true, "")
	require.NoError(t, err)
	assert.Equal(t, 1, lr.Total, "FetchMessage must not set \\Seen")
}

func TestSetSeenBatch_RoundTrip(t *testing.T) {
	f := testserver.NewFixture(t)
	uid1 := f.AddMessage("INBOX", "alice@test.com", "one", "body")
	uid2 := f.AddMessage("INBOX", "alice@test.com", "two", "body")
	uid3 := f.AddMessage("INBOX", "alice@test.com", "three", "body")

	client := dialFixture(t, f)

	// Mark two read, including one absent UID (silently ignored by the protocol).
	n, err := client.SetSeenBatch("INBOX", []uint32{uid1, uid2, 9999}, true)
	require.NoError(t, err)
	assert.Equal(t, 2, n, "two present UIDs modified, absent UID not counted")
	lr, err := client.ListMessages("INBOX", 10, true, "")
	require.NoError(t, err)
	assert.Equal(t, 1, lr.Total, "only the third stays unread")
	assert.Equal(t, []string{"three"}, subjects(lr))

	_ = uid3
}

func TestMoveMessages_CountsActualMoved(t *testing.T) {
	f := testserver.NewFixture(t)
	uid1 := f.AddMessage("INBOX", "alice@test.com", "one", "body")
	uid2 := f.AddMessage("INBOX", "alice@test.com", "two", "body")
	f.AddMessage("Archive", "system@test.com", "placeholder", "body")

	client := dialFixture(t, f)

	// Two present + one absent UID: only the present two move.
	moved, err := client.MoveMessages("INBOX", []uint32{uid1, uid2, 9999}, "Archive")
	require.NoError(t, err)
	assert.Equal(t, 2, moved, "absent UID is not counted as moved")

	// A move of an already-gone UID moves nothing.
	moved, err = client.MoveMessages("INBOX", []uint32{9999}, "Archive")
	require.NoError(t, err)
	assert.Equal(t, 0, moved, "a stale UID moves nothing")
}

// TestSearchMessages_SinceIsDateOnly proves SENTSINCE matches at DATE precision:
// a non-midnight Since (which ParseSearchSince accepts as RFC3339) still matches
// a message sent earlier in the same day.
func TestSearchMessages_SinceIsDateOnly(t *testing.T) {
	f := testserver.NewFixture(t)
	sameDayMorning := time.Date(2026, 7, 10, 6, 0, 0, 0, time.UTC)
	dayBefore := time.Date(2026, 7, 9, 23, 0, 0, 0, time.UTC)
	f.AddRawMessage("INBOX", rawDated("a@test.com", "same day", sameDayMorning))
	f.AddRawMessage("INBOX", rawDated("b@test.com", "day before", dayBefore))

	client := dialFixture(t, f)

	// Since is 2026-07-10 at 18:00 — later in the day than the 06:00 message,
	// yet the message still matches because SENTSINCE ignores the time of day.
	since := time.Date(2026, 7, 10, 18, 0, 0, 0, time.UTC)
	lr, err := client.SearchMessages("INBOX", email.SearchQuery{Since: since}, 10, 0)
	require.NoError(t, err)
	assert.Equal(t, []string{"same day"}, subjects(lr), "date-only match includes the same-day message, excludes the day before")
}
