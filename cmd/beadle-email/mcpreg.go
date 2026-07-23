package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// mcpServerName is the standalone MCP server name beadle-email registers.
// The marketplace plugin registers the same server as "plugin:beadle:email"
// from its plugin.json; the two must never coexist (see decideMCP).
const mcpServerName = "beadle-email"

// pluginState is what we know about the beadle marketplace plugin. It is a
// tri-state-plus: absent and disabled are distinct (a disabled plugin IS
// installed, just inactive), and unknown records that the query itself failed —
// which must never be silently downgraded to "absent".
type pluginState int

const (
	pluginAbsent   pluginState = iota // confirmed not installed
	pluginEnabled                     // installed and enabled — an active MCP source
	pluginDisabled                    // installed but disabled — inactive
	pluginUnknown                     // could not determine (the query failed)
)

// mcpDecision is the outcome of the install command's MCP-registration policy.
// The beadle plugin (plugin.json declares the "email" mcpServer) is the single
// automatic MCP registration; install adds a standalone server only for a
// genuine no-plugin machine.
type mcpDecision int

const (
	// mcpPluginProvides: the beadle plugin is installed and enabled and already
	// registers the MCP server, so install does nothing for MCP.
	mcpPluginProvides mcpDecision = iota
	// mcpRegisterStandalone: register a standalone user-scope server with
	// remove-before-add. Chosen on --standalone opt-in when the plugin is a
	// confirmed non-source (absent or installed-but-disabled).
	mcpRegisterStandalone
	// mcpRegisterStandaloneWarnDuplicate: --standalone was passed while the
	// plugin is an active source (enabled) OR its state could not be confirmed.
	// The explicit opt-in is honored, but registering may/will duplicate the
	// plugin's server, so a prominent warning precedes the add — never silent.
	mcpRegisterStandaloneWarnDuplicate
	// mcpAdviseInstall: no active plugin and no opt-in — advise installing the
	// plugin or re-running with --standalone. Register nothing.
	mcpAdviseInstall
)

// decideMCP applies the single-source POLICY for install. An enabled plugin is
// the active MCP source, so install stays hands-off. A standalone server is
// registered only on --standalone. When --standalone is passed while the plugin
// is enabled (a definite duplicate) OR its state is unknown (an unconfirmed
// possible duplicate), the opt-in still wins but routes through a warn-first
// path rather than registering silently. A disabled plugin is treated as a
// non-source for policy purposes: --standalone is legitimate, no warning.
func decideMCP(state pluginState, standalone bool) mcpDecision {
	switch {
	case standalone && (state == pluginEnabled || state == pluginUnknown):
		return mcpRegisterStandaloneWarnDuplicate
	case standalone:
		return mcpRegisterStandalone
	case state == pluginEnabled:
		return mcpPluginProvides
	default:
		return mcpAdviseInstall
	}
}

// duplicateWarning is the message printed before honoring --standalone on the
// warn-first path. It distinguishes a confirmed active plugin from an
// unconfirmed one (the plugin query failed) so the operator knows which risk
// they are accepting.
func duplicateWarning(state pluginState) string {
	if state == pluginUnknown {
		return "WARNING: could not confirm the beadle plugin is absent; --standalone may create a duplicate MCP registration.\n" +
			"  Verify with `claude plugin list`, then remove the plugin or drop --standalone.\n"
	}
	return "WARNING: the beadle plugin already provides the MCP server; --standalone will create a duplicate.\n" +
		"  Remove the plugin (claude plugin uninstall beadle@punt-labs) or drop --standalone.\n"
}

// beadlePluginState reports the beadle plugin's state from `claude plugin list`
// output. The plugin header appears as "beadle@<marketplace>", followed a few
// lines later by "Status: ✔ enabled" or "Status: ✘ disabled". This never
// returns pluginUnknown — that value is reserved for a failed query (see
// detectPluginState).
func beadlePluginState(pluginList string) pluginState {
	inBeadleBlock := false
	sawBeadle := false
	for _, line := range strings.Split(pluginList, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.Contains(line, "beadle@"):
			inBeadleBlock = true
			sawBeadle = true
		case inBeadleBlock && strings.HasPrefix(trimmed, "Status:"):
			if strings.Contains(trimmed, "enabled") {
				return pluginEnabled
			}
			return pluginDisabled
		case inBeadleBlock && strings.Contains(trimmed, "@"):
			// The next plugin's header before beadle's Status line — a malformed
			// block. Stop treating following lines as beadle's.
			inBeadleBlock = false
		}
	}
	if sawBeadle {
		// Header seen but no Status marker parsed; treat as installed-and-active.
		return pluginEnabled
	}
	return pluginAbsent
}

