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
