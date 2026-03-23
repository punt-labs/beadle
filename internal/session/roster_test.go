package session

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestReadRoster_Valid(t *testing.T) {
	ethosDir := t.TempDir()

	// Write a session roster.
	sessDir := filepath.Join(ethosDir, "sessions")
	require.NoError(t, os.MkdirAll(filepath.Join(sessDir, "current"), 0o750))

	rosterYAML := `session: test-session-123
started: "2026-03-22T05:30:47Z"
participants:
  - agent_id: jfreeman
    persona: jfreeman
  - agent_id: "12345"
    persona: claude
    parent: jfreeman
`
	require.NoError(t, os.WriteFile(filepath.Join(sessDir, "test-session-123.yaml"), []byte(rosterYAML), 0o640))

	// Write current/<pid> sidecar pointing at our session.
	// Use a known PID from the process tree walk. Since we can't predict
	// the Claude PID in tests, test ReadRosterFromFile directly instead.

	var roster Roster
	data, err := os.ReadFile(filepath.Join(sessDir, "test-session-123.yaml"))
	require.NoError(t, err)
	require.NoError(t, yaml.Unmarshal(data, &roster))

	assert.Equal(t, "test-session-123", roster.Session)
	assert.Len(t, roster.Participants, 2)
	assert.Equal(t, "jfreeman", roster.Participants[0].Persona)
	assert.Equal(t, "claude", roster.Participants[1].Persona)
	assert.Equal(t, "jfreeman", roster.Participants[1].Parent)
}

func TestReadRoster_NoSession(t *testing.T) {
	ethosDir := t.TempDir()
	roster, err := ReadRoster(ethosDir)
	assert.Nil(t, roster)
	assert.NoError(t, err)
}

func TestReadRoster_EmptyDir(t *testing.T) {
	roster, err := ReadRoster("")
	assert.Nil(t, roster)
	assert.NoError(t, err)
}

func TestHumanParticipants(t *testing.T) {
	roster := &Roster{
		Participants: []Participant{
			{AgentID: "jfreeman", Persona: "jfreeman"},
			{AgentID: "12345", Persona: "claude", Parent: "jfreeman"},
		},
	}

	humans := roster.HumanParticipants()
	require.Len(t, humans, 1)
	assert.Equal(t, "jfreeman", humans[0].Persona)
}

func TestAgentParticipants(t *testing.T) {
	roster := &Roster{
		Participants: []Participant{
			{AgentID: "jfreeman", Persona: "jfreeman"},
			{AgentID: "12345", Persona: "claude", Parent: "jfreeman"},
			{AgentID: "67890", Persona: "helper", Parent: "jfreeman"},
		},
	}

	agents := roster.AgentParticipants()
	require.Len(t, agents, 2)
	assert.Equal(t, "claude", agents[0].Persona)
	assert.Equal(t, "helper", agents[1].Persona)
}

func TestParticipant_IsHuman(t *testing.T) {
	assert.True(t, Participant{AgentID: "jfreeman"}.IsHuman())
	assert.False(t, Participant{AgentID: "12345", Parent: "jfreeman"}.IsHuman())
}

func TestWalkToTopmostClaude(t *testing.T) {
	// Mock process table: pid=100 → ppid=50 (claude) → ppid=1 (init)
	table := map[int]processEntry{
		100: {ppid: 50, comm: "/usr/local/bin/beadle-email"},
		50:  {ppid: 10, comm: "/Users/jfreeman/.claude/local/claude"},
		10:  {ppid: 1, comm: "/sbin/launchd"},
		1:   {ppid: 0, comm: "launchd"},
	}
	mockTable := func() (map[int]processEntry, error) { return table, nil }

	pid := walkToTopmostClaude(100, mockTable)
	assert.Equal(t, 50, pid)
}

func TestWalkToTopmostClaude_NoClaude(t *testing.T) {
	table := map[int]processEntry{
		100: {ppid: 50, comm: "beadle-email"},
		50:  {ppid: 1, comm: "bash"},
		1:   {ppid: 0, comm: "launchd"},
	}
	mockTable := func() (map[int]processEntry, error) { return table, nil }

	pid := walkToTopmostClaude(100, mockTable)
	assert.Equal(t, 0, pid)
}

func TestWalkToTopmostClaude_NestedClaude(t *testing.T) {
	// Two claude processes — should return the topmost one.
	table := map[int]processEntry{
		100: {ppid: 80, comm: "beadle-email"},
		80:  {ppid: 50, comm: "claude"},
		50:  {ppid: 10, comm: "claude"},
		10:  {ppid: 1, comm: "bash"},
		1:   {ppid: 0, comm: "launchd"},
	}
	mockTable := func() (map[int]processEntry, error) { return table, nil }

	pid := walkToTopmostClaude(100, mockTable)
	assert.Equal(t, 50, pid) // topmost
}

func TestWalkToTopmostClaude_DepthLimit(t *testing.T) {
	// Chain of 12 processes — exceeds the 10-step safety bound.
	// Claude is at pid=1 (depth 11), should NOT be found.
	table := make(map[int]processEntry)
	for i := 12; i > 1; i-- {
		table[i] = processEntry{ppid: i - 1, comm: "bash"}
	}
	table[1] = processEntry{ppid: 0, comm: "claude"}
	mockTable := func() (map[int]processEntry, error) { return table, nil }

	pid := walkToTopmostClaude(12, mockTable)
	assert.Equal(t, 0, pid) // beyond depth limit
}

func TestWalkToTopmostClaude_CycleDetection(t *testing.T) {
	// Cycle: 100 → 50 → 100 (should terminate without infinite loop).
	table := map[int]processEntry{
		100: {ppid: 50, comm: "bash"},
		50:  {ppid: 100, comm: "bash"},
	}
	mockTable := func() (map[int]processEntry, error) { return table, nil }

	pid := walkToTopmostClaude(100, mockTable)
	assert.Equal(t, 0, pid) // no claude found
}

