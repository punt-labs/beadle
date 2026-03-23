package mcp

import (
	"context"
	"fmt"
	"strings"

	mcplib "github.com/mark3labs/mcp-go/mcp"

	"github.com/punt-labs/beadle/internal/contacts"
	"github.com/punt-labs/beadle/internal/identity"
	"github.com/punt-labs/beadle/internal/paths"
	"github.com/punt-labs/beadle/internal/session"
)

// --- switch_identity tool ---

func switchIdentityTool() mcplib.Tool {
	return mcplib.NewTool("switch_identity",
		mcplib.WithDescription(
			"Switch the active beadle identity for this session. "+
				"Pass an ethos handle (e.g. 'jfreeman') to operate as that identity, "+
				"or pass an empty string to reset to the default agent identity. "+
				"Requires ethos identity files. Use whoami to see available identities.",
		),
		mcplib.WithString("handle",
			mcplib.Description("Ethos handle to switch to (e.g. 'jfreeman'). Empty string resets to default."),
		),
	)
}

func (h *handler) switchIdentity(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	handle := stringParam(req, "handle", "")

	// Reset to default.
	if handle == "" {
		h.overrideMu.Lock()
		h.identityOverride = nil
		h.overrideMu.Unlock()

		// Resolve the default to show the user what they're resetting to.
		defaultID, err := h.resolver.Resolve()
		if err != nil {
			return textResult("identity reset to default (could not resolve default: " + err.Error() + ")")
		}
		return textResult(fmt.Sprintf("identity reset to default: %s (%s)", defaultID.Handle, defaultID.Email))
	}

	// Validate and resolve the requested handle.
	if err := identity.ValidateHandle(handle); err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("invalid handle: %v", err)), nil
	}

	id, err := h.resolver.ResolveHandle(handle)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("resolve identity %q: %v", handle, err)), nil
	}

	h.overrideMu.Lock()
	h.identityOverride = id
	h.overrideMu.Unlock()

	return textResult(fmt.Sprintf("switched to %s (%s)", id.Handle, id.Email))
}

// --- whoami tool (moved from tools.go, enhanced with override + roster) ---

func whoamiTool() mcplib.Tool {
	return mcplib.NewTool("whoami",
		mcplib.WithDescription(
			"Show the active beadle identity: email, handle, source, and contacts path. "+
				"If identity was switched, shows override source. "+
				"Lists session participants when available.",
		),
	)
}

func (h *handler) whoami(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	// Resolve active identity (respects override).
	h.overrideMu.RLock()
	override := h.identityOverride
	h.overrideMu.RUnlock()

	var id *identity.Identity
	var err error
	if override != nil {
		id = override
	} else {
		id, err = h.resolver.Resolve()
		if err != nil {
			return mcplib.NewToolResultError(fmt.Sprintf("identity resolution failed: %v", err)), nil
		}
	}

	contactsPath, contactsErr := paths.IdentityContactsPath(id.Email)

	lines := []string{
		fmt.Sprintf("   %-16s %s", "email:", id.Email),
	}

	// Show source — indicate if switched.
	if override != nil {
		defaultID, _ := h.resolver.Resolve()
		defaultHandle := "unknown"
		if defaultID != nil {
			defaultHandle = defaultID.Handle
		}
		lines = append(lines, fmt.Sprintf("   %-16s override (switched from %s)", "source:", defaultHandle))
	} else {
		lines = append(lines, fmt.Sprintf("   %-16s %s", "source:", id.Source))
	}

	if id.Handle != "" {
		lines = append(lines, fmt.Sprintf("   %-16s %s", "handle:", id.Handle))
	}
	if id.Name != "" {
		lines = append(lines, fmt.Sprintf("   %-16s %s", "name:", id.Name))
	}
	if contactsErr != nil {
		lines = append(lines, fmt.Sprintf("   %-16s error: %v", "contacts:", contactsErr))
	} else {
		store := contacts.NewStore(contactsPath)
		if loadErr := store.Load(); loadErr != nil {
			lines = append(lines, fmt.Sprintf("   %-16s %s (error: %v)", "contacts:", contactsPath, loadErr))
		} else {
			lines = append(lines, fmt.Sprintf("   %-16s %s (%d contacts)", "contacts:", contactsPath, store.Count()))
		}
	}

	// Append session participants if available.
	if h.ethosDir != "" {
		roster, _ := session.ReadRoster(h.ethosDir)
		if roster != nil && len(roster.Participants) > 0 {
			lines = append(lines, "", "   session participants:")
			for _, p := range roster.Participants {
				role := "agent"
				if p.IsHuman() {
					role = "human"
				}
				persona := p.Persona
				if persona == "" {
					persona = p.AgentID
				}
				lines = append(lines, fmt.Sprintf("   %-16s %s (%s)", "  "+persona+":", role, "switch_identity handle="+persona))
			}
		}
	}

	return textResult(strings.Join(lines, "\n"))
}