// standaloneMCPRegistered reports whether `claude mcp list` output contains a
// standalone beadle-email server. A standalone entry is named "beadle-email";
// the plugin's server is "plugin:beadle:email", so a line beginning with
// "beadle-email:" is unambiguously the standalone duplicate. The mcp-list entry
// is the RESOLVED (user-scope) registration — hence the coexistence remedy is
// always `-s user` (see mcpRegistrationCheck).
func standaloneMCPRegistered(mcpList string) bool {
	prefix := mcpServerName + ":"
	for _, line := range strings.Split(mcpList, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), prefix) {
			return true
		}
	}
	return false
}

// mcpConfig is the subset of a .mcp.json file that matters for drift detection:
// the mcpServers map. Server definitions are ignored — the presence of the key
// is what marks a project-scope registration.
type mcpConfig struct {
	MCPServers map[string]json.RawMessage `json:"mcpServers"`
}

// mcpFileDeclaresServer reports whether the .mcp.json at path declares a server
// with the given name. A missing file is not an error (returns false); a
// malformed file IS an error so the caller can surface it rather than silently
// treating drift as absent.
func mcpFileDeclaresServer(path, server string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("read %s: %w", path, err)
	}
	var cfg mcpConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return false, fmt.Errorf("parse %s: %w", path, err)
	}
	_, ok := cfg.MCPServers[server]
	return ok, nil
}

// projectScopeMCPFile scans .mcp.json from startDir up to the filesystem root
// and returns the path of the first file that declares a beadle-email server,
// or "" if none. Project-scope MCP servers live in .mcp.json (mcpServers keyed
// by name); Claude Code resolves the nearest one walking up from the working
// directory. Scanning the files directly detects a project-scope entry even
// when a USER-scope entry shadows it in `claude mcp get`, and works without the
// claude CLI (it is a plain file check).
func projectScopeMCPFile(startDir string) (string, error) {
	dir := startDir
	for {
		path := filepath.Join(dir, ".mcp.json")
		found, err := mcpFileDeclaresServer(path, mcpServerName)
		if err != nil {
			return "", err
		}
		if found {
			return path, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", nil // reached the filesystem root
		}
		dir = parent
	}
}

// currentProjectScopeFile scans for a project-scope beadle-email registration
// starting from the working directory. It is CLI-independent, so it runs even
// when the claude CLI is absent or its queries fail.
func currentProjectScopeFile() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getwd: %w", err)
	}
	return projectScopeMCPFile(cwd)
}

// mcpRegistrationCheck builds the standalone-vs-plugin coexistence check — one
// of three INDEPENDENT doctor observations. It reports whether an active
// (enabled) plugin and a standalone server both exist, and it accurately
// diagnoses a disabled-but-installed plugin. It does NOT consult project scope:
// the standalone signal comes from `claude mcp list`, which surfaces the
// RESOLVED user-scope entry, so the remedy is always `claude mcp remove -s user`.
// Project-scope drift is a separate observation (projectScopeCheck) with its
// own `-s project` remedy.
func mcpRegistrationCheck(state pluginState, standalone bool) doctorCheck {
	switch {
	case state == pluginEnabled && standalone:
		return doctorCheck{"mcp_registration", "WARN",
			"standalone beadle-email server coexists with the enabled beadle plugin (duplicate) — remove it: claude mcp remove -s user beadle-email"}
	case state == pluginEnabled:
		return doctorCheck{"mcp_registration", "OK", "plugin provides the MCP server (no standalone duplicate)"}
	case state == pluginDisabled && standalone:
		return doctorCheck{"mcp_registration", "OK", "standalone server active; beadle plugin is installed but disabled"}
	case state == pluginDisabled:
		return doctorCheck{"mcp_registration", "OK", "beadle plugin is installed but disabled; no standalone server"}
	case standalone:
		return doctorCheck{"mcp_registration", "OK", "standalone server (plugin not installed)"}
	default:
		return doctorCheck{"mcp_registration", "OK", "no beadle MCP registration found"}
	}
}

