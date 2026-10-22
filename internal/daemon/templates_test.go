package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"regexp"
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
// its args plus --help and requiring exit 0, a usage window anchored to a
// line whose trimmed form starts with the literal "Usage:", that window
// naming no subcommand placeholder, AND the configured path within that
// window catches the defect class several ways over, each closing a gap
// the previous one left open:
//
//   - exit 0 alone is not enough: a server that swallows --help and
//     starts also exits 0 on stdin EOF (measured on this host for both
//     ethos serve and beadle-email serve -- --help is not special-cased
//     by either binary's RunE, so cobra parses the flag, falls through to
//     the command body, and the body itself returns nil once its stdin
//     pipe reaches EOF, which CombinedOutput's pipe always eventually
//     does).
//   - matching the path anywhere in the whole output is not enough: with
//     flags banned from Args (below), a registry entry whose path is a
//     non-runnable GROUP -- e.g. {ethos, ["mission"]}, naming a cobra
//     command that only groups subcommands and never runs on its own --
//     exits 0 and its usage ("Usage:\n  ethos mission [command]") does
//     contain the path "ethos mission". The path check alone cannot
//     reject this: the window really does mention the configured path.
//     isLeafUsage is what catches it, by STRUCTURE rather than content:
//     cobra marks a non-leaf usage with the "[command]" placeholder and
//     click marks its root's with "COMMAND" -- either present means the
//     printed usage is an invitation to pick a subcommand, not help for
//     one specific command, whatever subcommand names happen to appear
//     in its path or its "Available Commands" prose. (A registry entry
//     naming an unknown NESTED subcommand under a real leaf, e.g.
//     {ethos, ["mission", "nosuch"]}, is instead rejected by the path
//     check alone once flags are banned: `ethos mission nosuch --help`
//     prints the SAME parent usage, "Usage:\n  ethos mission [command]",
//     which does not contain the configured path "ethos mission
//     nosuch" -- isLeafUsage plays no role in that particular case.)
//   - matching the path anywhere in the whole output is STILL not enough
//     even once the leaf check passes: usageLine anchors the match to
//     the usage window itself -- the line whose trimmed form starts with
//     "Usage:", joined with the next non-blank line only when nothing
//     already follows "Usage:" on that line (see usageLine's own doc
//     comment for why) -- so a path mentioned only in description prose
//     elsewhere in a real leaf's own --help cannot satisfy the check.
//   - matching "Usage:" anywhere in the output (via strings.Contains) is
//     not enough either: that would match the word mid-sentence in prose
//     appearing before the real usage block. Anchoring to a line whose
//     TRIMMED form STARTS WITH "Usage:" requires the literal word at the
//     front of some line, which urfave/cli ("USAGE:"), argparse
//     ("usage:"), and the standard library's flag package ("Usage of")
//     do not print -- a registry entry built on one of those frameworks
//     fails this test's no-usage-line branch rather than silently
//     matching nothing and passing vacuously.
//
// A stdio entry's Args must be the subcommand path only: no flags (a flag
// between, before, or after path elements is indistinguishable BY SHAPE
// from a path element -- nothing about `["mission", "--json", "nosuch"]`
// marks "--json" as not-a-path-segment except knowing ethos's own flag
// grammar, which this test does not and should not need to parse) and no
// empty elements. Every registry entry's Args is checked, element by
// element, before any of this runs. mcpCommandPath is therefore simply
// cmd plus every element of args, space-joined -- there is no flag to
// stop at, because the check above guarantees none can reach it.
//
// A command that blocks past the 5s deadline is reported as a timeout,
// not misread as a crash via *exec.ExitError's -1 "signal: killed" exit
// code -- ctx.Err() is checked first, since the same CombinedOutput error
// wraps both cases and only ctx.Err() tells them apart. The child's Cmd
// sets WaitDelay to 1s so a server that forks a child holding the pipes
// open (common for a daemon's double-fork) cannot block CombinedOutput
// indefinitely: WaitDelay starts its own countdown at whichever comes
// first, the context's deadline or the direct child exiting, and Wait
// returns once that countdown elapses rather than blocking on a
// descendant that never exits. (Verified during review with a fake
// binary that exits immediately after forking a lingering grandchild
// holding the pipes -- not a committed test.)
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

			for _, a := range cfg.Args {
				require.True(t, a != "" && a == strings.TrimSpace(a) && !strings.HasPrefix(a, "-"),
					"registry entry %q: Args %v contains %q -- Args is the subcommand path only, no flags and no empty or whitespace-only elements (a flag value is indistinguishable by shape from a path element, so no positional rule can single one out correctly)",
					name, cfg.Args, a)
			}

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
			window := usageLine(string(out))
			if window == "" {
				t.Fatalf("registry entry %q: %s %s exited 0 but printed no line starting with the literal \"Usage:\" -- a server that swallows --help and starts also exits 0 on stdin EOF, and a framework that prints a differently-cased or differently-worded usage header (urfave/cli's \"USAGE:\", argparse's \"usage:\", the standard library flag package's \"Usage of\") does not satisfy this:\n%s",
					name, cfg.Command, strings.Join(args, " "), out)
			}
			if !isLeafUsage(window) {
				t.Fatalf("registry entry %q: %s %s exited 0, but its usage window %q names a subcommand placeholder (\"[command]\" or \"COMMAND\") -- this is usually a non-runnable group's usage, inviting a subcommand choice rather than naming one specific command, but a LEAF command can legitimately print one of these words too (cobra's own \"help\" command, a click metavar, clap's USAGE line) -- if that is what failed here, the registry entry or this check needs a closer look, loudly, rather than this test silently assuming it is always the group case:\n%s",
					name, cfg.Command, strings.Join(args, " "), window, out)
			}
			if !strings.Contains(window, cmdPath) {
				t.Fatalf("registry entry %q: %s %s exited 0, but its usage window %q did not mention %q:\n%s",
					name, cfg.Command, strings.Join(args, " "), window, cmdPath, out)
			}
		})
	}
	require.Greater(t, exercised, 0, "no stdio entries were exercised -- an all-http or empty registry must not pass this test vacuously")
}

