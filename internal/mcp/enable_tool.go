package mcp

import (
	"context"
	"fmt"
	"strings"

	mcplib "github.com/mark3labs/mcp-go/mcp"

	"github.com/punt-labs/beadle/internal/enable"
)

// action is the enable tool's verb — the one vocabulary shared with the CLI
// (§2.14), not an enabled:bool. The two consts are the single source for the
// tool's Enum values, its validation, and its dispatch, so a typo cannot slip
// in at one use site and disagree with another.
type action string

const (
	actionEnable  action = "enable"
	actionDisable action = "disable"
)

// enableTool is the Claude Code surface for beadle's enable/disable verbs
// (§2.14). It writes the same <repo>/.punt-labs/beadle/enabled marker the CLI
// writes and never runs git: the working-tree change is committed through a PR
// like any other.
func enableTool() mcplib.Tool {
	return mcplib.NewTool("enable",
		mcplib.WithDescription(
			"Enable or disable beadle guidance in the current repo. "+
				"action=\"enable\" deposits the beadle user guide into .punt-labs/beadle/, "+
				"marks the repo enabled, and adds the @.punt-labs/beadle/CLAUDE.md import to "+
				"the repo CLAUDE.md, and allows beadle's tools in the repo's .claude/settings.json. "+
				"action=\"disable\" removes that import, those rules and the enabled "+
				"marker, leaving the directory dormant. Writes working-tree files only — it "+
				"does not run git, so commit the change through a PR.",
		),
		mcplib.WithString("action",
			mcplib.Required(),
			mcplib.Description("enable or disable"),
			mcplib.Enum(string(actionEnable), string(actionDisable)),
		),
	)
}

func (h *handler) enable(_ context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	raw, err := req.RequireString("action")
	if err != nil {
		return mcplib.NewToolResultError("action is required"), nil
	}
	act := action(raw)
	if act != actionEnable && act != actionDisable {
		return mcplib.NewToolResultError(fmt.Sprintf("action must be %q or %q, got %q", actionEnable, actionDisable, raw)), nil
	}

	// A handler built outside RegisterTools has no resolver; guard rather than
	// panic on a nil func.
	if h.repoRoot == nil {
		return mcplib.NewToolResultError("repo-root resolver not configured"), nil
	}
	root, err := h.repoRoot()
	if err != nil {
		return mcplib.NewToolResultError(err.Error()), nil
	}

	// nil progress: the MCP surface reports through the tool result, not stderr.
	var report progressLog
	if act == actionEnable {
		if err := enable.Enable(root, report.printf); err != nil {
			return mcplib.NewToolResultError(fmt.Sprintf("enable: %v", err)), nil
		}
		return textResult(fmt.Sprintf("%sbeadle enabled in %s\ncommit the .punt-labs/beadle/ marker, the CLAUDE.md import and .claude/settings.json through a PR", report.permissions(), root))
	}
	if err := enable.Disable(root, false, report.printf); err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("disable: %v", err)), nil
	}
	return textResult(fmt.Sprintf("%sbeadle disabled in %s\ncommit the removed marker, import and permission rules through a PR", report.permissions(), root))
}

// progressLog collects enable/disable progress so the tool result can repeat
// the permission rules changed, which the CLI prints to stderr.
type progressLog struct{ lines []string }

func (l *progressLog) printf(format string, a ...any) {
	l.lines = append(l.lines, fmt.Sprintf(format, a...))
}

func (l *progressLog) permissions() string {
	var b strings.Builder
	for _, line := range l.lines {
		if strings.Contains(line, ".claude/settings.json") {
			b.WriteString(line)
		}
	}
	return b.String()
}
