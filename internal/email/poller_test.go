package email

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPoller_SetInterval_Valid(t *testing.T) {
	p := &Poller{}
	require.NoError(t, p.SetInterval("5m"))
	st := p.Status()
	assert.Equal(t, "5m", st.Interval)
	assert.True(t, st.Active)
	p.Stop()
}

func TestPoller_SetInterval_Disable(t *testing.T) {
	p := &Poller{}
	require.NoError(t, p.SetInterval("5m"))
	require.NoError(t, p.SetInterval("n"))
	st := p.Status()
	assert.Equal(t, "n", st.Interval)
	assert.False(t, st.Active)
}

func TestPoller_SetInterval_Invalid(t *testing.T) {
	p := &Poller{}
	err := p.SetInterval("3m")
	require.Error(t, err)
	var ie *InvalidIntervalError
	assert.ErrorAs(t, err, &ie)
}

func TestPoller_Status_Initial(t *testing.T) {
	p := &Poller{}
	st := p.Status()
	assert.Equal(t, "", st.Interval)
	assert.False(t, st.Active)
	assert.True(t, st.LastCheck.IsZero())
	assert.Equal(t, uint32(0), st.Unseen)
}

func TestPoller_StopIdempotent(t *testing.T) {
	p := &Poller{}
	p.Stop() // should not panic
	p.Stop()
}
