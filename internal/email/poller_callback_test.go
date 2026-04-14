package email_test

import (
	"encoding/json"
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

	p := email.NewPoller(onNewMail, env.Resolver, discardLogger(), dialer)

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

	p := email.NewPoller(nil, env.Resolver, discardLogger(), dialer)

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
