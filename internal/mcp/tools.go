// Package mcp defines the MCP tool surface for Beadle's email channel.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/punt-labs/beadle/internal/channel"
	"github.com/punt-labs/beadle/internal/email"
	"github.com/punt-labs/beadle/internal/pgp"
)

// RegisterTools adds all email channel tools to the MCP server.
func RegisterTools(s *server.MCPServer, cfg *email.Config, logger *slog.Logger) {
	h := &handler{cfg: cfg, logger: logger}

	s.AddTool(listMessagesTool(), h.listMessages)
	s.AddTool(readMessageTool(), h.readMessage)
	s.AddTool(listFoldersTool(), h.listFolders)
	s.AddTool(sendEmailTool(), h.sendEmail)
	s.AddTool(verifySignatureTool(), h.verifySignature)
	s.AddTool(showMIMETool(), h.showMIME)
	s.AddTool(checkTrustTool(), h.checkTrust)
	s.AddTool(moveMessageTool(), h.moveMessage)
}

type handler struct {
	cfg    *email.Config
	logger *slog.Logger
}

// sendResult is the typed response for the send_email tool.
type sendResult struct {
	Status  string `json:"status"`
	Method  string `json:"method"`
	Signed  bool   `json:"signed"`
	ID      string `json:"id,omitempty"`
	From    string `json:"from"`
	To      string `json:"to"`
	Subject string `json:"subject"`
}

// verifyResult is the typed response for the verify_signature tool.
type verifyResult struct {
	TrustLevel  channel.TrustLevel `json:"trust_level"`
	Valid       bool               `json:"valid"`
	KeyID       string             `json:"key_id,omitempty"`
	Signer      string             `json:"signer,omitempty"`
	KeyImported bool               `json:"key_imported"`
	GPGOutput   string             `json:"gpg_output"`
}

// --- Tool Definitions ---

func listMessagesTool() mcplib.Tool {
	return mcplib.NewTool("list_messages",
		mcplib.WithDescription("List messages from a mailbox folder. Returns id, from, date, subject, trust level for each message."),
		mcplib.WithString("folder",
			mcplib.Description("IMAP folder name (e.g., INBOX, Sent, All Mail)"),
			mcplib.DefaultString("INBOX"),
		),
		mcplib.WithNumber("count",
			mcplib.Description("Maximum number of messages to return"),
			mcplib.DefaultNumber(10),
		),
		mcplib.WithBoolean("unread_only",
			mcplib.Description("Only return unread messages"),
		),
	)
}

func readMessageTool() mcplib.Tool {
	return mcplib.NewTool("read_message",
		mcplib.WithDescription("Read a single message by UID. Returns full body, headers, attachments summary, and trust level."),
		mcplib.WithString("folder",
			mcplib.Description("IMAP folder name"),
			mcplib.DefaultString("INBOX"),
		),
		mcplib.WithString("message_id",
			mcplib.Required(),
			mcplib.Description("Message UID (from list_messages)"),
		),
	)
}

func listFoldersTool() mcplib.Tool {
	return mcplib.NewTool("list_folders",
		mcplib.WithDescription("List all IMAP mailbox folders."),
	)
}

func sendEmailTool() mcplib.Tool {
	return mcplib.NewTool("send_email",
		mcplib.WithDescription("Send an email via Proton Bridge SMTP (primary) or Resend API (fallback). Sends from the configured address."),
		mcplib.WithString("to",
			mcplib.Required(),
			mcplib.Description("Recipient email address"),
		),
		mcplib.WithString("subject",
			mcplib.Required(),
			mcplib.Description("Email subject line"),
		),
		mcplib.WithString("body",
			mcplib.Required(),
			mcplib.Description("Plain text email body"),
		),
		mcplib.WithString("html",
			mcplib.Description("Optional HTML body"),
		),
	)
}

func verifySignatureTool() mcplib.Tool {
	return mcplib.NewTool("verify_signature",
		mcplib.WithDescription("Verify PGP signature on a message. Returns verification result and signer info."),
		mcplib.WithString("folder",
			mcplib.Description("IMAP folder name"),
			mcplib.DefaultString("INBOX"),
		),
		mcplib.WithString("message_id",
			mcplib.Required(),
			mcplib.Description("Message UID to verify"),
		),
	)
}