// mcpCommandPath returns the subcommand path a registry entry names: cmd
// followed by every element of args, space-joined, with NO trimming of
// its own. There is no flag to stop at, no empty element to skip, and no
// whitespace-only element to collapse -- TestDefaultMCPRegistry_StdioCommandsAreReal
// requires every element of Args to be non-empty, not whitespace-only,
// and free of a leading "-" before this helper is ever called, so args
// here is guaranteed to be pure subcommand path. A TrimSpace in this
// helper (an earlier version had one) would mask exactly the whitespace-
// only element that check exists to catch: {"serve", " "} would collapse
// to the same string "ethos serve" the real leaf {"serve"} produces, even
// though `ethos serve " "` is a different, failing invocation at runtime
// (exits 2, "unknown command"). The guard upstream is what must reject
// that shape; this helper must not quietly repair it.
func mcpCommandPath(cmd string, args []string) string {
	return strings.Join(append([]string{cmd}, args...), " ")
}

// ansiEscape matches a CSI (Control Sequence Introducer) escape sequence --
// ESC '[' followed by zero or more parameter/intermediate bytes (0x30-0x3f)
// and one final byte (0x40-0x7e), e.g. "\x1b[1m" (bold), "\x1b[1;33m"
// (bold yellow), "\x1b[0m" (reset). This is the sequence family terminal
// colour libraries emit; it is not a general ANSI/VT100 parser (OSC, DCS,
// and other escape families are out of scope), but it is everything a
// colour-capable terminal's SGR (Select Graphic Rendition) output uses.
var ansiEscape = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

