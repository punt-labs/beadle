package main

import (
	"errors"
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
	// remove-before-add. Chosen on --standalone opt-in with no plugin present.
	mcpRegisterStandalone
	// mcpRegisterStandaloneWarnDuplicate: --standalone was passed WHILE the
	// plugin is present. The explicit opt-in is honored, but the standalone
	// server would duplicate the plugin's server, so a prominent warning
	// precedes the add — registration is never silent in this case.
	mcpRegisterStandaloneWarnDuplicate
	// mcpAdviseInstall: no plugin and no opt-in — advise installing the plugin
	// or re-running with --standalone. Register nothing.
	mcpAdviseInstall
)

// decideMCP applies the single-source policy. The beadle plugin registers the
// MCP server, so install stays hands-off when the plugin is present. A
// standalone server is registered only when the caller opts in with
// --standalone (a no-plugin machine) or has no plugin at all. When --standalone
// is passed WHILE the plugin is present, the explicit opt-in still wins — but
// registering creates a duplicate, so the decision routes to a warn-first path
// (mcpRegisterStandaloneWarnDuplicate) rather than registering silently.
// pluginPresent reports whether the beadle plugin is installed and enabled;
// standalone is the --standalone opt-in.
func decideMCP(pluginPresent, standalone bool) mcpDecision {
	switch {
	case standalone && pluginPresent:
		return mcpRegisterStandaloneWarnDuplicate
	case standalone:
		return mcpRegisterStandalone
	case pluginPresent:
		return mcpPluginProvides
	default:
		return mcpAdviseInstall
	}
}

// beadlePluginInstalled reports whether `claude plugin list` shows the beadle
// marketplace plugin installed AND enabled. The plugin header appears as
// "beadle@<marketplace>", followed a few lines later by "Status: ✔ enabled" or
// "Status: ✘ disabled". A disabled plugin does not run its MCP server, so it is
// not an active source and is treated as not-installed for the single-source
// policy (the enabled/disabled marker is what makes this distinguishable).
func beadlePluginInstalled(pluginList string) bool {
	inBeadleBlock := false
	for _, line := range strings.Split(pluginList, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.Contains(line, "beadle@"):
			inBeadleBlock = true
		case inBeadleBlock && strings.HasPrefix(trimmed, "Status:"):
			return strings.Contains(trimmed, "enabled")
		case inBeadleBlock && strings.Contains(trimmed, "@"):
			// The next plugin's header before any Status line for beadle — a
			// malformed block. Stop treating following lines as beadle's.
			inBeadleBlock = false
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
//
// Limitation: `claude mcp get` reports a single scope, and user scope shadows
// project scope. If beadle-email is registered at BOTH user and project scope,
// get reports the user entry and this returns false, leaving the project-scope
// duplicate unflagged. The common drift (project-only) is still caught.
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

	// `claude mcp get <name>` exits non-zero when the server is not registered
	// at any scope. Distinguish that expected-absent case (an *exec.ExitError →
	// no project-scope entry, no warning) from a genuine failure to run claude,
	// which must NOT be read as "no project scope" — surface it as a WARN.
	var mcpGet, getFailed string
	if out, getErr := exec.Command("claude", "mcp", "get", mcpServerName).Output(); getErr != nil {
		var exitErr *exec.ExitError
		if !errors.As(getErr, &exitErr) {
			getFailed = getErr.Error()
		}
	} else {
		mcpGet = string(out)
	}

	checks := mcpDriftChecks(string(pluginList), string(mcpList), mcpGet)
	if getFailed != "" {
		checks = append(checks, doctorCheck{"mcp_scope", "WARN",
			fmt.Sprintf("cannot query MCP scope: %s", getFailed)})
	}
	return checks
}

// claudeAvailable reports whether the claude CLI is on PATH.
func claudeAvailable() bool {
	_, err := exec.LookPath("claude")
	return err == nil
}

// detectBeadlePlugin runs `claude plugin list` and reports whether the beadle
// plugin is installed and enabled. A CLI error is surfaced to stderr (a
// transient failure must not be silently read as "not installed", which would
// advise installing an already-present plugin) and then treated as not
// installed so install falls back to advising the user.
func detectBeadlePlugin() bool {
	out, err := exec.Command("claude", "plugin", "list").Output()
	if err != nil {
		fmt.Fprintf(os.Stderr, "note: could not query installed plugins: %v\n", err)
		return false
	}
	return beadlePluginInstalled(string(out))
}

// registerStandaloneMCP registers the standalone beadle-email MCP server at
// user scope with remove-before-add. The remove is `-s user`, so it refreshes
// only the USER-scope entry — discarding a stale binary path and making re-runs
// idempotent. A project-scope entry is NOT touched here; that drift is surfaced
// by `doctor` (mcp_scope), not healed by install.
func registerStandaloneMCP(binPath string) error {
	rm := exec.Command("claude", "mcp", "remove", "-s", "user", mcpServerName)
	rm.Stdout = os.Stderr
	rm.Stderr = os.Stderr
	if err := rm.Run(); err != nil {
		// Distinguish "server not registered" (claude ran and exited non-zero,
		// an *exec.ExitError — expected, stay quiet, matching install.sh which
		// silences its remove) from a genuine failure to run claude (surface the
		// actual error). The add below is authoritative either way.
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			fmt.Fprintf(os.Stderr, "note: could not remove existing user-scope %s: %v\n", mcpServerName, err)
		}
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
// server only when the caller opts in with --standalone. When --standalone is
// passed while the plugin is present, it warns about the duplicate before
// honoring the opt-in.
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
	case mcpRegisterStandaloneWarnDuplicate:
		fmt.Fprintf(os.Stderr,
			"WARNING: the beadle plugin already provides the MCP server; --standalone will create a duplicate.\n  Remove the plugin (claude plugin uninstall beadle@punt-labs) or drop --standalone.\n")
		fallthrough
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