func showMIMETool() mcplib.Tool {
	return mcplib.NewTool("show_mime",
		mcplib.WithDescription("Show the MIME structure of a message. Useful for inspecting multipart messages, PGP parts, and attachments."),
		mcplib.WithString("folder",
			mcplib.Description("IMAP folder name"),
			mcplib.DefaultString("INBOX"),
		),
		mcplib.WithString("message_id",
			mcplib.Required(),
			mcplib.Description("Message UID"),
		),
	)
}

func checkTrustTool() mcplib.Tool {
	return mcplib.NewTool("check_trust",
		mcplib.WithDescription("Classify a message's trust level with detailed explanation. Shows encryption type, origin, and whether PGP verification is needed."),
		mcplib.WithString("folder",
			mcplib.Description("IMAP folder name"),
			mcplib.DefaultString("INBOX"),
		),
		mcplib.WithString("message_id",
			mcplib.Required(),
			mcplib.Description("Message UID"),
		),
	)
}

func moveMessageTool() mcplib.Tool {
	return mcplib.NewTool("move_message",
		mcplib.WithDescription("Move a message to another folder. Defaults to Archive. Use for archiving, trashing, or reorganizing messages."),
		mcplib.WithString("folder",
			mcplib.Description("Source IMAP folder name"),
			mcplib.DefaultString("INBOX"),
		),
		mcplib.WithString("message_id",
			mcplib.Required(),
			mcplib.Description("Message UID to move"),
		),
		mcplib.WithString("destination",
			mcplib.Description("Destination folder name"),
			mcplib.DefaultString("Archive"),
		),
	)
}

// --- Tool Handlers ---

func (h *handler) withClient(ctx context.Context, fn func(*email.Client) (*mcplib.CallToolResult, error)) (*mcplib.CallToolResult, error) {
	client, err := email.Dial(h.cfg, h.logger)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("IMAP connection failed: %v", err)), nil
	}
	defer client.Close()
	return fn(client)
}

func (h *handler) listMessages(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	folder := stringParam(req, "folder", "INBOX")
	count := intParam(req, "count", 10)
	unreadOnly := boolParam(req, "unread_only")

	return h.withClient(ctx, func(c *email.Client) (*mcplib.CallToolResult, error) {
		msgs, err := c.ListMessages(folder, count, unreadOnly)
		if err != nil {
			return mcplib.NewToolResultError(fmt.Sprintf("list messages: %v", err)), nil
		}

		// Verify PGP signatures inline so trust levels are accurate
		for i := range msgs {
			if !msgs[i].HasSig {
				continue
			}
			uid, parseErr := strconv.ParseUint(msgs[i].ID, 10, 32)
			if parseErr != nil {
				continue
			}
			raw, fetchErr := c.FetchRaw(folder, uint32(uid))
			if fetchErr != nil {
				h.logger.Warn("pgp: fetch raw failed", "uid", msgs[i].ID, "err", fetchErr)
				continue
			}
			result, verifyErr := pgp.Verify(h.cfg.GPGBinary, raw)
			if verifyErr != nil {
				h.logger.Warn("pgp: verify failed", "uid", msgs[i].ID, "err", verifyErr)
				continue
			}
			if result.Valid {
				msgs[i].TrustLevel = channel.Verified
			} else {
				msgs[i].TrustLevel = channel.Untrusted
			}
		}

		return jsonResult(msgs)
	})
}

