package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// mcpServerName is the standalone MCP server name beadle-email registers.
// The marketplace plugin registers the same server as "plugin:beadle:email"
// from its plugin.json; the two must never coexist (see decideMCP).
const mcpServerName = "beadle-email"

// mcpDecision is the outcome of the install command's MCP-registration policy.
// The beadle plugin (plugin.json declares the "email" mcpServer) is the single
// automatic MCP registration; install adds a standalone server only for a
// genuine no-plugin machine.
type mcpDecision int

const (
	// mcpPluginProvides: the beadle plugin is installed and already registers
	// the MCP server, so install does nothing for MCP.
	mcpPluginProvides mcpDecision = iota
	// mcpRegisterStandalone: register a standalone user-scope server with
	// remove-before-add. Chosen only on explicit --standalone opt-in.
	mcpRegisterStandalone
	// mcpAdviseInstall: no plugin and no opt-in — advise installing the plugin
	// or re-running with --standalone. Register nothing.
	mcpAdviseInstall
)

// decideMCP applies the single-source policy. The plugin registers the MCP
// server, so install stays hands-off unless the caller explicitly opts in with
// --standalone (for a no-plugin machine) or has no plugin at all. pluginPresent
// reports whether the beadle plugin is installed; standalone is the --standalone
// opt-in. Never registering standalone alongside the plugin is what stops the
// double-registration drift.
func decideMCP(pluginPresent, standalone bool) mcpDecision {
	switch {
	case standalone:
		return mcpRegisterStandalone
	case pluginPresent:
		return mcpPluginProvides
	default:
		return mcpAdviseInstall
	}
}

// beadlePluginInstalled reports whether `claude plugin list` output shows the
// beadle marketplace plugin, which appears as "beadle@<marketplace>".
func beadlePluginInstalled(pluginList string) bool {
	for _, line := range strings.Split(pluginList, "\n") {
		if strings.Contains(line, "beadle@") {
			return true
		}
	}
	return false
}

// standaloneMCPRegistered reports whether `claude mcp list` output contains a
// standalone beadle-email server. A standalone entry is named "beadle-email";
// the plugin's server is "plugin:beadle:email", so a line beginning with
// "beadle-email:" is unambiguously the standalone duplicate.
func standaloneMCPRegistered(mcpList string) bool {
	prefix := mcpServerName + ":"
	for _, line := range strings.Split(mcpList, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), prefix) {
			return true
		}
	}
	return false
}

// projectScopeRegistration reports whether `claude mcp get beadle-email` output
// shows a project-scope registration. Project scope leaks a beadle server into
// every session that opens the repo and is never what install should create.
func projectScopeRegistration(mcpGet string) bool {
	return strings.Contains(mcpGet, "Scope: Project")
}

// mcpDriftChecks builds doctor checks from raw claude CLI output. It is pure so
// the drift logic is unit-testable; the exec calls live in the caller. It flags
// two forms of drift: a standalone server coexisting with the installed plugin,
// and any project-scope beadle registration.
func mcpDriftChecks(pluginList, mcpList, mcpGet string) []doctorCheck {
	pluginInstalled := beadlePluginInstalled(pluginList)
	standalone := standaloneMCPRegistered(mcpList)

	var checks []doctorCheck
	switch {
	case pluginInstalled && standalone:
		checks = append(checks, doctorCheck{"mcp_registration", "WARN",
			"standalone beadle-email server coexists with the beadle plugin (duplicate) — remove it: claude mcp remove -s user beadle-email"})
	case pluginInstalled:
		checks = append(checks, doctorCheck{"mcp_registration", "OK", "plugin provides the MCP server (no standalone duplicate)"})
	case standalone:
		checks = append(checks, doctorCheck{"mcp_registration", "OK", "standalone user-scope server (plugin not installed)"})
	default:
		checks = append(checks, doctorCheck{"mcp_registration", "OK", "no beadle MCP registration found"})
	}

	if projectScopeRegistration(mcpGet) {
		checks = append(checks, doctorCheck{"mcp_scope", "WARN",
			"beadle-email is registered at project scope — remove it: claude mcp remove -s project beadle-email (use user scope only)"})
	}
	return checks
}

