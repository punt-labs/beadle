package mcp

import (
	"context"
	"fmt"

	mcplib "github.com/mark3labs/mcp-go/mcp"

	"github.com/punt-labs/beadle/internal/enable"
)

// enableTool is the Claude Code surface for beadle's enable/disable verbs
// (§2.14). Its action argument is an enum "enable" | "disable" — not an
// enabled:bool — so the one vocabulary carries across the CLI and the MCP tool.
// It writes the same <repo>/.punt-labs/beadle/enabled marker the CLI writes and
// never runs git: the working-tree change is committed through a PR like any
// other.
func enableTool() mcplib.Tool {
	return mcplib.NewTool("enable",
		mcplib.WithDescription(
			"Enable or disable beadle guidance in the current repo. "+
				"action=\"enable\" deposits the beadle user guide into .punt-labs/beadle/, "+
				"marks the repo enabled, and adds the @.punt-labs/beadle/CLAUDE.md import to "+
				"the repo CLAUDE.md. action=\"disable\" removes that import and the enabled "+
				"marker, leaving the directory dormant. Writes working-tree files only — it "+
				"does not run git, so commit the change through a PR.",
		),
		mcplib.WithString("action",
			mcplib.Required(),
			mcplib.Description("enable or disable"),
			mcplib.Enum("enable", "disable"),
		),
	)
}

func (h *handler) enable(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	action, err := req.RequireString("action")
	if err != nil {
		return mcplib.NewToolResultError("action is required"), nil
	}
	if action != "enable" && action != "disable" {
		return mcplib.NewToolResultError(fmt.Sprintf("action must be \"enable\" or \"disable\", got %q", action)), nil
	}

	root, err := h.repoRoot()
	if err != nil {
		return mcplib.NewToolResultError(err.Error()), nil
	}

	// nil progress: the MCP surface reports through the tool result, not stderr.
	if action == "enable" {
		if err := enable.Enable(root, nil); err != nil {
			return mcplib.NewToolResultError(fmt.Sprintf("enable: %v", err)), nil
		}
		return textResult(fmt.Sprintf("beadle enabled in %s\ncommit the .punt-labs/beadle/ marker and the CLAUDE.md import through a PR", root))
	}
	if err := enable.Disable(root, false, nil); err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("disable: %v", err)), nil
	}
	return textResult(fmt.Sprintf("beadle disabled in %s\ncommit the removed marker and import through a PR", root))
}
