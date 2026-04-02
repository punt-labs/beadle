package mcp

import (
	"context"
	"errors"
	"fmt"
	"os"

	mcplib "github.com/mark3labs/mcp-go/mcp"

	"github.com/punt-labs/beadle/internal/email"
	"github.com/punt-labs/beadle/internal/paths"
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

	// Persist to the default identity's config — the same path the poller
	// reads on restart. We bypass session identity overrides because the
	// poller always runs as the default identity.
	id, err := h.resolver.Resolve()
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("resolve identity: %v", err)), nil
	}
	configPath, err := paths.IdentityConfigPath(id.Email)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("identity path: %v", err)), nil
	}
	cfg, loadErr := email.LoadConfig(configPath)
	if loadErr != nil {
		if !errors.Is(loadErr, os.ErrNotExist) {
			return mcplib.NewToolResultError(fmt.Sprintf("identity config %s: %v", configPath, loadErr)), nil
		}
		cfg, loadErr = email.LoadConfig(email.DefaultConfigPath())
		if loadErr != nil {
			return mcplib.NewToolResultError(fmt.Sprintf("load config: %v", loadErr)), nil
		}
		configPath = email.DefaultConfigPath()
	}
	cfg.PollInterval = interval
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
