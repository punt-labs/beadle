package email

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/punt-labs/beadle/internal/identity"
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

func TestNewPoller_NilRepoScopeIgnored(t *testing.T) {
	// A nil resolver must not clobber the default, so poll() never calls a
	// nil func.
	p := NewPoller(nil, nil, discardLogger(), nil, WithRepoScope(nil))
	require.NotNil(t, p.repoSlug)
	assert.NotPanics(t, func() { _ = p.repoSlug() })
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

func TestPoller_Notify(t *testing.T) {
	tests := []struct {
		name       string
		first      bool
		prev       uint32
		unseen     uint32
		wantCount  bool   // count observer fired
		wantCountN uint32 // value it fired with
		wantMail   bool   // new-mail callback fired
		wantMailN  uint32 // delta it fired with
	}{
		{name: "increase after first: both fire", prev: 3, unseen: 5, wantCount: true, wantCountN: 5, wantMail: true, wantMailN: 2},
		{name: "decrease: only count observer", prev: 40, unseen: 3, wantCount: true, wantCountN: 3},
		{name: "read down to zero: count observer clears", prev: 5, unseen: 0, wantCount: true, wantCountN: 0},
		{name: "no change: neither fires", prev: 5, unseen: 5},
		{name: "first poll with mail: count observer only", first: true, prev: 0, unseen: 5, wantCount: true, wantCountN: 5},
		{name: "first poll empty: neither fires", first: true, prev: 0, unseen: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotCount, gotMail bool
			var countN, mailN uint32
			p := &Poller{
				logger:    discardLogger(),
				onCount:   func(n uint32) { gotCount = true; countN = n },
				onNewMail: func(n uint32) { gotMail = true; mailN = n },
			}
			p.notify(tt.first, tt.prev, tt.unseen)

			assert.Equal(t, tt.wantCount, gotCount, "count observer fired")
			if tt.wantCount {
				assert.Equal(t, tt.wantCountN, countN)
			}
			assert.Equal(t, tt.wantMail, gotMail, "new-mail callback fired")
			if tt.wantMail {
				assert.Equal(t, tt.wantMailN, mailN)
			}
		})
	}
}

// TestPoller_LoadConfig_DataDirFailureErrorsNotPanics proves loadConfig
// returns a clean error, rather than panicking, when the fallback path's
// underlying paths.DataDir() call fails — a real robustness requirement for
// a background goroutine that must not crash the process on every poll tick.
// Regression guard for eagerly evaluating the panicking DefaultConfigPath()
// as a call argument.
func TestPoller_LoadConfig_DataDirFailureErrorsNotPanics(t *testing.T) {
	beadleDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(beadleDir, "default-identity"), []byte("agent@test.com\n"), 0o600))
	resolver := identity.NewResolver(t.TempDir(), beadleDir, t.TempDir())

	t.Setenv("HOME", "")

	p := &Poller{logger: discardLogger(), resolver: resolver}
	var err error
	assert.NotPanics(t, func() {
		_, err = p.loadConfig()
	})
	require.Error(t, err)
}

// TestPoller_LoadConfig_FallbackLoadFailureWrapped proves a fallback-file
// load failure is distinguished from any other loadConfig failure, so
// poller failure logs stay debuggable.
func TestPoller_LoadConfig_FallbackLoadFailureWrapped(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	beadleDir := filepath.Join(home, ".punt-labs", "beadle")
	require.NoError(t, os.MkdirAll(beadleDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(beadleDir, "default-identity"), []byte("agent@test.com\n"), 0o600))
	resolver := identity.NewResolver(t.TempDir(), beadleDir, t.TempDir())

	p := &Poller{logger: discardLogger(), resolver: resolver}
	_, err := p.loadConfig()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "default config:")
}

func TestPoller_RecordFailure(t *testing.T) {
	p := testPoller()
	before := time.Now()
	p.recordFailure("dial: connection refused")
	st := p.Status()
	assert.Equal(t, uint32(1), st.ConsecFails)
	assert.Equal(t, "dial: connection refused", st.LastError)
	assert.False(t, st.LastCheck.IsZero(), "lastCheck must be set on failure")
	assert.False(t, st.LastCheck.Before(before), "lastCheck must be >= call time")

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