func (h *handler) readMessage(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	folder := stringParam(req, "folder", "INBOX")
	msgID, err := req.RequireString("message_id")
	if err != nil {
		return mcplib.NewToolResultError("message_id is required"), nil
	}

	uid, err := strconv.ParseUint(msgID, 10, 32)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("invalid message_id %q: %v", msgID, err)), nil
	}

	return h.withClient(ctx, func(c *email.Client) (*mcplib.CallToolResult, error) {
		msg, err := c.FetchMessage(folder, uint32(uid))
		if err != nil {
			return mcplib.NewToolResultError(fmt.Sprintf("read message: %v", err)), nil
		}

		// Verify PGP signature if the Content-Type indicates multipart/signed.
		// We pass nil for raw bytes — this checks the header only, not body
		// scanning. Messages with PGP markers only in the body (no header)
		// will not be auto-verified; use verify_signature explicitly for those.
		if msg.TrustLevel == channel.Unverified && email.HasPGPSignature(msg.RawHeaders["Content-Type"], nil) {
			raw, fetchErr := c.FetchRaw(folder, uint32(uid))
			if fetchErr == nil {
				result, verifyErr := pgp.Verify(h.cfg.GPGBinary, raw)
				if verifyErr == nil {
					if result.Valid {
						msg.TrustLevel = channel.Verified
					} else {
						msg.TrustLevel = channel.Untrusted
					}
				}
			}
		}

		return jsonResult(msg)
	})
}

func (h *handler) listFolders(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	return h.withClient(ctx, func(c *email.Client) (*mcplib.CallToolResult, error) {
		folders, err := c.ListFolders()
		if err != nil {
			return mcplib.NewToolResultError(fmt.Sprintf("list folders: %v", err)), nil
		}
		return jsonResult(folders)
	})
}

func (h *handler) sendEmail(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	to, err := req.RequireString("to")
	if err != nil {
		return mcplib.NewToolResultError("to is required"), nil
	}
	subject, err := req.RequireString("subject")
	if err != nil {
		return mcplib.NewToolResultError("subject is required"), nil
	}
	body, err := req.RequireString("body")
	if err != nil {
		return mcplib.NewToolResultError("body is required"), nil
	}
	html := stringParam(req, "html", "")

	// Sender chain: Proton Bridge SMTP → Resend plain
	//
	// PGP signing of outbound mail requires a transport that preserves raw MIME
	// (multipart/signed envelopes). Neither Proton Bridge (strips MIME) nor
	// Resend (no raw MIME API) supports this. Tracked in beadle-atz: Amazon SES
	// will be added as the PGP-signing transport in a future release.
	result, sendErr := h.trySendChain(to, subject, body, html)
	if sendErr != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("send email: %v", sendErr)), nil
	}
	return jsonResult(result)
}

// trySendChain attempts to send via the best available method.
func (h *handler) trySendChain(to, subject, body, html string) (*sendResult, error) {
	// 1. Proton Bridge SMTP — passes SPF/DKIM/DMARC for punt-labs.com
	// Note: SMTP path sends plain text only. The html parameter is only
	// used by the Resend fallback which supports structured HTML fields.
	if email.SMTPAvailable(h.cfg) {
		raw, composeErr := email.ComposeRaw(h.cfg.FromAddress, to, subject, body)
		if composeErr != nil {
			return nil, composeErr
		}
		if err := email.SMTPSend(h.cfg, h.cfg.FromAddress, to, raw); err != nil {
			h.logger.Warn("smtp send failed, falling back to resend", "err", err)
		} else {
			return &sendResult{
				Status:  "sent",
				Method:  "proton-bridge-smtp",
				From:    h.cfg.FromAddress,
				To:      to,
				Subject: subject,
			}, nil
		}
	}

	// 2. Resend API — fallback when Bridge is unavailable
	resp, err := email.Send(h.cfg, email.SendRequest{
		To:      to,
		Subject: subject,
		Text:    body,
		HTML:    html,
	})
	if err != nil {
		return nil, err
	}

	return &sendResult{
		Status:  "sent",
		Method:  "resend",
		ID:      resp.ID,
		From:    h.cfg.FromAddress,
		To:      to,
		Subject: subject,
	}, nil
}

