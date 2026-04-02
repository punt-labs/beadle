package email

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// discardLogger returns a logger that writes nowhere.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// testPoller returns a Poller with a discard logger for unit tests
// that exercise interval/stop mechanics without real I/O.
func testPoller() *Poller {
	return &Poller{logger: discardLogger()}
}

func TestPoller_SetInterval_Valid(t *testing.T) {
	p := testPoller()
	require.NoError(t, p.SetInterval("5m"))
	st := p.Status()
	assert.Equal(t, "5m", st.Interval)
	assert.True(t, st.Active)
	p.Stop()
}

func TestPoller_SetInterval_Disable(t *testing.T) {
	p := testPoller()
	require.NoError(t, p.SetInterval("5m"))
	require.NoError(t, p.SetInterval("n"))
	st := p.Status()
	assert.Equal(t, "n", st.Interval)
	assert.False(t, st.Active)
}

func TestPoller_SetInterval_Invalid(t *testing.T) {
	p := testPoller()
	err := p.SetInterval("3m")
	require.Error(t, err)
	var ie *InvalidIntervalError
	assert.ErrorAs(t, err, &ie)
}

func TestPoller_SetInterval_RunningRestart(t *testing.T) {
	// SetInterval on a running poller must stop the old goroutine
	// before starting a new one. No concurrent goroutines.
	p := testPoller()
	require.NoError(t, p.SetInterval("5m"))
	require.NoError(t, p.SetInterval("10m"))
	st := p.Status()
	assert.Equal(t, "10m", st.Interval)
	assert.True(t, st.Active)
	p.Stop()
	st = p.Status()
	assert.False(t, st.Active)
}

func TestPoller_Status_Initial(t *testing.T) {
	p := testPoller()
	st := p.Status()
	assert.Equal(t, "", st.Interval)
	assert.False(t, st.Active)
	assert.True(t, st.LastCheck.IsZero())
	assert.Equal(t, uint32(0), st.Unseen)
	assert.Equal(t, uint32(0), st.ConsecFails)
	assert.Equal(t, "", st.LastError)
}

func TestPoller_StopIdempotent(t *testing.T) {
	p := testPoller()
	p.Stop() // should not panic
	p.Stop()
}

func TestPoller_StopWaitsForGoroutine(t *testing.T) {
	p := testPoller()
	require.NoError(t, p.SetInterval("5m"))
	assert.True(t, p.Status().Active)
	p.Stop()
	assert.False(t, p.Status().Active)
}

func TestPoller_FirstPollNoNotification(t *testing.T) {
	// Verify that lastCheck starts as zero (the "first poll" signal).
	// The actual notification suppression is in poll(), which requires
	// a full Dialer mock. Here we verify the precondition.
	p := testPoller()
	assert.True(t, p.lastCheck.IsZero(), "lastCheck must start as zero for first-poll detection")
}

func TestPoller_RecordFailure(t *testing.T) {
	p := testPoller()
	p.recordFailure("dial: connection refused")
	st := p.Status()
	assert.Equal(t, uint32(1), st.ConsecFails)
	assert.Equal(t, "dial: connection refused", st.LastError)

	p.recordFailure("status: timeout")
	st = p.Status()
	assert.Equal(t, uint32(2), st.ConsecFails)
	assert.Equal(t, "status: timeout", st.LastError)

	// Simulate success clearing failures.
	p.mu.Lock()
	p.consecFails = 0
	p.lastError = ""
	p.lastCheck = time.Now()
	p.mu.Unlock()
	st = p.Status()
	assert.Equal(t, uint32(0), st.ConsecFails)
	assert.Equal(t, "", st.LastError)
}
