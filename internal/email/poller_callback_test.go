package email_test

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/punt-labs/beadle/internal/email"
	"github.com/punt-labs/beadle/internal/testenv"
	"github.com/punt-labs/beadle/internal/testserver"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// allRepos scopes a poller to every repo, so tests that seed untagged mail
// count it regardless of the ambient git remote.
func allRepos() email.PollerOption {
	return email.WithRepoScope(func() string { return "" })
}

// scopedTo scopes a poller to a fixed repo slug.
func scopedTo(slug string) email.PollerOption {
	return email.WithRepoScope(func() string { return slug })
}

// writeConfigWithPoll writes email.json including poll_interval to the
// testenv identity directory.
func writeConfigWithPoll(t *testing.T, env *testenv.Env, cfg *email.Config) {
	t.Helper()
	data, err := json.MarshalIndent(map[string]any{
		"imap_host":     cfg.IMAPHost,
		"imap_port":     cfg.IMAPPort,
		"imap_user":     cfg.IMAPUser,
		"smtp_port":     cfg.SMTPPort,
		"from_address":  cfg.FromAddress,
		"poll_interval": "5m",
	}, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(env.IdentityDir(), "email.json"), data, 0o640))
}

func TestPoller_CallbackFiresOnNewMail(t *testing.T) {
	env := testenv.New(t, "test@test.com")
	fix := testserver.NewFixture(t)
	writeConfigWithPoll(t, env, fix.Config)
	dialer := testserver.TestDialer{Password: "testpass"}

	var callbackCount atomic.Uint32
	var receivedNewCount atomic.Uint32

	onNewMail := func(newCount uint32) {
		callbackCount.Add(1)
		receivedNewCount.Store(newCount)
	}

	p := email.NewPoller(onNewMail, env.Resolver, discardLogger(), dialer, allRepos())

	// Start triggers an immediate first poll (baseline).
	before := time.Now()
	require.NoError(t, p.Start())
	waitForPollAfter(t, p, before)
	assert.Equal(t, uint32(0), callbackCount.Load(), "callback must not fire on first poll")
	p.Stop()

	// Add 2 unseen messages.
	fix.AddMessage("INBOX", "alice@test.com", "Hello", "body 1")
	fix.AddMessage("INBOX", "bob@test.com", "World", "body 2")

	// Restart: immediate poll detects 2 new unseen.
	before = time.Now()
	require.NoError(t, p.Start())
	waitForPollAfter(t, p, before)
	assert.Equal(t, uint32(1), callbackCount.Load(), "callback must fire once")
	assert.Equal(t, uint32(2), receivedNewCount.Load(), "newCount must equal unseen delta")
	p.Stop()

	// Restart with no new messages: callback must not fire again.
	before = time.Now()
	require.NoError(t, p.Start())
	waitForPollAfter(t, p, before)
	assert.Equal(t, uint32(1), callbackCount.Load(), "callback must not fire when unseen unchanged")
	p.Stop()
}

func TestPoller_NilCallbackNoPanic(t *testing.T) {
	env := testenv.New(t, "test@test.com")
	fix := testserver.NewFixture(t)
	writeConfigWithPoll(t, env, fix.Config)
	dialer := testserver.TestDialer{Password: "testpass"}

	p := email.NewPoller(nil, env.Resolver, discardLogger(), dialer, allRepos())

	before := time.Now()
	require.NoError(t, p.Start())
	waitForPollAfter(t, p, before)
	p.Stop()

	fix.AddMessage("INBOX", "alice@test.com", "Hello", "body")

	before = time.Now()
	require.NoError(t, p.Start())
	waitForPollAfter(t, p, before)
	p.Stop()
}

// TestPoller_ScopedUnreadCount verifies the poller counts only the scoped
// repo's unread mail. The scope is injected, so the count does not depend on
// the ambient git remote.
func TestPoller_ScopedUnreadCount(t *testing.T) {
	env := testenv.New(t, "test@test.com")
	fix := testserver.NewFixture(t)
	writeConfigWithPoll(t, env, fix.Config)
	dialer := testserver.TestDialer{Password: "testpass"}

	// Two unread messages tagged for the scoped repo, two for another.
	fix.AddRawMessage("INBOX", taggedRaw("a@test.com", "punt-labs/beadle", "one"))
	fix.AddRawMessage("INBOX", taggedRaw("b@test.com", "punt-labs/beadle", "two"))
	fix.AddRawMessage("INBOX", taggedRaw("c@test.com", "punt-labs/other", "three"))
	fix.AddRawMessage("INBOX", taggedRaw("d@test.com", "punt-labs/other", "four"))

	p := email.NewPoller(nil, env.Resolver, discardLogger(), dialer, scopedTo("punt-labs/beadle"))
	before := time.Now()
	require.NoError(t, p.Start())
	waitForPollAfter(t, p, before)
	assert.Equal(t, uint32(2), p.Status().Unseen, "poller counts only the scoped repo's unread mail")
	p.Stop()
}

// TestPoller_DaemonScopeCountsUntaggedMail guards the daemon's poller
// configuration: owner command emails are untagged (no X-Beadle-Repo, no
// [slug]), so a repo-scoped count would never see them and OnNewMail would
// never fire. Configured as cmd/beadle-daemon builds it — WithRepoScope
// returning "" — the poller must count an untagged unread message. This fails
// against the CWD-scoped default, which resolves this repo's slug and filters
// the untagged message out.
func TestPoller_DaemonScopeCountsUntaggedMail(t *testing.T) {
	env := testenv.New(t, "test@test.com")
	fix := testserver.NewFixture(t)
	writeConfigWithPoll(t, env, fix.Config)
	dialer := testserver.TestDialer{Password: "testpass"}

	// An owner command email: plain, untagged, unread.
	fix.AddMessage("INBOX", "owner@example.com", "run the thing", "please")

	p := email.NewPoller(nil, env.Resolver, discardLogger(), dialer, allRepos())
	before := time.Now()
	require.NoError(t, p.Start())
	waitForPollAfter(t, p, before)
	assert.Equal(t, uint32(1), p.Status().Unseen, "unscoped daemon poller must count untagged owner mail")
	p.Stop()
}

// taggedRaw builds a raw RFC822 message carrying an X-Beadle-Repo header for
// slug. The message has no Seen flag when seeded via AddRawMessage.
func taggedRaw(from, slug, subject string) []byte {
	return []byte(fmt.Sprintf(
		"From: %s\r\n%s: %s\r\nSubject: %s\r\nDate: %s\r\nContent-Type: text/plain\r\n\r\nbody",
		from, email.HeaderRepo, slug, subject, time.Now().Format(time.RFC1123Z),
	))
}

// waitForPollAfter waits until the poller's LastCheck is strictly after
// the given time, indicating a new poll cycle completed.
func waitForPollAfter(t *testing.T, p *email.Poller, after time.Time) {
	t.Helper()
	for range 200 {
		st := p.Status()
		if !st.LastCheck.IsZero() && st.LastCheck.After(after) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("poller did not complete a poll within 2s")
}
