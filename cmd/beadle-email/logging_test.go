package main

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/punt-labs/beadle/internal/usage"
)

func TestLoggingEntrypoints(t *testing.T) {
	t.Setenv("PUNT_TELEMETRY", "")
	t.Setenv("BEADLE_TELEMETRY", "")
	binDir := t.TempDir()
	for _, name := range []string{"beadle-email", "beadle-daemon"} {
		build := exec.Command("go", "build", "-race", "-o", filepath.Join(binDir, name), "../"+name)
		out, err := build.CombinedOutput()
		require.NoError(t, err, "%s", out)
	}

	for _, control := range []string{"enabled", "invalid flags", "BEADLE_TELEMETRY", "PUNT_TELEMETRY", "email.json", "daemon.json"} {
		t.Run("telemetry "+control, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("PUNT_TELEMETRY", "")
			t.Setenv("BEADLE_TELEMETRY", "")
			dir := filepath.Join(home, ".punt-labs", "beadle")
			if strings.HasSuffix(control, ".json") {
				require.NoError(t, os.MkdirAll(dir, 0o700))
				require.NoError(t, os.WriteFile(filepath.Join(dir, control), []byte(`{"telemetry_disabled":true}`), 0o600))
			} else if control != "enabled" && control != "invalid flags" {
				t.Setenv(control, "off")
			}
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			args := []string{"version"}
			if control == "invalid flags" {
				args = append(args, "--not-a-real-flag")
			}
			cmd := exec.CommandContext(ctx, filepath.Join(binDir, "beadle-email"), args...)
			if control == "invalid flags" {
				require.Error(t, cmd.Run())
			} else {
				require.NoError(t, cmd.Run())
			}
			path := filepath.Join(dir, "telemetry.db")
			if control != "enabled" && control != "invalid flags" {
				_, err := os.Stat(path)
				assert.True(t, os.IsNotExist(err))
				return
			}
			info, err := os.Stat(path)
			require.NoError(t, err)
			assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
			assertUsageRow(t, path, "beadle.cli.command", "command", "version")
			outcome := "ok"
			if control == "invalid flags" {
				outcome = "error"
				assert.Contains(t, readDiagnosticLog(t, home), "success=false")
			}
			assertUsageRow(t, path, "beadle.cli.command", "outcome", outcome)
		})
	}

	for _, tc := range []struct {
		name, binary, component string
		args                    []string
		debug, wantError        bool
	}{
		{name: "cli", binary: "beadle-email", component: "cli", args: []string{"version"}},
		{name: "repair modes", binary: "beadle-email", component: "cli", args: []string{"version"}},
		{name: "quiet", binary: "beadle-email", component: "cli", args: []string{"-q", "version"}},
		{name: "quiet verbose", binary: "beadle-email", component: "cli", args: []string{"-q", "-v", "version"}, debug: true},
		{name: "verbose", binary: "beadle-email", component: "cli", args: []string{"-v", "version"}, debug: true},
		{name: "env debug", binary: "beadle-email", component: "cli", args: []string{"version"}, debug: true},
		{name: "mcp ws failure", binary: "beadle-email", component: "mcp", args: []string{"serve", "--transport", "ws", "--port", "-1"}, wantError: true},
		{name: "daemon failure", binary: "beadle-daemon", component: "daemon", args: []string{"run"}, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			if tc.name == "repair modes" {
				dir := filepath.Join(home, ".punt-labs", "beadle", "logs")
				require.NoError(t, os.MkdirAll(dir, 0o755))
				require.NoError(t, os.WriteFile(filepath.Join(dir, "beadle.log"), nil, 0o644))
			}
			t.Setenv("HOME", home)
			t.Setenv("BEADLE_DEBUG", "")
			t.Setenv("BEADLE_LOG_MISSION_ID", "m-logging-test")
			if tc.name == "env debug" {
				t.Setenv("BEADLE_DEBUG", "1")
			}
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, filepath.Join(binDir, tc.binary), tc.args...)
			cmd.Dir = t.TempDir()
			err := cmd.Run()
			if tc.wantError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			data := readDiagnosticLog(t, home)
			assert.Contains(t, data, "[INFO] beadle."+tc.component+": Command started")
			assert.Contains(t, data, "Command completed")
			if tc.binary == "beadle-email" {
				assert.Contains(t, data, "mission=m-logging-test")
			}
			assert.Equal(t, tc.debug, strings.Contains(data, "[DEBUG]"))
			assert.Contains(t, data, "success="+map[bool]string{true: "false", false: "true"}[tc.wantError])
		})
	}

	t.Run("stdio transport without stderr", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("BEADLE_DEBUG", "1")
		ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, filepath.Join(binDir, "beadle-email"), "serve")
		cmd.Dir = t.TempDir()
		stdin, err := cmd.StdinPipe()
		require.NoError(t, err)
		stdout, err := cmd.StdoutPipe()
		require.NoError(t, err)
		require.NoError(t, cmd.Start())
		cleanupProcess(t, cmd)
		_, err = stdin.Write([]byte("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\",\"params\":{\"protocolVersion\":\"2024-11-05\",\"capabilities\":{},\"clientInfo\":{\"name\":\"logging-test\",\"version\":\"1\"}}}\n"))
		require.NoError(t, err)
		scanner := bufio.NewScanner(stdout)
		require.True(t, scanner.Scan())
		var reply map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(scanner.Bytes(), &reply))
		assert.JSONEq(t, `"2.0"`, string(reply["jsonrpc"]))
		assert.JSONEq(t, `1`, string(reply["id"]))

		_, err = stdin.Write([]byte("{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/call\",\"params\":{\"name\":\"whoami\",\"arguments\":{}}}\n"))
		require.NoError(t, err)
		require.True(t, scanner.Scan())
		require.NoError(t, json.Unmarshal(scanner.Bytes(), &reply))
		assert.JSONEq(t, `2`, string(reply["id"]))
		require.NoError(t, stdin.Close())
		for scanner.Scan() {
			require.True(t, json.Valid(scanner.Bytes()), "%s", scanner.Text())
		}
		require.NoError(t, scanner.Err())
		require.NoError(t, cmd.Wait())
		assert.Contains(t, readDiagnosticLog(t, home), "[INFO] beadle.mcp: Command started")
		assertUsageRow(t, filepath.Join(home, ".punt-labs", "beadle", "telemetry.db"), "beadle.mcp.tool", "tool", "whoami")
	})

	t.Run("daemon graceful shutdown", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("ANTHROPIC_API_KEY", "")
		dir := filepath.Join(home, ".punt-labs", "beadle")
		require.NoError(t, os.MkdirAll(dir, 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "default-identity"), []byte("daemon@example.invalid"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "email.json"), []byte(`{"poll_interval":"n"}`), 0o600))
		ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, filepath.Join(binDir, "beadle-daemon"), "run")
		cmd.Dir = t.TempDir()
		require.NoError(t, cmd.Start())
		cleanupProcess(t, cmd)
		require.Eventually(t, func() bool {
			data, err := os.ReadFile(filepath.Join(dir, "logs", "beadle.log"))
			return err == nil && strings.Contains(string(data), "daemon started")
		}, 10*time.Second, 10*time.Millisecond)
		require.NoError(t, cmd.Process.Signal(syscall.SIGTERM))
		require.NoError(t, cmd.Wait())
		data := readDiagnosticLog(t, home)
		assert.Contains(t, data, "shutting down signal=terminated")
		assert.NotContains(t, data, "[unsupported value]")
	})

	t.Run("initialization failure surfaces independently", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		require.NoError(t, os.WriteFile(filepath.Join(home, ".punt-labs"), []byte("blocked"), 0o600))
		cmd := exec.Command(filepath.Join(binDir, "beadle-email"), "version")
		var stderr, stdout bytes.Buffer
		cmd.Stderr, cmd.Stdout = &stderr, &stdout
		require.Error(t, cmd.Run())
		assert.Contains(t, stderr.String(), "initialize logging")
		assert.Empty(t, stdout.String())
	})
}

