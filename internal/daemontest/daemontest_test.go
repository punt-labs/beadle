package daemontest

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/punt-labs/beadle/internal/daemon"
)

// TestFakeSpawner_RunCopiesEnvOverrides guards against FakeSpawner recording
// the caller's envOverrides map by reference: if Run stored the map as
// passed, mutating it after Run returns would silently change the recorded
// call, and concurrent reuse of one map across calls would race under
// -race.
func TestFakeSpawner_RunCopiesEnvOverrides(t *testing.T) {
	overrides := map[string]string{"FOO": "bar"}

	s := &FakeSpawner{}
	_, err := s.Run(context.Background(), "mission-1", "mcp.json", "prompt.txt", nil, overrides)
	require.NoError(t, err)

	overrides["FOO"] = "mutated"
	overrides["NEW"] = "added"

	calls := s.Calls()
	require.Len(t, calls, 1)
	assert.Equal(t, map[string]string{"FOO": "bar"}, calls[0].EnvOverrides)
}

// TestFakeSpawner_RunNilEnvOverrides confirms a nil envOverrides is
// recorded as nil, not an empty map -- callers that assert on nilness (a
// stage with no declared EnvVars) must see the distinction preserved.
func TestFakeSpawner_RunNilEnvOverrides(t *testing.T) {
	s := &FakeSpawner{}
	_, err := s.Run(context.Background(), "mission-1", "mcp.json", "prompt.txt", nil, nil)
	require.NoError(t, err)

	calls := s.Calls()
	require.Len(t, calls, 1)
	assert.Nil(t, calls[0].EnvOverrides)
}

var _ daemon.Spawner = (*FakeSpawner)(nil)
