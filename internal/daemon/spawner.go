package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var validMissionIDRe = regexp.MustCompile(`^m-[a-z0-9][a-z0-9-]{0,61}$`)

// ValidMissionID reports whether id matches the mission ID format.
func ValidMissionID(id string) bool {
	return validMissionIDRe.MatchString(id)
}

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

func (s *WorkerSpawner) logger() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

// Run executes a Claude Code worker for the given mission.
// The context governs subprocess lifetime — cancel it for graceful shutdown.
// mcpConfigPath and systemPromptPath must be paths to existing files;
// the caller is responsible for cleanup.
func (s *WorkerSpawner) Run(ctx context.Context, missionID, mcpConfigPath, systemPromptPath string) (WorkerResult, error) {
	if !ValidMissionID(missionID) {
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

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	prompt := "Execute mission " + missionID
	args := []string{
		"-p",
		"--bare",
		"--mcp-config", mcpConfigPath,
		"--append-system-prompt-file", systemPromptPath,
		"--output-format", "json",
		"--max-turns", strconv.Itoa(maxTurns),
		"--max-budget-usd", maxBudget,
		"--permission-mode", "auto",
		"--allowedTools", "Bash,Read,Edit,Write,Glob,Grep,Agent",
		// -- ends option parsing; prompt is always positional even if it starts with -
		"--", prompt,
	}

	claudePath, err := exec.LookPath("claude")
	if err != nil {
		return WorkerResult{MissionID: missionID, ExitCode: -1},
			fmt.Errorf("find claude binary: %w", err)
	}

	// Isolated HOME prevents the worker from reading ~/.ssh, ~/.gnupg, etc.
	workerHome, err := os.MkdirTemp("", "beadle-worker-*")
	if err != nil {
		return WorkerResult{MissionID: missionID, ExitCode: -1},
			fmt.Errorf("create worker home: %w", err)
	}
	defer os.RemoveAll(workerHome)

	// Restricted PATH: claude binary dir + /usr/bin (git, basic tools).
	workerPATH := filepath.Dir(claudePath) + ":/usr/bin:/usr/local/bin"

	cmd := exec.CommandContext(ctx, claudePath, args...)
	// Minimal env: only what claude needs. Do not leak daemon credentials
	// (BEADLE_IMAP_PASSWORD, BEADLE_RESEND_API_KEY, etc.) to the subprocess.
	// HOME is an isolated temp dir — no access to user's SSH keys, GPG, config.
	cmd.Env = []string{
		"ANTHROPIC_API_KEY=" + s.APIKey,
		"HOME=" + workerHome,
		"PATH=" + workerPATH,
		"USER=" + os.Getenv("USER"),
		"TMPDIR=" + workerHome,
	}

	s.logger().Info("spawning worker", "mission", missionID, "timeout", timeout)

	out, err := cmd.Output()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
			s.logger().Warn("worker exited with error",
				"mission", missionID,
				"exitCode", exitCode,
				"stderr", string(exitErr.Stderr))
		} else {
			return WorkerResult{MissionID: missionID, ExitCode: -1},
				fmt.Errorf("run claude for mission %s: %w", missionID, err)
		}
	}

	result, parseErr := parseWorkerOutput(missionID, out, exitCode)
	if parseErr != nil {
		return result, parseErr
	}
	// Non-zero exit is always an error, even if JSON parsed successfully.
	if exitCode != 0 {
		result.IsError = true
	}
	return result, nil
}

// parseWorkerOutput parses the JSON output from a claude --output-format json
// invocation and returns a WorkerResult.
func parseWorkerOutput(missionID string, out []byte, exitCode int) (WorkerResult, error) {
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return WorkerResult{
			MissionID: missionID,
			IsError:   true,
			ExitCode:  exitCode,
		}, fmt.Errorf("worker for mission %s produced empty output (exit code %d)", missionID, exitCode)
	}

	var parsed workerJSON
	if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
		return WorkerResult{
			MissionID: missionID,
			Output:    trimmed,
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