func cleanupProcess(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	t.Cleanup(func() {
		if cmd.ProcessState != nil {
			return
		}
		if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			t.Errorf("kill child process: %v", err)
		}
		if err := cmd.Wait(); err != nil {
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) {
				t.Errorf("reap child process: %v", err)
			}
		}
	})
}

func TestQuietOnlySuppressesOptionalOutput(t *testing.T) {
	t.Setenv("BEADLE_DEBUG", "")
	for _, quiet := range []bool{false, true} {
		opts := globalOpts{Quiet: quiet}
		assert.Equal(t, slog.LevelInfo, opts.slogLevel())
		called := false
		opts.printResult(nil, func() { called = true })
		assert.Equal(t, !quiet, called)
	}
	opts := globalOpts{Quiet: true, JSON: true}
	output := captureStdout(t, func() {
		opts.printResult(map[string]string{"status": "ok"}, func() { t.Fatal("unexpected human output") })
	})
	assert.JSONEq(t, `{"status":"ok"}`, output)
	assert.Contains(t, rootCmd.PersistentFlags().Lookup("quiet").Usage, "does not affect JSON or logging")
}

func assertUsageRow(t *testing.T, path, name, key, value string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	require.NoError(t, err)
	defer func() { require.NoError(t, db.Close()) }()
	var encoded string
	require.NoError(t, db.QueryRowContext(t.Context(), "SELECT attributes FROM signals WHERE name=?", name).Scan(&encoded))
	var attrs map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(encoded), &attrs))
	assert.JSONEq(t, strconv.Quote(value), string(attrs[key]))
}

func TestUsageCommandPaths(t *testing.T) {
	rootCmd.InitDefaultCompletionCmd()
	var visit func(*cobra.Command)
	visit = func(cmd *cobra.Command) {
		if cmd.Runnable() && cmd.Name() != "help" {
			assert.NotEmpty(t, usage.CommandForPath(cmd.CommandPath()), cmd.CommandPath())
		}
		for _, child := range cmd.Commands() {
			visit(child)
		}
	}
	visit(rootCmd)
}

func readDiagnosticLog(t *testing.T, home string) string {
	t.Helper()
	dir := filepath.Join(home, ".punt-labs", "beadle", "logs")
	for path, mode := range map[string]os.FileMode{dir: 0o700, filepath.Join(dir, "beadle.log"): 0o600} {
		info, err := os.Stat(path)
		require.NoError(t, err)
		assert.Equal(t, mode, info.Mode().Perm())
	}
	data, err := os.ReadFile(filepath.Join(dir, "beadle.log"))
	require.NoError(t, err)
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		assert.Regexp(t, `^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2} \[(INFO|WARNING|ERROR|DEBUG)\] beadle\.[a-z]+: `, line)
	}
	return string(data)
}
