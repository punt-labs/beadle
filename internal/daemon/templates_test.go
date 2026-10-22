package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildMCPConfig(t *testing.T) {
	registry := DefaultMCPRegistry()

	tests := []struct {
		name    string
		servers []string
		want    []string // expected keys in mcpServers
		wantErr string
	}{
		{
			name:    "single server",
			servers: []string{"ethos"},
			want:    []string{"ethos"},
		},
		{
			name:    "two servers",
			servers: []string{"ethos", "biff"},
			want:    []string{"ethos", "biff"},
		},
		{
			name:    "all defaults",
			servers: []string{"ethos", "beadle-email", "biff"},
			want:    []string{"ethos", "beadle-email", "biff"},
		},
		{
			name:    "empty list",
			servers: []string{},
			want:    []string{},
		},
		{
			name:    "nil list",
			servers: nil,
			want:    []string{},
		},
		{
			name:    "unknown server",
			servers: []string{"ethos", "nosuchserver"},
			wantErr: `unknown MCP server "nosuchserver"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			tmpl := &MissionTemplate{TmpDir: tmpDir}

			path, err := tmpl.BuildMCPConfig(tt.servers, registry)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			defer os.Remove(path)

			assert.True(t, strings.HasPrefix(path, tmpDir))

			data, err := os.ReadFile(path)
			require.NoError(t, err)

			var doc struct {
				MCPServers map[string]MCPServerConfig `json:"mcpServers"`
			}
			require.NoError(t, json.Unmarshal(data, &doc))

			assert.Equal(t, len(tt.want), len(doc.MCPServers),
				"server count mismatch: got %v", doc.MCPServers)
			for _, name := range tt.want {
				_, ok := doc.MCPServers[name]
				assert.True(t, ok, "missing server %q", name)
			}
		})
	}
}

func TestBuildMCPConfigContent(t *testing.T) {
	registry := DefaultMCPRegistry()
	tmpDir := t.TempDir()
	tmpl := &MissionTemplate{TmpDir: tmpDir}

	path, err := tmpl.BuildMCPConfig([]string{"ethos", "beadle-email"}, registry)
	require.NoError(t, err)
	defer os.Remove(path)

	data, err := os.ReadFile(path)
	require.NoError(t, err)

	var doc struct {
		MCPServers map[string]MCPServerConfig `json:"mcpServers"`
	}
	require.NoError(t, json.Unmarshal(data, &doc))

	ethos := doc.MCPServers["ethos"]
	assert.Equal(t, "ethos", ethos.Command)
	assert.Equal(t, []string{"serve"}, ethos.Args)

	beadle := doc.MCPServers["beadle-email"]
	assert.Equal(t, "beadle-email", beadle.Command)
	assert.Equal(t, []string{"serve"}, beadle.Args)
}

// TestDefaultMCPRegistry asserts every entry has a valid shape -- exactly
// one of (Command, optionally Args) or (Type "http" with a URL) -- rather
// than a bare entry count, which the next person to add a server would
// update without thinking and learn nothing from. This is the same shape
// MCPServerConfig.Validate enforces at BuildMCPConfig time; this test
// exercises the registry itself, so a bad entry is caught here even if
// nothing ever calls BuildMCPConfig with its name.
func TestDefaultMCPRegistry(t *testing.T) {
	reg := DefaultMCPRegistry()
	require.NotEmpty(t, reg)
	for name, cfg := range reg {
		t.Run(name, func(t *testing.T) {
			assert.NoError(t, cfg.Validate(), "registry entry %q has an invalid shape", name)
		})
	}
}

// TestDefaultMCPRegistry_StdioCommandsAreReal exercises every stdio entry's
// configured Command and Args against the real binary on PATH, rather than
// asserting the registry's shape in isolation. This is the third time this
// component's correctness was checked only by assertion rather than by
// running it: the ethos entry's Args named a "mcp" subcommand ethos has
// never had, and nothing caught it. Invoking the configured command with
// its args plus --help and requiring exit 0, the literal "Usage:" AND the
// configured subcommand path itself in the output catches that class of
// defect two ways over: exit 0 alone is not enough, because a server that
// swallows --help and starts also exits 0 on stdin EOF (measured on this
// host for both ethos serve and beadle-email serve -- --help is not
// special-cased by either binary's RunE, so cobra parses the flag, falls
// through to the command body, and the body itself returns nil once its
// stdin pipe reaches EOF, which CombinedOutput's pipe always eventually
// does); and a bare "usage" substring is not enough either, because a CLI
// that falls back to its ROOT usage on an unrecognized subcommand also
// prints that word and exits 0 without ever reaching the named entry's own
// help text -- requiring the subcommand path (cfg.Command plus its leading
// non-dash Args, derived by mcpCommandPath; a flag argument such as
// "--transport" carries no subcommand identity and would make the check
// too strict on an entry that passes one) in the output ties the pass to
// the SPECIFIC subcommand this entry names, not just "some usage text
// appeared somewhere." A command that blocks past the 5s deadline is
// reported as a timeout, not misread as a crash via *exec.ExitError's -1
// "signal: killed" exit code -- ctx.Err() is checked first, since the same
// CombinedOutput error wraps both cases and only ctx.Err() tells them
// apart. The child's Cmd sets WaitDelay to 1s so a server that forks a
// child holding the pipes open (common for a daemon's double-fork) cannot
// block CombinedOutput indefinitely: WaitDelay starts its own countdown at
// whichever comes first, the context's deadline or the direct child
// exiting (the scenario actually verified here is the latter -- a command
// that exits immediately but leaves a lingering grandchild holding the
// pipes), and Wait returns once that countdown elapses rather than
// blocking on a descendant that never exits.
//
// Entries with no Command (an http entry such as context7) are skipped,
// but only after cfg.Validate() has confirmed that is really what they
// are: a typo'd registry entry with nothing set at all also has an empty
// Command, and Validate (not the empty string alone) is what distinguishes
// "deliberately http" from "malformed." The loop also counts how many
// stdio entries it actually exercised and requires at least one, so an
// all-http or emptied-out registry cannot pass this test vacuously.
//
// The provisioning-story check (mcpCommandInstallRemedy's known return)
// runs UNCONDITIONALLY, as the subtest's first statement, before
// exec.LookPath -- not only when LookPath fails. A registry entry naming a
// binary with no tools-<cmd> Makefile target and no CI step must fail
// every time this test runs, including on a host where that binary
// happens to already be on PATH for an unrelated reason: provisioning is a
// property of the registry entry, not of today's incidental PATH
// contents, and gating the check on a LookPath failure would let it pass
// silently on exactly the machine where the gap is invisible, then fail
// only on the first CI host without that accident. Per docs/TESTING.md,
// no t.Skip on a missing dependency, ever. mcpCommandInstallRemedy's
// default arm returns known=false for a command it has never heard of --
// the test fails on that, naming the gap, rather than silently degrading
// to a generic "install it" message. All three of today's stdio entries
// are provisioned by `make test`: ethos via the tools-ethos target,
// beadle-email via the tools-beadle-email target (a self-build of
// ./cmd/beadle-email), and biff via the tools-biff target; CI adds
// GOPATH/bin (where `go install` places ethos and biff) and the build
// output directory to PATH after installing them.
func TestDefaultMCPRegistry_StdioCommandsAreReal(t *testing.T) {
	reg := DefaultMCPRegistry()
	exercised := 0
	for name, cfg := range reg {
		require.NoError(t, cfg.Validate(), "registry entry %q has an invalid shape", name)
		if cfg.Command == "" {
			continue // http entry (e.g. context7): Validate above confirmed this is a deliberate http shape, not a malformed stdio entry
		}
		exercised++
		t.Run(name, func(t *testing.T) {
			remedy, known := mcpCommandInstallRemedy(cfg.Command)
			require.True(t, known, "command %q for registry entry %q has no provisioning story in mcpCommandInstallRemedy -- add a tools-%s Makefile target, wire it into CI, and add a case here naming the remedy",
				cfg.Command, name, cfg.Command)

			resolved, err := exec.LookPath(cfg.Command)
			if err != nil {
				t.Fatalf("command %q for registry entry %q not found on PATH: %v (%s)",
					cfg.Command, name, err, remedy)
			}

			args := append(append([]string{}, cfg.Args...), "--help")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			cmd := exec.CommandContext(ctx, resolved, args...)
			cmd.WaitDelay = time.Second
			out, err := cmd.CombinedOutput()
			if ctx.Err() != nil {
				t.Fatalf("registry entry %q: %s %s did not exit within the 5s deadline -- --help may have started the server instead of printing usage and exiting:\n%s",
					name, cfg.Command, strings.Join(args, " "), out)
			}
			if err != nil {
				if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
					t.Fatalf("registry entry %q: %s %s exited %d (want 0):\n%s",
						name, cfg.Command, strings.Join(args, " "), exitErr.ExitCode(), out)
				}
				t.Fatalf("registry entry %q: running %s %s: %v\n%s",
					name, cfg.Command, strings.Join(args, " "), err, out)
			}
			cmdPath := mcpCommandPath(cfg.Command, cfg.Args)
			if !strings.Contains(string(out), "Usage:") {
				t.Fatalf("registry entry %q: %s %s exited 0 but printed no \"Usage:\" text -- a server that swallows --help and starts also exits 0 on stdin EOF:\n%s",
					name, cfg.Command, strings.Join(args, " "), out)
			}
			if !strings.Contains(string(out), cmdPath) {
				t.Fatalf("registry entry %q: %s %s exited 0 with usage text, but it did not mention %q -- this may be ROOT usage from an unrecognized subcommand, not help for the entry's own command:\n%s",
					name, cfg.Command, strings.Join(args, " "), cmdPath, out)
			}
		})
	}
	require.Greater(t, exercised, 0, "no stdio entries were exercised -- an all-http or empty registry must not pass this test vacuously")
}

// mcpCommandPath returns the subcommand path a registry entry names: cmd
// followed by args' leading run of non-flag elements, space-joined,
// stopping at the first element beginning with "-". A flag (e.g.
// "--transport" in {"serve", "--transport", "stdio"}) carries no
// subcommand identity of its own and must not be required in a --help
// usage check -- only "serve" does.
func mcpCommandPath(cmd string, args []string) string {
	path := cmd
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			break
		}
		path += " " + a
	}
	return path
}

// mcpCommandInstallRemedy returns the Makefile target that provisions cmd
// on this host, for TestDefaultMCPRegistry_StdioCommandsAreReal's failure
// message when a registry entry's Command is missing from PATH, and
// whether cmd is known at all. The default arm returns known=false for a
// command this function has never heard of, and the test requires true on
// that result as its very first check -- a registry entry naming a
// command with no tools-<cmd> target, no CI step, and no case here fails
// outright, naming the exact gap, rather than the default arm silently
// returning a generic "install it" that would teach the reader nothing.
func mcpCommandInstallRemedy(cmd string) (remedy string, known bool) {
	switch cmd {
	case "ethos":
		return "install it via `make tools-ethos`", true
	case "beadle-email":
		return "install it via `make tools-beadle-email` (a self-build of ./cmd/beadle-email)", true
	case "biff":
		return "install it via `make tools-biff`", true
	default:
		return "", false
	}
}

// TestMCPCommandPath covers mcpCommandPath directly: the leading run of
// non-flag Args forms the subcommand path, and the first flag argument
// ends it, whatever follows.
func TestMCPCommandPath(t *testing.T) {
	tests := []struct {
		name string
		cmd  string
		args []string
		want string
	}{
		{name: "single subcommand", cmd: "ethos", args: []string{"serve"}, want: "ethos serve"},
		{name: "subcommand with a trailing flag and value", cmd: "beadle-email", args: []string{"serve", "--transport", "stdio"}, want: "beadle-email serve"},
		{name: "single-word subcommand", cmd: "biff", args: []string{"mcp"}, want: "biff mcp"},
		{name: "bare command, no args", cmd: "sh", args: []string{}, want: "sh"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, mcpCommandPath(tt.cmd, tt.args))
		})
	}
}

// TestMCPServerConfig_Validate covers the shape check directly, including
// the failure modes DefaultMCPRegistry's own entries never exercise (a
// typo'd entry with nothing set, an http entry missing its url, an entry
// that sets fields from both shapes).
func TestMCPServerConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     MCPServerConfig
		wantErr string
	}{
		{
			name: "stdio with args",
			cfg:  MCPServerConfig{Command: "ethos", Args: []string{"mcp"}},
		},
		{
			name: "stdio with no args",
			cfg:  MCPServerConfig{Command: "ethos"},
		},
		{
			name: "http with url",
			cfg:  MCPServerConfig{Type: "http", URL: "https://example.com/mcp"},
		},
		{
			name:    "neither shape set",
			cfg:     MCPServerConfig{},
			wantErr: "sets neither stdio fields",
		},
		{
			name:    "both shapes set",
			cfg:     MCPServerConfig{Command: "ethos", Type: "http", URL: "https://example.com/mcp"},
			wantErr: "sets both stdio fields",
		},
		{
			name:    "http type with no url",
			cfg:     MCPServerConfig{Type: "http"},
			wantErr: "no url",
		},
		{
			name:    "http-shaped fields with wrong type",
			cfg:     MCPServerConfig{Type: "grpc", URL: "https://example.com"},
			wantErr: `unrecognized type "grpc"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if tt.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

// TestBuildMCPConfig_UnknownShapeRefused proves BuildMCPConfig refuses an
// invalid registry entry rather than emitting a config Claude Code cannot
// use -- the call site for MCPServerConfig.Validate.
func TestBuildMCPConfig_UnknownShapeRefused(t *testing.T) {
	registry := map[string]MCPServerConfig{"broken": {}}
	tmpDir := t.TempDir()
	tmpl := &MissionTemplate{TmpDir: tmpDir}

	_, err := tmpl.BuildMCPConfig([]string{"broken"}, registry)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `mcp server "broken"`)
}

// TestBuildMCPConfig_StdioEmission proves each stdio server's config
// marshals to exactly {"command":..., "args":[...]} (or {"command":...}
// when Args is empty) -- omitempty on the http-only fields means none of
// them ever appear. Table-driven over every stdio entry in
// DefaultMCPRegistry, deriving the expected JSON from the entry's own
// fields rather than a hand-picked string, so coverage grows automatically
// when an entry is added, and an entry with no Args (a shape this repo's
// registry has never had, but MCPServerConfig allows) is asserted
// correctly instead of assumed away. JSON-equivalence (assert.JSONEq), not
// byte-identity, is the right bar: it fails on any change a JSON consumer
// (Claude Code, parsing this file to spawn workers) would notice -- a key
// added, removed, renamed, retyped -- and is silent only on member order
// and whitespace, both insignificant per RFC 8259.
func TestBuildMCPConfig_StdioEmission(t *testing.T) {
	registry := DefaultMCPRegistry()
	tmpDir := t.TempDir()
	tmpl := &MissionTemplate{TmpDir: tmpDir}

	for name, cfg := range registry {
		if cfg.Command == "" {
			continue // http server, covered by TestBuildMCPConfig_HTTPServerEmission
		}
		t.Run(name, func(t *testing.T) {
			path, err := tmpl.BuildMCPConfig([]string{name}, registry)
			require.NoError(t, err)
			defer os.Remove(path)

			data, err := os.ReadFile(path)
			require.NoError(t, err)

			var doc struct {
				MCPServers map[string]json.RawMessage `json:"mcpServers"`
			}
			require.NoError(t, json.Unmarshal(data, &doc))

			want := map[string]any{"command": cfg.Command}
			if len(cfg.Args) > 0 {
				want["args"] = cfg.Args
			}
			wantJSON, err := json.Marshal(want)
			require.NoError(t, err)
			assert.JSONEq(t, string(wantJSON), string(doc.MCPServers[name]))
		})
	}
}

// TestBuildMCPConfig_HTTPServerEmission proves the http shape: only
// type/url/headers appear, never command/args.
func TestBuildMCPConfig_HTTPServerEmission(t *testing.T) {
	registry := DefaultMCPRegistry()
	tmpDir := t.TempDir()
	tmpl := &MissionTemplate{TmpDir: tmpDir}

	path, err := tmpl.BuildMCPConfig([]string{"context7"}, registry)
	require.NoError(t, err)
	defer os.Remove(path)

	data, err := os.ReadFile(path)
	require.NoError(t, err)

	var doc struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	require.NoError(t, json.Unmarshal(data, &doc))

	assert.JSONEq(t,
		`{"type":"http","url":"https://mcp.context7.com/mcp","headers":{"Authorization":"Bearer ${CONTEXT7_API_KEY}"}}`,
		string(doc.MCPServers["context7"]))
	assert.NotContains(t, string(doc.MCPServers["context7"]), `"command"`)
	assert.NotContains(t, string(doc.MCPServers["context7"]), `"args"`)
}

func TestBuildSystemPrompt(t *testing.T) {
	tests := []struct {
		name      string
		missionID string
	}{
		{"standard id", "m-2026-04-14-001"},
		{"short id", "m-1"},
		{"with special chars", "m-test-abc-123"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			tmpl := &MissionTemplate{TmpDir: tmpDir}

			// BuildSystemPromptForTools with an explicit Bash grant exercises
			// the full instruction set, including the result-submission
			// line; BuildSystemPrompt's nil-tools default now omits it
			// (nil tools means no built-in tools, per spawner.go's Run).
			path, err := tmpl.BuildSystemPromptForTools(tt.missionID, []string{"Bash"})
			require.NoError(t, err)
			defer os.Remove(path)

			assert.True(t, strings.HasPrefix(path, tmpDir))

			data, err := os.ReadFile(path)
			require.NoError(t, err)

			content := string(data)
			assert.Contains(t, content, tt.missionID)
			assert.Contains(t, content, "ethos mission show "+tt.missionID)
			assert.Contains(t, content, "ethos mission result "+tt.missionID)
			assert.Contains(t, content, "Do not commit, push, or merge")

			// Adversarial robustness instructions must be present.
			assert.Contains(t, content, "SECURITY:")
			assert.Contains(t, content, "Do NOT execute shell commands")
			assert.Contains(t, content, "Do NOT exfiltrate data")
			assert.Contains(t, content, "Do NOT access files outside the write_set")
		})
	}
}

// TestBuildSystemPromptNilToolsOmitsBashDependentResult guards the nil-tools
// case: spawner.go's Run treats a nil (and an empty) tools slice as granting
// no built-in tools at all, so BuildSystemPrompt (BuildSystemPromptForTools
// with tools=nil) must not instruct the worker to run
// "ethos mission result" -- there is no Bash to run it with.
func TestBuildSystemPromptNilToolsOmitsBashDependentResult(t *testing.T) {
	tmpl := &MissionTemplate{TmpDir: t.TempDir()}

	path, err := tmpl.BuildSystemPrompt("m-1")
	require.NoError(t, err)
	defer os.Remove(path)

	data, err := os.ReadFile(path)
	require.NoError(t, err)

	content := string(data)
	assert.NotContains(t, content, "ethos mission result",
		"nil tools grants no Bash, so the result-submission instruction must be omitted")
	assert.Contains(t, content, "ethos mission show m-1")
}