// usageLine returns the usage window of out: the first line whose TRIMMED
// form starts with the literal "Usage:", optionally joined with the next
// non-blank line. Returns "" if no line's trimmed form starts with
// "Usage:" at all.
//
// ANSI escape sequences are stripped from the whole output before it is
// split into lines. A colour-capable terminal -- rich (ethos, Python's
// click-based biff) and similar libraries detect this per-stream, not
// per-host, so the same binary prints plain text to a pipe on one machine
// and SGR-coloured text to a pipe on another (observed: this test passed
// locally, where biff's "Usage: biff mcp [OPTIONS]" prints plain, and
// failed in CI, where the GitHub Actions runner's pipe is detected as
// colour-capable and the identical line prints as
// "\x1b[1m \x1b[0m\x1b[1;33mUsage: \x1b[0m\x1b[1mbiff mcp [OPTIONS]\x1b[0m" --
// the literal "Usage:" is no longer at the start of the trimmed line, it is
// embedded after a leading escape-coloured space, so HasPrefix missed it
// only in CI). Stripping escapes before the line scan means the anchor
// sees exactly the text a human reading the terminal would see, regardless
// of which host decided to colour it.
//
// Anchoring to a line START, not a Contains anywhere in the line, means a
// sentence that merely mentions the word "Usage:" in passing before the
// real usage block cannot be mistaken for it. The literal case-sensitive
// spelling also means a framework with a different usage header --
// urfave/cli's "USAGE:", argparse's "usage:", the standard library flag
// package's "Usage of" -- fails to produce a usage window at all, rather
// than one of those being silently matched as if it were this project's
// three cobra/click tools.
//
// The join to the next line happens ONLY when nothing follows "Usage:" on
// its own line: cobra prints "Usage:" alone with the subcommand path on
// the FOLLOWING line ("Usage:\n  ethos serve [flags]"), so the join is
// needed to see the path at all; click prints the path on the SAME line
// ("Usage: biff mcp [OPTIONS]"), so nothing follows "Usage:" that the
// join would need to add. Joining unconditionally, as an earlier version
// of this helper did, would instead append whatever random text sits on
// the next non-blank line after a click line that already has everything
// it needs -- pulling prose into the window and defeating the anchoring
// this helper exists to provide.
//
// The window is deliberately ONE line (or the "Usage:" line plus exactly
// one joined line) and never more, even though cobra prints TWO usage
// lines for a command that is both runnable AND a parent of
// subcommands -- "Usage:\n  x [flags]\n  x [command]". usageLine returns
// only the first ("x [flags]"), so the window never carries the
// "[command]" placeholder from the second line, and isLeafUsage treats
// such an entry as a leaf. That is deliberate, not an oversight: a
// runnable parent is a real server the registry entry is entitled to
// invoke, and an unknown word appended under it (e.g. a nested subcommand
// that does not exist) is still caught by the path check -- the parent's
// own usage, runnable or not, never contains a path with an extra unknown
// word appended. Widening this join to read a second usage line would
// reject every runnable-parent server's registry entry on the "[command]"
// placeholder alone, which is the opposite of what this test should do.
func usageLine(out string) string {
	lines := strings.Split(ansiEscape.ReplaceAllString(out, ""), "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "Usage:") {
			continue
		}
		if strings.TrimSpace(strings.TrimPrefix(trimmed, "Usage:")) != "" {
			return trimmed // click shape: the path is already on this line
		}
		result := trimmed
		for _, next := range lines[i+1:] {
			if strings.TrimSpace(next) == "" {
				continue
			}
			result += " " + strings.TrimSpace(next)
			break
		}
		return result
	}
	return ""
}

// isLeafUsage reports whether window -- the usage window usageLine
// returned -- names a specific leaf command rather than inviting the
// caller to pick a subcommand. cobra marks a non-leaf (root or parent)
// usage with the literal placeholder "[command]" (e.g.
// "Usage:\n  ethos mission [command]"); click marks its root's with
// "COMMAND" (e.g. "Usage: biff [OPTIONS] COMMAND [ARGS]..."). Either
// placeholder present means the printed usage is structurally a menu, not
// help for one subcommand.
//
// This is NOT what excludes an unknown nested subcommand under a real
// leaf (e.g. {ethos, ["mission", "nosuch"]}): once Args is banned from
// carrying flags, that case is already rejected by the path check alone,
// since the parent's usage window contains "ethos mission" but not the
// configured "ethos mission nosuch". What isLeafUsage uniquely catches is
// a registry entry whose configured path IS itself a non-runnable group
// -- e.g. {ethos, ["mission"]} -- where the path check cannot help,
// because the parent's own usage window genuinely does contain "ethos
// mission".
//
// An unknown ROOT subcommand is a different case again, and is not what
// this function is for: for these three tools it never produces a leaf
// vs. non-leaf ambiguity to resolve, because none of them exits 0 on
// one. Measured directly: ethos and biff both exit 2 (`ethos nosuch`,
// `biff nosuch`); beadle-email exits 1 (`beadle-email nosuch`). Nothing
// in this test enforces any particular exit code for that case -- it is
// cited here only to say what isLeafUsage does not need to handle.
func isLeafUsage(window string) bool {
	return !strings.Contains(window, "[command]") && !strings.Contains(window, "COMMAND")
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

// TestMCPCommandPath covers mcpCommandPath directly: cmd plus every
// element of args, space-joined, with no element skipped or treated as a
// terminator -- there is nothing left to parse around once
// TestDefaultMCPRegistry_StdioCommandsAreReal's per-element Args check has
// run.
func TestMCPCommandPath(t *testing.T) {
	tests := []struct {
		name string
		cmd  string
		args []string
		want string
	}{
		{name: "single subcommand", cmd: "ethos", args: []string{"serve"}, want: "ethos serve"},
		{name: "two-element subcommand path", cmd: "ethos", args: []string{"mission", "show"}, want: "ethos mission show"},
		{name: "bare command, no args", cmd: "sh", args: []string{}, want: "sh"},
		{name: "a whitespace-only element is NOT trimmed away", cmd: "ethos", args: []string{"serve", " "}, want: "ethos serve  "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, mcpCommandPath(tt.cmd, tt.args))
		})
	}
}

