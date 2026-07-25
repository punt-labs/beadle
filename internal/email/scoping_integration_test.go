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

const scopeSlug = "punt-labs/beadle"

// rawMsg builds a raw RFC822 message. A non-empty repoHeader adds the
// X-Beadle-Repo header; subject is used verbatim.
func rawMsg(from, repoHeader, subject string) []byte {
	header := ""
	if repoHeader != "" {
		header = fmt.Sprintf("%s: %s\r\n", email.HeaderRepo, repoHeader)
	}
	return []byte(fmt.Sprintf(
		"From: %s\r\n%sSubject: %s\r\nDate: %s\r\nContent-Type: text/plain\r\n\r\nbody",
		from, header, subject, time.Now().Format(time.RFC1123Z),
	))
}

// subjects returns the subject of every message in a list result.
func subjects(lr *email.ListResult) []string {
	out := make([]string, len(lr.Messages))
	for i, m := range lr.Messages {
		out[i] = m.Subject
	}
	return out
}

// TestListMessages_RepoScoped_FourClasses seeds header-only, subject-tag-only,
// both, and neither, and asserts the scoped listing returns the first three and
// never the fourth.
func TestListMessages_RepoScoped_FourClasses(t *testing.T) {
	f := testserver.NewFixture(t)
	f.AddRawMessage("INBOX", rawMsg("a@test.com", scopeSlug, "header only"))
	f.AddRawMessage("INBOX", rawMsg("b@test.com", "", "["+scopeSlug+"] subject only"))
	f.AddRawMessage("INBOX", rawMsg("c@test.com", scopeSlug, "["+scopeSlug+"] both"))
	f.AddRawMessage("INBOX", rawMsg("d@test.com", "", "neither repo"))

	client := dialFixture(t, f)
	lr, err := client.ListMessages("INBOX", 10, false, scopeSlug)
	require.NoError(t, err)

	assert.Equal(t, 3, lr.Total)
	got := subjects(lr)
	assert.Contains(t, got, "header only")
	assert.Contains(t, got, "["+scopeSlug+"] subject only")
	assert.Contains(t, got, "["+scopeSlug+"] both")
	assert.NotContains(t, got, "neither repo")
}

// TestListMessages_CountWindow proves the fix for the client-side-filter bug:
// with 30 matching and 30 non-matching messages interleaved and count=10, the
// listing returns the 10 most-recent MATCHING messages, not a client-side
// filter of the last 10 messages (which would return far fewer).
func TestListMessages_CountWindow(t *testing.T) {
	f := testserver.NewFixture(t)
	for i := range 30 {
		f.AddRawMessage("INBOX", rawMsg("x@test.com", "", fmt.Sprintf("other-%02d", i)))
		f.AddRawMessage("INBOX", rawMsg("y@test.com", scopeSlug, fmt.Sprintf("match-%02d", i)))
	}

	client := dialFixture(t, f)
	lr, err := client.ListMessages("INBOX", 10, false, scopeSlug)
	require.NoError(t, err)

	assert.Equal(t, 30, lr.Total, "Total counts every matching message")
	require.Len(t, lr.Messages, 10, "only count messages are fetched")

	want := make([]string, 0, 10)
	for i := 20; i < 30; i++ {
		want = append(want, fmt.Sprintf("match-%02d", i))
	}
	assert.ElementsMatch(t, want, subjects(lr), "the 10 most-recent matching messages")
}

// TestListMessages_UnreadScopeCompose asserts the unread and repo filters
// combine: only unread matching mail is returned.
func TestListMessages_UnreadScopeCompose(t *testing.T) {
	f := testserver.NewFixture(t)
	f.AddRawMessage("INBOX", rawMsg("a@test.com", scopeSlug, "unread match"))
	f.AddRawMessageWithFlags("INBOX", rawMsg("b@test.com", scopeSlug, "read match"), []imap.Flag{imap.FlagSeen})
	f.AddRawMessage("INBOX", rawMsg("c@test.com", "", "unread other"))

	client := dialFixture(t, f)
	lr, err := client.ListMessages("INBOX", 10, true, scopeSlug)
	require.NoError(t, err)

	assert.Equal(t, 1, lr.Total)
	assert.Equal(t, []string{"unread match"}, subjects(lr))
}

