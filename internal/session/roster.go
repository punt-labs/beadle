// Package session reads the ethos session roster to enumerate participants
// (human + agent) in the current Claude Code session. Uses the sidecar
// pattern — file reads only, no import dependency on ethos.
package session

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// Roster represents an ethos session with its participants.
type Roster struct {
	Session      string        `yaml:"session"`
	Started      string        `yaml:"started"`
	Participants []Participant `yaml:"participants"`
}

// Participant is a session member. Human participants have no Parent field;
// agent participants have Parent set to the human's agent_id.
type Participant struct {
	AgentID string `yaml:"agent_id"`
	Persona string `yaml:"persona,omitempty"`
	Parent  string `yaml:"parent,omitempty"`
}

// IsHuman returns true if the participant has no parent (root participant).
func (p Participant) IsHuman() bool {
	return p.Parent == ""
}

// HumanParticipants returns participants without a parent field.
func (r *Roster) HumanParticipants() []Participant {
	var out []Participant
	for _, p := range r.Participants {
		if p.IsHuman() {
			out = append(out, p)
		}
	}
	return out
}

// AgentParticipants returns participants with a parent field.
func (r *Roster) AgentParticipants() []Participant {
	var out []Participant
	for _, p := range r.Participants {
		if !p.IsHuman() {
			out = append(out, p)
		}
	}
	return out
}

// ReadRoster reads the session roster for the current Claude Code session.
// Returns (nil, nil) if no session is active (no Claude ancestor, no session
// file, or ethos sessions directory missing). This makes the roster optional
// — callers should handle nil gracefully.
func ReadRoster(ethosDir string) (*Roster, error) {
	if ethosDir == "" {
		return nil, nil
	}

	pid := findClaudePID()
	if pid == 0 {
		return nil, nil
	}

	// Read session ID from current/<pid> sidecar.
	currentPath := filepath.Join(ethosDir, "sessions", "current", strconv.Itoa(pid))
	data, err := os.ReadFile(currentPath)
	if err != nil {
		return nil, nil // no session file = not in a session
	}

	sessionID := strings.TrimSpace(string(data))
	if sessionID == "" {
		return nil, nil
	}

	// Read roster YAML.
	rosterPath := filepath.Join(ethosDir, "sessions", sessionID+".yaml")
	rosterData, err := os.ReadFile(rosterPath)
	if err != nil {
		return nil, nil // session file missing = stale sidecar
	}

	var roster Roster
	if err := yaml.Unmarshal(rosterData, &roster); err != nil {
		return nil, fmt.Errorf("parse session roster %s: %w", rosterPath, err)
	}

	return &roster, nil
}

// --- Process tree walk (copied from mcp-proxy/internal/session) ---

var (
	claudePID     int
	claudePIDOnce sync.Once
)

// findClaudePID returns the PID of the topmost claude ancestor process.
// Cached for the lifetime of the process. Returns 0 if not found.
func findClaudePID() int {
	claudePIDOnce.Do(func() {
		claudePID = walkToTopmostClaude(os.Getpid(), readProcessTable)
	})
	return claudePID
}

type processEntry struct {
	ppid int
	comm string
}

// walkToTopmostClaude walks upward from pid, returning the topmost claude
// ancestor. Returns 0 if no claude ancestor is found.
func walkToTopmostClaude(pid int, tableFn func() (map[int]processEntry, error)) int {
	table, err := tableFn()
	if err != nil {
		return 0
	}

	topmostClaude := 0
	current := pid
	visited := make(map[int]bool, 10)

	for range 10 { // safety bound — process trees are shallow
		if visited[current] {
			break
		}
		visited[current] = true

		entry, ok := table[current]
		if !ok {
			break
		}
		if isClaude(entry.comm) {
			topmostClaude = current
		}
		if entry.ppid == current || entry.ppid == 0 {
			break
		}
		current = entry.ppid
	}

	return topmostClaude
}

// readProcessTable runs ps and parses output into {pid: (ppid, comm)}.
func readProcessTable() (map[int]processEntry, error) {
	out, err := exec.Command("ps", "-eo", "pid=,ppid=,comm=").Output()
	if err != nil {
		return nil, err
	}

	table := make(map[int]processEntry)
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		pid, err1 := strconv.Atoi(fields[0])
		ppid, err2 := strconv.Atoi(fields[1])
		if err1 != nil || err2 != nil {
			continue
		}
		comm := strings.Join(fields[2:], " ")
		table[pid] = processEntry{ppid: ppid, comm: comm}
	}
	return table, nil
}

// isClaude checks whether a comm value refers to a Claude Code process.
func isClaude(comm string) bool {
	return filepath.Base(comm) == "claude"
}