// TestUsageLine covers usageLine directly: a cobra leaf (the path on the
// line after "Usage:"), a click leaf (the path on the same line as
// "Usage:", nothing to join), a sample where the path appears only in
// description prose below the usage block (must not be returned), a line
// where "Usage:" appears mid-sentence BEFORE the real usage block (the
// real block, not the mid-sentence mention, must be returned), and a
// click line followed by prose (the prose must NOT be joined on, since
// click already has everything it needs on the Usage: line itself).
func TestUsageLine(t *testing.T) {
	tests := []struct {
		name string
		out  string
		want string
	}{
		{
			name: "cobra leaf: path on the line after Usage:",
			out:  "Start MCP server (stdio transport)\n\nUsage:\n  ethos serve [flags]\n\nFlags:\n  -h, --help   help for serve\n",
			want: "Usage: ethos serve [flags]",
		},
		{
			name: "click leaf: path on the same line as Usage:",
			out:  "Usage: biff mcp [OPTIONS]\n\n Start the biff MCP server (stdio transport).\n",
			want: "Usage: biff mcp [OPTIONS]",
		},
		{
			name: "path only in description prose below the usage block must not be returned",
			out:  "Usage:\n  ethos mission [command]\n\nAvailable Commands:\n  show  Show mission contract details\n",
			want: "Usage: ethos mission [command]",
		},
		{
			name: "Usage: mid-sentence before the real block must not be matched -- the real block must be",
			out:  "See the Usage: section of the manual for details.\n\nUsage:\n  ethos serve [flags]\n",
			want: "Usage: ethos serve [flags]",
		},
		{
			name: "click line followed by prose must not join the prose onto the window",
			out:  "Usage: biff mcp [OPTIONS]\nStart the biff MCP server (stdio transport).\n",
			want: "Usage: biff mcp [OPTIONS]",
		},
		{
			name: "no line starts with Usage: at all",
			out:  "USAGE: fakecli serve\n",
			want: "",
		},
		{
			// Exact shape observed from `biff mcp --help` on the GitHub
			// Actions runner: rich detects the pipe as colour-capable and
			// emits SGR escapes around a leading space and around "Usage: ",
			// pushing the literal "Usage:" past the start of the raw line.
			// Stripped of escapes this is the ordinary click leaf shape
			// ("Usage: biff mcp [OPTIONS]"), so the want is identical to
			// the plain click-leaf case above.
			name: "CI colour-capable pipe: ANSI-escaped Usage: line (biff mcp --help on GitHub Actions)",
			out:  "\x1b[1m \x1b[0m\x1b[1;33mUsage: \x1b[0m\x1b[1mbiff mcp [OPTIONS]\x1b[0m\n\n Start the biff MCP server (stdio transport).\n",
			want: "Usage: biff mcp [OPTIONS]",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, usageLine(tt.out))
		})
	}
}

// TestIsLeafUsage covers isLeafUsage directly: a cobra leaf's usage window
// (true), a cobra parent's window naming the "[command]" placeholder
// (false), and a click root's window naming the "COMMAND" placeholder
// (false).
func TestIsLeafUsage(t *testing.T) {
	tests := []struct {
		name   string
		window string
		want   bool
	}{
		{name: "cobra leaf", window: "Usage: ethos serve [flags]", want: true},
		{name: "cobra parent names [command]", window: "Usage: ethos mission [command]", want: false},
		{name: "click root names COMMAND", window: "Usage: biff [OPTIONS] COMMAND [ARGS]...", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isLeafUsage(tt.window))
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
