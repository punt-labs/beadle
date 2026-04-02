package mcp

import (
	"context"
	"fmt"
	"path/filepath"

	mcplib "github.com/mark3labs/mcp-go/mcp"

	"github.com/punt-labs/beadle/internal/email"
)

func setPollIntervalTool() mcplib.Tool {
	return mcplib.NewTool("set_poll_interval",
		mcplib.WithDescription(
			"Set the background inbox polling interval. "+
				"The server checks INBOX periodically and sends a notification when new mail arrives. "+
				"Valid intervals: 5m, 10m, 15m, 30m, 1h, 2h. Use 'n' to disable.",
		),
		mcplib.WithString("interval",
			mcplib.Required(),
			mcplib.Description("Polling interval: 5m, 10m, 15m, 30m, 1h, 2h, or n (disable)"),
		),
	)
}

func getPollStatusTool() mcplib.Tool {
	return mcplib.NewTool("get_poll_status",
		mcplib.WithDescription("Show background inbox poller state: interval, active, last check time, unread count."),
	)
}

func (h *handler) setPollInterval(_ context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	interval, err := req.RequireString("interval")
	if err != nil {
		return mcplib.NewToolResultError("interval is required"), nil
	}

	if !email.ValidPollInterval(interval) {
		return mcplib.NewToolResultError(
			fmt.Sprintf("invalid interval %q: must be 5m, 10m, 15m, 30m, 1h, 2h, or n", interval),
		), nil
	}

	// Persist to config file.
	_, cfg, idDir, err := h.resolveIdentityAndConfig()
	if err != nil {
		return mcplib.NewToolResultError(err.Error()), nil
	}
	cfg.PollInterval = interval
	configPath := filepath.Join(idDir, "email.json")
	if saveErr := email.SaveConfig(configPath, cfg); saveErr != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("save config: %v", saveErr)), nil
	}

	if err := h.poller.SetInterval(interval); err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("set interval: %v", err)), nil
	}

	if interval == "n" || interval == "" {
		return textResult("polling disabled")
	}
	return textResult(fmt.Sprintf("polling set to %s", interval))
}

func (h *handler) getPollStatus(_ context.Context, _ mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	st := h.poller.Status()
	return textResult(formatPollStatus(st))
}