// TestListMessages_AllReposParity asserts that an empty slug lists the full
// recency window unchanged, matching the pre-scoping behavior.
func TestListMessages_AllReposParity(t *testing.T) {
	f := testserver.NewFixture(t)
	f.AddRawMessage("INBOX", rawMsg("a@test.com", scopeSlug, "beadle mail"))
	f.AddRawMessage("INBOX", rawMsg("b@test.com", "punt-labs/other", "other mail"))
	f.AddRawMessage("INBOX", rawMsg("c@test.com", "", "untagged mail"))

	client := dialFixture(t, f)
	lr, err := client.ListMessages("INBOX", 10, false, "")
	require.NoError(t, err)

	assert.Equal(t, 3, lr.Total)
	assert.Len(t, lr.Messages, 3)
}

// TestUnreadCount_RepoScoped covers the poller's counting path: a scoped count
// counts only unread matching mail, while an empty slug counts all unseen.
func TestUnreadCount_RepoScoped(t *testing.T) {
	f := testserver.NewFixture(t)
	f.AddRawMessage("INBOX", rawMsg("a@test.com", scopeSlug, "unread match one"))
	f.AddRawMessage("INBOX", rawMsg("b@test.com", "", "["+scopeSlug+"] unread match two"))
	f.AddRawMessageWithFlags("INBOX", rawMsg("c@test.com", scopeSlug, "read match"), []imap.Flag{imap.FlagSeen})
	f.AddRawMessage("INBOX", rawMsg("d@test.com", "punt-labs/other", "unread other"))

	client := dialFixture(t, f)

	scoped, err := client.UnreadCount("INBOX", scopeSlug)
	require.NoError(t, err)
	assert.Equal(t, uint32(2), scoped, "counts only unread matching mail")

	all, err := client.UnreadCount("INBOX", "")
	require.NoError(t, err)
	assert.Equal(t, uint32(3), all, "empty slug counts all unseen")
}

// TestListMessages_UnreadScope_SearchErrorKeepsUnread asserts that when a scoped
// unread listing's SEARCH fails, the fallback widens the repo scope but keeps
// the unread filter — it never surfaces read mail on a transient error.
func TestListMessages_UnreadScope_SearchErrorKeepsUnread(t *testing.T) {
	f := testserver.NewFixture(t)
	f.AddRawMessage("INBOX", rawMsg("a@test.com", scopeSlug, "unread beadle"))
	f.AddRawMessageWithFlags("INBOX", rawMsg("b@test.com", scopeSlug, "read beadle"), []imap.Flag{imap.FlagSeen})
	f.AddRawMessage("INBOX", rawMsg("c@test.com", "punt-labs/other", "unread other"))

	// Fail the scoped search — it carries the repo OR arms — while letting the
	// widened unread-all retry (NotFlag, no OR) succeed.
	f.SetSearchError(func(crit *imap.SearchCriteria) error {
		if len(crit.Or) > 0 {
			return fmt.Errorf("forced scoped search failure")
		}
		return nil
	})

	client := dialFixture(t, f)
	lr, err := client.ListMessages("INBOX", 10, true, scopeSlug)
	require.NoError(t, err)

	got := subjects(lr)
	assert.ElementsMatch(t, []string{"unread beadle", "unread other"}, got)
	assert.NotContains(t, got, "read beadle", "unread fallback must never surface read mail")
}

// TestListMessages_UnreadScope_SearchErrorRetryShowsAll asserts that when both
// the scoped search and the widened unread retry fail, the listing falls
// through to the recency window — the never-empty floor.
func TestListMessages_UnreadScope_SearchErrorRetryShowsAll(t *testing.T) {
	f := testserver.NewFixture(t)
	f.AddRawMessage("INBOX", rawMsg("a@test.com", scopeSlug, "unread beadle"))
	f.AddRawMessageWithFlags("INBOX", rawMsg("b@test.com", scopeSlug, "read beadle"), []imap.Flag{imap.FlagSeen})
	f.AddRawMessage("INBOX", rawMsg("c@test.com", "punt-labs/other", "unread other"))

	// Every search fails, including the widened retry.
	f.SetSearchError(func(*imap.SearchCriteria) error {
		return fmt.Errorf("forced search failure")
	})

	client := dialFixture(t, f)
	lr, err := client.ListMessages("INBOX", 10, true, scopeSlug)
	require.NoError(t, err)

	require.NotEmpty(t, lr.Messages, "error fallback must never return an empty list")
	assert.Len(t, lr.Messages, 3, "recency fallback shows all recent mail, read included")
}
