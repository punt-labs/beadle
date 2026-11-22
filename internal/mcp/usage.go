package mcp

import (
	"context"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/punt-labs/beadle/internal/usage"
)

func (h *handler) observeUsage(next server.ToolHandlerFunc) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		ctx, finish := h.usage.StartMCP(ctx, usage.Tool(request.Params.Name))
		result, err := next(ctx, request)
		outcome := usage.OK
		if err != nil || result == nil || result.IsError {
			outcome = usage.Error
		}
		finish(outcome)
		return result, err
	}
}