func (h *handler) verifySignature(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	folder := stringParam(req, "folder", "INBOX")
	msgID, err := req.RequireString("message_id")
	if err != nil {
		return mcplib.NewToolResultError("message_id is required"), nil
	}

	uid, err := strconv.ParseUint(msgID, 10, 32)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("invalid message_id: %v", err)), nil
	}

	return h.withClient(ctx, func(c *email.Client) (*mcplib.CallToolResult, error) {
		raw, err := c.FetchRaw(folder, uint32(uid))
		if err != nil {
			return mcplib.NewToolResultError(fmt.Sprintf("fetch message: %v", err)), nil
		}

		result, err := pgp.Verify(h.cfg.GPGBinary, raw)
		if err != nil {
			return mcplib.NewToolResultError(fmt.Sprintf("verify signature: %v", err)), nil
		}

		// Map PGP result to trust level
		trustLevel := channel.Untrusted
		if result.Valid {
			trustLevel = channel.Verified
		}

		return jsonResult(&verifyResult{
			TrustLevel:  trustLevel,
			Valid:       result.Valid,
			KeyID:       result.KeyID,
			Signer:      result.Signer,
			KeyImported: result.KeyImported,
			GPGOutput:   result.Output,
		})
	})
}

func (h *handler) showMIME(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	folder := stringParam(req, "folder", "INBOX")
	msgID, err := req.RequireString("message_id")
	if err != nil {
		return mcplib.NewToolResultError("message_id is required"), nil
	}

	uid, err := strconv.ParseUint(msgID, 10, 32)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("invalid message_id: %v", err)), nil
	}

	return h.withClient(ctx, func(c *email.Client) (*mcplib.CallToolResult, error) {
		raw, err := c.FetchRaw(folder, uint32(uid))
		if err != nil {
			return mcplib.NewToolResultError(fmt.Sprintf("fetch message: %v", err)), nil
		}

		parts, err := email.ParseMIMEStructure(raw)
		if err != nil {
			return mcplib.NewToolResultError(fmt.Sprintf("parse MIME: %v", err)), nil
		}
		return jsonResult(parts)
	})
}

func (h *handler) checkTrust(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	folder := stringParam(req, "folder", "INBOX")
	msgID, err := req.RequireString("message_id")
	if err != nil {
		return mcplib.NewToolResultError("message_id is required"), nil
	}

	uid, err := strconv.ParseUint(msgID, 10, 32)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("invalid message_id: %v", err)), nil
	}

	return h.withClient(ctx, func(c *email.Client) (*mcplib.CallToolResult, error) {
		raw, err := c.FetchRaw(folder, uint32(uid))
		if err != nil {
			return mcplib.NewToolResultError(fmt.Sprintf("fetch message: %v", err)), nil
		}

		_, _, headers := email.ParseMIME(raw)
		result := email.ClassifyTrustDetailed(headers, raw)

		return jsonResult(result)
	})
}

// moveResult is the typed response for the move_message tool.
type moveResult struct {
	Status      string `json:"status"`
	MessageID   string `json:"message_id"`
	Source      string `json:"source"`
	Destination string `json:"destination"`
}

func (h *handler) moveMessage(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	folder := stringParam(req, "folder", "INBOX")
	msgID, err := req.RequireString("message_id")
	if err != nil {
		return mcplib.NewToolResultError("message_id is required"), nil
	}
	destination := stringParam(req, "destination", "Archive")

	uid, err := strconv.ParseUint(msgID, 10, 32)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("invalid message_id %q: %v", msgID, err)), nil
	}

	return h.withClient(ctx, func(c *email.Client) (*mcplib.CallToolResult, error) {
		if err := c.MoveMessage(folder, uint32(uid), destination); err != nil {
			return mcplib.NewToolResultError(fmt.Sprintf("move message: %v", err)), nil
		}
		return jsonResult(&moveResult{
			Status:      "moved",
			MessageID:   msgID,
			Source:      folder,
			Destination: destination,
		})
	})
}

// --- Helpers ---

func stringParam(req mcplib.CallToolRequest, key, fallback string) string {
	args := req.GetArguments()
	if v, ok := args[key]; ok {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	return fallback
}

func intParam(req mcplib.CallToolRequest, key string, fallback int) int {
	args := req.GetArguments()
	if v, ok := args[key]; ok {
		switch n := v.(type) {
		case float64:
			return int(n)
		case int:
			return n
		}
	}
	return fallback
}

func boolParam(req mcplib.CallToolRequest, key string) bool {
	args := req.GetArguments()
	if v, ok := args[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return false
}

func jsonResult(v any) (*mcplib.CallToolResult, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("marshal result: %v", err)), nil
	}
	return mcplib.NewToolResultText(string(data)), nil
}