// inspectMCPRegistration queries the claude CLI for MCP-registration drift and
// returns the resulting doctor checks. When claude is unavailable the check is
// skipped (nil). The pure drift logic lives in mcpDriftChecks; this wrapper
// only gathers the raw CLI output.
func inspectMCPRegistration() []doctorCheck {
	if !claudeAvailable() {
		return nil
	}

	pluginList, err := exec.Command("claude", "plugin", "list").Output()
	if err != nil {
		return []doctorCheck{{"mcp_registration", "WARN", fmt.Sprintf("cannot query plugins: %v", err)}}
	}
	mcpList, err := exec.Command("claude", "mcp", "list").Output()
	if err != nil {
		return []doctorCheck{{"mcp_registration", "WARN", fmt.Sprintf("cannot query MCP servers: %v", err)}}
	}

	// `claude mcp get <name>` exits non-zero when the server is not registered;
	// that simply means no project-scope entry, so treat the error as empty.
	var mcpGet string
	if out, getErr := exec.Command("claude", "mcp", "get", mcpServerName).Output(); getErr == nil {
		mcpGet = string(out)
	}

	return mcpDriftChecks(string(pluginList), string(mcpList), mcpGet)
}

// claudeAvailable reports whether the claude CLI is on PATH.
func claudeAvailable() bool {
	_, err := exec.LookPath("claude")
	return err == nil
}

// detectBeadlePlugin runs `claude plugin list` and reports whether the beadle
// plugin is installed. A CLI error is treated as "not installed" so install
// falls back to advising the user rather than crashing.
func detectBeadlePlugin() bool {
	out, err := exec.Command("claude", "plugin", "list").Output()
	if err != nil {
		return false
	}
	return beadlePluginInstalled(string(out))
}

// registerStandaloneMCP registers the standalone beadle-email MCP server at
// user scope with remove-before-add. Removing first discards any stale path or
// project-scope entry, so re-running install always converges on exactly one
// user-scope server — the idempotency the check-then-add pattern lacked.
func registerStandaloneMCP(binPath string) error {
	rm := exec.Command("claude", "mcp", "remove", "-s", "user", mcpServerName)
	rm.Stdout = os.Stderr
	rm.Stderr = os.Stderr
	if err := rm.Run(); err != nil {
		// Not fatal: the server may simply not exist yet. The add below is the
		// authoritative step; a failed remove of an absent server is expected.
		fmt.Fprintf(os.Stderr, "note: no existing user-scope %s registration to remove\n", mcpServerName)
	}

	add := exec.Command("claude", "mcp", "add", "-s", "user", mcpServerName, "--", binPath, "serve")
	add.Stdout = os.Stderr
	add.Stderr = os.Stderr
	if err := add.Run(); err != nil {
		return fmt.Errorf("register standalone MCP server: %w", err)
	}
	return nil
}

// removeStandaloneMCP removes the standalone beadle-email MCP server at user
// scope — the exact scope registerStandaloneMCP adds, keeping uninstall
// symmetric with install.
func removeStandaloneMCP() error {
	rm := exec.Command("claude", "mcp", "remove", "-s", "user", mcpServerName)
	rm.Stdout = os.Stderr
	rm.Stderr = os.Stderr
	if err := rm.Run(); err != nil {
		return fmt.Errorf("remove standalone MCP server: %w", err)
	}
	return nil
}

// setupMCPRegistration applies the single-source MCP policy for `install`. The
// beadle plugin registers the MCP server automatically, so install stays
// hands-off when the plugin is present and registers a standalone user-scope
// server only when the caller opts in with --standalone.
func setupMCPRegistration(standalone bool) error {
	if !claudeAvailable() {
		fmt.Fprintf(os.Stderr,
			"claude CLI not found — install the beadle plugin, or register manually:\n  claude mcp add -s user %s -- %s serve\n",
			mcpServerName, selfPath())
		return nil
	}

	switch decideMCP(detectBeadlePlugin(), standalone) {
	case mcpPluginProvides:
		fmt.Fprintln(os.Stderr, "MCP server provided by the beadle plugin — no standalone registration needed")
		return nil
	case mcpAdviseInstall:
		fmt.Fprintf(os.Stderr,
			"beadle plugin not installed. Install it (recommended):\n  claude plugin install beadle@punt-labs --scope user\nOr register a standalone server for a no-plugin setup:\n  %s install --standalone\n",
			selfPath())
		return nil
	case mcpRegisterStandalone:
		if err := registerStandaloneMCP(selfPath()); err != nil {
			return err
		}
		fmt.Fprintln(os.Stderr, "standalone MCP server registered (user scope)")
		return nil
	default:
		return fmt.Errorf("unhandled MCP decision")
	}
}
