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
// the caller is responsible for cleanup. envOverrides are added to the
// subprocess environment (e.g. secrets resolved by the daemon for a command).
// Pass nil when no overrides are needed.
func (s *WorkerSpawner) Run(ctx context.Context, missionID, mcpConfigPath, systemPromptPath string, envOverrides map[string]string) (WorkerResult, error) {
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

	// Worker needs real HOME so MCP servers (ethos, beadle-email) can find
	// their config at ~/.punt-labs/. --bare mode already skips CLAUDE.md,
	// hooks, plugins, and auto memory. The adversarial system prompt is the
	// defense against prompt injection reaching sensitive files.
	// TODO: container isolation (network namespace, seccomp) for defense-in-depth.
	workerHome := os.Getenv("HOME")

	// Restricted PATH: claude binary dir + /usr/bin (git, basic tools).
	workerPATH := filepath.Dir(claudePath) + ":/usr/bin:/usr/local/bin"

	s.logger().Info("spawner",
		"claude", claudePath,
		"mission", missionID,
		"home", workerHome,
		"hasAPIKey", s.APIKey != "")
	cmd := exec.CommandContext(ctx, claudePath, args...)
	// Minimal env: only what claude needs. Do not leak daemon credentials
	// (BEADLE_IMAP_PASSWORD, BEADLE_RESEND_API_KEY, etc.) to the subprocess.
	// Build env as a map, apply overrides, then force-set protected vars
	// AFTER overrides so they always win.
	envMap := map[string]string{
		"ANTHROPIC_API_KEY": s.APIKey,
		"HOME":              workerHome,
		"PATH":              workerPATH,
		"USER":              os.Getenv("USER"),
	}
	for k, v := range envOverrides {
		envMap[k] = v
	}
	// Force-set protected vars AFTER overrides — never allow override.
	envMap["ANTHROPIC_API_KEY"] = s.APIKey
	envMap["HOME"] = workerHome
	envMap["PATH"] = workerPATH

	cmd.Env = make([]string, 0, len(envMap))
	for k, v := range envMap {
		cmd.Env = append(cmd.Env, k+"="+v)
	}

	// Log env keys (not values — API key is secret).
	envKeys := make([]string, 0, len(envMap))
	for k := range envMap {
		envKeys = append(envKeys, k)
	}
	s.logger().Info("spawning worker",
		"mission", missionID,
		"timeout", timeout,
		"envKeys", envKeys)

	out, err := cmd.CombinedOutput()
	// Always log raw output for diagnostics.
	s.logger().Info("worker raw output",
		"mission", missionID,
		"outputLen", len(out),
		"output", truncateLog(string(out), 1000))
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
			s.logger().Warn("worker exited with error",
				"mission", missionID,
				"exitCode", exitCode)
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