// projectScopeCheck builds the project-scope drift check — an INDEPENDENT
// observation from the .mcp.json file scan, unrelated to the coexistence check.
// It is pure. A found project-scope file is a WARN naming the file and the
// correct `-s project` remove; a scan error is a WARN (a failed scan must not
// read as "no drift"); no project entry returns nil.
func projectScopeCheck(projectScopeFile string, scanErr error) *doctorCheck {
	switch {
	case scanErr != nil:
		return &doctorCheck{"mcp_scope", "WARN", fmt.Sprintf("cannot determine MCP project scope: %v", scanErr)}
	case projectScopeFile != "":
		return &doctorCheck{"mcp_scope", "WARN",
			fmt.Sprintf("beadle-email is registered at project scope in %s — remove it: claude mcp remove -s project beadle-email (use user scope only)", projectScopeFile)}
	default:
		return nil
	}
}

// inspectMCPRegistration returns the three INDEPENDENT MCP-drift observations as
// doctor checks. The coexistence check (mcp_registration) uses the claude CLI;
// the project-scope check (mcp_scope) is a CLI-independent .mcp.json scan. No
// check consults another's signal — a CLI failure, an absent claude, or a
// shadowing user-scope entry cannot hide the project-scope leak, and the
// project scope never rewrites the coexistence remedy.
func inspectMCPRegistration() []doctorCheck {
	projectFile, scanErr := currentProjectScopeFile()

	checks := registrationChecks()
	if sc := projectScopeCheck(projectFile, scanErr); sc != nil {
		checks = append(checks, *sc)
	}
	return checks
}

// registrationChecks returns the coexistence check. It needs the claude CLI:
// when claude is absent it returns nil (the caller still runs the
// CLI-independent project-scope check), and a query failure becomes a WARN.
func registrationChecks() []doctorCheck {
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
	return []doctorCheck{mcpRegistrationCheck(beadlePluginState(string(pluginList)), standaloneMCPRegistered(string(mcpList)))}
}

// claudeAvailable reports whether the claude CLI is on PATH.
func claudeAvailable() bool {
	_, err := exec.LookPath("claude")
	return err == nil
}

// detectPluginState runs `claude plugin list` and reports the beadle plugin's
// state. A query error is surfaced to stderr and returns pluginUnknown — NOT
// pluginAbsent — so a transient failure cannot be silently read as "not
// installed" (which would let --standalone create a duplicate with no warning).
func detectPluginState() pluginState {
	out, err := exec.Command("claude", "plugin", "list").Output()
	if err != nil {
		fmt.Fprintf(os.Stderr, "note: could not query installed plugins: %v\n", err)
		return pluginUnknown
	}
	return beadlePluginState(string(out))
}

// registerStandaloneMCP registers the standalone beadle-email MCP server at
// user scope with remove-before-add. The remove is `-s user`, so it refreshes
// only the USER-scope entry — discarding a stale binary path and making re-runs
// idempotent. A project-scope entry is NOT touched here; that drift is surfaced
// by `doctor` (mcp_scope), not healed by install.
func registerStandaloneMCP(binPath string) error {
	rm := exec.Command("claude", "mcp", "remove", "-s", "user", mcpServerName)
	// Discard the remove's output: the expected-absent case (no user-scope
	// entry yet) makes claude print an error we do not want surfaced. A genuine
	// (non-ExitError) failure to run claude is reported explicitly below.
	rm.Stdout = io.Discard
	rm.Stderr = io.Discard
	if err := rm.Run(); err != nil {
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
// hands-off when an enabled plugin is present and registers a standalone
// user-scope server only when the caller opts in with --standalone. When
// --standalone is passed while the plugin is an active source (or its state
// cannot be confirmed), it warns about the possible duplicate before honoring
// the opt-in.
func setupMCPRegistration(standalone bool) error {
	if !claudeAvailable() {
		fmt.Fprintf(os.Stderr,
			"claude CLI not found — install the beadle plugin, or register manually:\n  claude mcp add -s user %s -- %s serve\n",
			mcpServerName, selfPath())
		return nil
	}

	state := detectPluginState()
	switch decideMCP(state, standalone) {
	case mcpPluginProvides:
		fmt.Fprintln(os.Stderr, "MCP server provided by the beadle plugin — no standalone registration needed")
		return nil
	case mcpAdviseInstall:
		fmt.Fprintf(os.Stderr,
			"beadle plugin not installed. Install it (recommended):\n  claude plugin install beadle@punt-labs --scope user\nOr register a standalone server for a no-plugin setup:\n  %s install --standalone\n",
			selfPath())
		return nil
	case mcpRegisterStandaloneWarnDuplicate:
		fmt.Fprint(os.Stderr, duplicateWarning(state))
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
