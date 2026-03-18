package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
)

// globalOpts holds parsed global flags that apply to all subcommands.
type globalOpts struct {
	JSON    bool // --json / -j: output JSON to stdout
	Verbose bool // --verbose / -v: debug logging
	Quiet   bool // --quiet / -q: errors only
}

// parseGlobalOpts extracts global flags from args before the subcommand,
// returning the opts and remaining args with global flags stripped.
// Only parses flags before the first non-flag token (the subcommand).
func parseGlobalOpts(args []string) (globalOpts, []string) {
	var g globalOpts
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json", "-j":
			g.JSON = true
		case "--verbose", "-v":
			g.Verbose = true
		case "--quiet", "-q":
			g.Quiet = true
		default:
			// First non-global-flag token — return it and everything after
			return g, args[i:]
		}
	}
	return g, nil
}

// slogLevel returns the appropriate log level based on flags.
func (g globalOpts) slogLevel() slog.Level {
	if g.Verbose {
		return slog.LevelDebug
	}
	if g.Quiet {
		return slog.LevelWarn
	}
	if os.Getenv("BEADLE_DEBUG") != "" {
		return slog.LevelDebug
	}
	return slog.LevelInfo
}

// printJSON marshals v as indented JSON to stdout.
func (g globalOpts) printJSON(v any) {
	data, _ := json.MarshalIndent(v, "", "  ")
	fmt.Println(string(data))
}

// printResult outputs v as JSON when --json is set, otherwise calls humanFn.
func (g globalOpts) printResult(v any, humanFn func()) {
	if g.JSON {
		g.printJSON(v)
		return
	}
	if !g.Quiet {
		humanFn()
	}
}

// errorf prints an error to stderr.
func (g globalOpts) errorf(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", a...)
}
