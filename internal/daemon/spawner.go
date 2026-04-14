package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"time"
)

var validMissionIDRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9\-]{0,63}$`)

// WorkerResult holds the outcome of a Claude Code worker session.
type WorkerResult struct {
	MissionID string
	Output    string
	SessionID string
	IsError   bool
	ExitCode  int
}

// WorkerSpawner runs a Claude Code session for a mission.
type WorkerSpawner struct {
	APIKey    string
	MaxTurns  int
	MaxBudget string
	Timeout   time.Duration
	Logger    *slog.Logger
}

// workerJSON is the JSON shape returned by claude --output-format json.
type workerJSON struct {
	Result    string `json:"result"`
	SessionID string `json:"session_id"`
	IsError   bool   `json:"is_error"`
}

// Run executes a Claude Code worker for the given mission.
// mcpConfigPath and systemPromptPath must be paths to existing files;
// the caller is responsible for cleanup.
func (s *WorkerSpawner) Run(missionID, mcpConfigPath, systemPromptPath string) (WorkerResult, error) {
	if !validMissionIDRe.MatchString(missionID) {
		return WorkerResult{MissionID: missionID}, fmt.Errorf("invalid mission ID %q", missionID)
	}
	timeout := s.Timeout
	if timeout == 0 {
		timeout = 30 * time.Minute
	}
	maxTurns := s.MaxTurns
	if maxTurns == 0 {
		maxTurns = 50
	}
	maxBudget := s.MaxBudget
	if maxBudget == "" {
		maxBudget = "5.00"
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	args := []string{
		"-p", "--bare",
		"--mcp-config", mcpConfigPath,
		"--append-system-prompt-file", systemPromptPath,
		"--output-format", "json",
		"--max-turns", strconv.Itoa(maxTurns),
		"--max-budget-usd", maxBudget,
		"--permission-mode", "auto",
		"--allowedTools", "Bash,Read,Edit,Write,Glob,Grep,Agent",
		"Execute mission " + missionID,
	}

	cmd := exec.CommandContext(ctx, "claude", args...)
	// Minimal env: only what claude needs. Do not leak daemon credentials
	// (BEADLE_IMAP_PASSWORD, BEADLE_RESEND_API_KEY, etc.) to the subprocess.
	cmd.Env = []string{
		"ANTHROPIC_API_KEY=" + s.APIKey,
		"HOME=" + os.Getenv("HOME"),
		"PATH=" + os.Getenv("PATH"),
		"USER=" + os.Getenv("USER"),
	}

	s.Logger.Info("spawning worker", "mission", missionID, "timeout", timeout)

	out, err := cmd.Output()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
			s.Logger.Warn("worker exited with error",
				"mission", missionID,
				"exitCode", exitCode,
				"stderr", string(exitErr.Stderr))
		} else {
			return WorkerResult{MissionID: missionID, ExitCode: -1},
				fmt.Errorf("run claude for mission %s: %w", missionID, err)
		}
	}

	return parseWorkerOutput(missionID, out, exitCode)
}

// parseWorkerOutput parses the JSON output from a claude --output-format json
// invocation and returns a WorkerResult.
func parseWorkerOutput(missionID string, out []byte, exitCode int) (WorkerResult, error) {
	var parsed workerJSON
	if err := json.Unmarshal(out, &parsed); err != nil {
		return WorkerResult{
			MissionID: missionID,
			Output:    string(out),
			IsError:   true,
			ExitCode:  exitCode,
		}, fmt.Errorf("parse worker output for mission %s: %w", missionID, err)
	}

	return WorkerResult{
		MissionID: missionID,
		Output:    parsed.Result,
		SessionID: parsed.SessionID,
		IsError:   parsed.IsError,
		ExitCode:  exitCode,
	}, nil
}
