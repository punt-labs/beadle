// Package mcp defines the MCP tool surface for Beadle's email channel.
package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/punt-labs/beadle/internal/channel"
	"github.com/punt-labs/beadle/internal/contacts"
	"github.com/punt-labs/beadle/internal/email"
	"github.com/punt-labs/beadle/internal/pgp"
)

const maxAttachmentSize = 25 * 1024 * 1024 // 25 MB

// RegisterTools adds all email channel tools to the MCP server.
func RegisterTools(s *server.MCPServer, cfg *email.Config, contactsPath string, logger *slog.Logger) {
	h := &handler{cfg: cfg, contactsPath: contactsPath, logger: logger}

	s.AddTool(listMessagesTool(), h.listMessages)
	s.AddTool(readMessageTool(), h.readMessage)
	s.AddTool(listFoldersTool(), h.listFolders)
	s.AddTool(sendEmailTool(), h.sendEmail)
	s.AddTool(verifySignatureTool(), h.verifySignature)
	s.AddTool(showMIMETool(), h.showMIME)
	s.AddTool(checkTrustTool(), h.checkTrust)
	s.AddTool(moveMessageTool(), h.moveMessage)
	s.AddTool(downloadAttachmentTool(), h.downloadAttachment)

	s.AddTool(listContactsTool(), h.listContacts)
	s.AddTool(findContactTool(), h.findContact)
	s.AddTool(addContactTool(), h.addContact)
	s.AddTool(removeContactTool(), h.removeContact)
}

type handler struct {
	cfg          *email.Config
	contactsPath string
	logger       *slog.Logger
}

// sendResult is the typed response for the send_email tool.
// Bcc is intentionally omitted — BCC addresses are confidential by design.
type sendResult struct {
	Status      string `json:"status"`
	Method      string `json:"method"`
	Signed      bool   `json:"signed"`
	ID          string `json:"id,omitempty"`
	From        string `json:"from"`
	To          string `json:"to"`
	Cc          string `json:"cc,omitempty"`
	BccCount    int    `json:"bcc_count,omitempty"`
	Subject     string `json:"subject"`
	Attachments int    `json:"attachments"`
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
		mcplib.WithDescription("Send an email via Proton Bridge SMTP (primary) or Resend API (fallback). Sends from the configured address. Supports file attachments. Recipients can be email addresses or contact names/aliases from the address book — names are resolved inline."),
		mcplib.WithString("to",
			mcplib.Required(),
			mcplib.Description("Recipient(s), comma-separated. Accepts email addresses or contact names/aliases (e.g., 'jim' or 'jim,kai@example.com')"),
		),
		mcplib.WithString("cc",
			mcplib.Description("CC recipient(s), comma-separated. Accepts email addresses or contact names/aliases"),
		),
		mcplib.WithString("bcc",
			mcplib.Description("BCC recipient(s), comma-separated. Accepts email addresses or contact names/aliases"),
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
		mcplib.WithArray("attachments",
			mcplib.Description("Absolute file paths to attach (max 25 MB total)"),
			mcplib.WithStringItems(),
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

// --- Contact Tool Definitions ---

func listContactsTool() mcplib.Tool {
	return mcplib.NewTool("list_contacts",
		mcplib.WithDescription("List all contacts in the address book."),
	)
}

func findContactTool() mcplib.Tool {
	return mcplib.NewTool("find_contact",
		mcplib.WithDescription("Look up a contact by name, email, or alias. Returns all exact matches (case-insensitive)."),
		mcplib.WithString("query",
			mcplib.Required(),
			mcplib.Description("Name, email, or alias to search for"),
		),
	)
}

func addContactTool() mcplib.Tool {
	return mcplib.NewTool("add_contact",
		mcplib.WithDescription("Add a contact to the address book. Name and email are required. Names and aliases must be unique."),
		mcplib.WithString("name",
			mcplib.Required(),
			mcplib.Description("Contact display name (unique key)"),
		),
		mcplib.WithString("email",
			mcplib.Required(),
			mcplib.Description("Email address"),
		),
		mcplib.WithArray("aliases",
			mcplib.Description("Alternative names for lookup (e.g., nicknames)"),
			mcplib.WithStringItems(),
		),
		mcplib.WithString("gpg_key_id",
			mcplib.Description("GPG key fingerprint for future encryption"),
		),
		mcplib.WithString("notes",
			mcplib.Description("Free-text notes"),
		),
	)
}

func removeContactTool() mcplib.Tool {
	return mcplib.NewTool("remove_contact",
		mcplib.WithDescription("Remove a contact from the address book by name."),
		mcplib.WithString("name",
			mcplib.Required(),
			mcplib.Description("Contact name to remove (case-insensitive)"),
		),
	)
}

func downloadAttachmentTool() mcplib.Tool {
	return mcplib.NewTool("download_attachment",
		mcplib.WithDescription("Extract an attachment from a message by MIME part index (from show_mime). Saves the file to ~/.beadle/<mailbox>/attachments/ and returns the path."),
		mcplib.WithString("folder",
			mcplib.Description("IMAP folder name"),
			mcplib.DefaultString("INBOX"),
		),
		mcplib.WithString("message_id",
			mcplib.Required(),
			mcplib.Description("Message UID"),
		),
		mcplib.WithNumber("part_index",
			mcplib.Required(),
			mcplib.Description("MIME part index (from show_mime)"),
		),
	)
}

// downloadResult is the typed response for the download_attachment tool.
type downloadResult struct {
	Status      string `json:"status"`
	Path        string `json:"path"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	Size        int    `json:"size"`
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
			if fetchErr != nil {
				h.logger.Warn("pgp: fetch raw failed", "uid", msgID, "err", fetchErr)
			} else {
				result, verifyErr := pgp.Verify(h.cfg.GPGBinary, raw)
				if verifyErr != nil {
					h.logger.Warn("pgp: verify failed", "uid", msgID, "err", verifyErr)
				} else if result.Valid {
					msg.TrustLevel = channel.Verified
				} else {
					msg.TrustLevel = channel.Untrusted
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
	toRaw, err := req.RequireString("to")
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

	// Resolve contact names to email addresses before splitting.
	// Load contacts once for all three address fields.
	ccRaw := stringParam(req, "cc", "")
	bccRaw := stringParam(req, "bcc", "")
	store, storeErr := h.loadContactsIfNeeded(toRaw, ccRaw, bccRaw)
	toResolved, err := resolveField(store, storeErr, toRaw)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("to: %v", err)), nil
	}
	ccResolved, err := resolveField(store, storeErr, ccRaw)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("cc: %v", err)), nil
	}
	bccResolved, err := resolveField(store, storeErr, bccRaw)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("bcc: %v", err)), nil
	}

	to := splitAddresses(toResolved)
	if len(to) == 0 {
		return mcplib.NewToolResultError("to: at least one valid email address is required"), nil
	}
	cc := splitAddresses(ccResolved)
	bcc := splitAddresses(bccResolved)

	attachments, err := readAttachments(req)
	if err != nil {
		return mcplib.NewToolResultError(err.Error()), nil
	}

	// Sender chain: Proton Bridge SMTP → Resend plain
	//
	// PGP signing of outbound mail requires a transport that preserves raw MIME
	// (multipart/signed envelopes). Neither Proton Bridge (strips MIME) nor
	// Resend (no raw MIME API) supports this. Tracked in beadle-atz: Amazon SES
	// will be added as the PGP-signing transport in a future release.
	result, sendErr := h.trySendChain(to, cc, bcc, subject, body, html, attachments)
	if sendErr != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("send email: %v", sendErr)), nil
	}
	return jsonResult(result)
}

// trySendChain attempts to send via the best available method.
func (h *handler) trySendChain(to, cc, bcc []string, subject, body, html string, attachments []email.OutboundAttachment) (*sendResult, error) {
	// Validate BCC addresses for CR/LF injection. ComposeRaw validates to/cc
	// but never receives bcc (by design — BCC must not appear in headers).
	// Without this check, malicious bcc values could inject SMTP commands.
	for _, addr := range bcc {
		if strings.ContainsAny(addr, "\r\n") {
			return nil, fmt.Errorf("bcc address contains CR/LF")
		}
	}

	// All envelope recipients (SMTP RCPT TO + Resend arrays).
	allRecipients := make([]string, 0, len(to)+len(cc)+len(bcc))
	allRecipients = append(allRecipients, to...)
	allRecipients = append(allRecipients, cc...)
	allRecipients = append(allRecipients, bcc...)

	toStr := strings.Join(to, ", ")
	ccStr := strings.Join(cc, ", ")

	// 1. Proton Bridge SMTP — passes SPF/DKIM/DMARC for punt-labs.com
	// Note: SMTP path sends plain text only (no HTML). The html parameter
	// is only used by the Resend fallback which supports structured HTML fields.
	if email.SMTPAvailable(h.cfg) {
		raw, composeErr := email.ComposeRaw(h.cfg.FromAddress, to, cc, subject, body, attachments)
		if composeErr != nil {
			return nil, composeErr
		}
		if err := email.SMTPSend(h.cfg, h.cfg.FromAddress, allRecipients, raw); err != nil {
			h.logger.Warn("smtp send failed, falling back to resend", "err", err)
		} else {
			return &sendResult{
				Status:      "sent",
				Method:      "proton-bridge-smtp",
				From:        h.cfg.FromAddress,
				To:          toStr,
				Cc:          ccStr,
				BccCount:    len(bcc),
				Subject:     subject,
				Attachments: len(attachments),
			}, nil
		}
	}

	// 2. Resend API — fallback when Bridge is unavailable
	var resendAtts []email.ResendAttachment
	for _, att := range attachments {
		resendAtts = append(resendAtts, email.ResendAttachment{
			Filename: att.Filename,
			Content:  base64.StdEncoding.EncodeToString(att.Data),
		})
	}

	resp, err := email.Send(h.cfg, email.SendRequest{
		To:          to,
		Cc:          cc,
		Bcc:         bcc,
		Subject:     subject,
		Text:        body,
		HTML:        html,
		Attachments: resendAtts,
	})
	if err != nil {
		return nil, err
	}

	return &sendResult{
		Status:      "sent",
		Method:      "resend",
		ID:          resp.ID,
		From:        h.cfg.FromAddress,
		To:          toStr,
		Cc:          ccStr,
		BccCount:    len(bcc),
		Subject:     subject,
		Attachments: len(attachments),
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

func (h *handler) downloadAttachment(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	folder := stringParam(req, "folder", "INBOX")
	msgID, err := req.RequireString("message_id")
	if err != nil {
		return mcplib.NewToolResultError("message_id is required"), nil
	}
	partIndex := intParam(req, "part_index", -1)
	if partIndex < 0 {
		return mcplib.NewToolResultError("part_index is required"), nil
	}

	uid, err := strconv.ParseUint(msgID, 10, 32)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("invalid message_id %q: %v", msgID, err)), nil
	}

	return h.withClient(ctx, func(c *email.Client) (*mcplib.CallToolResult, error) {
		raw, err := c.FetchRaw(folder, uint32(uid))
		if err != nil {
			h.logger.Warn("download_attachment: fetch failed", "uid", msgID, "folder", folder, "err", err)
			return mcplib.NewToolResultError(fmt.Sprintf("fetch message: %v", err)), nil
		}

		part, data, err := email.ExtractPart(raw, partIndex)
		if err != nil {
			h.logger.Warn("download_attachment: extract failed", "uid", msgID, "part", partIndex, "err", err)
			return mcplib.NewToolResultError(fmt.Sprintf("extract part: %v", err)), nil
		}

		// Build output path: ~/.beadle/<mailbox>/attachments/<uid>_<filename>
		// Use filepath.Base to prevent path traversal via attacker-controlled filenames.
		filename := filepath.Base(part.Filename)
		if filename == "" || filename == "." {
			filename = fmt.Sprintf("part_%d", partIndex)
		}
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return mcplib.NewToolResultError(fmt.Sprintf("resolve home directory: %v", err)), nil
		}
		attachDir := filepath.Join(homeDir, ".beadle", filepath.Base(h.cfg.IMAPUser), "attachments")
		if err := os.MkdirAll(attachDir, 0o750); err != nil {
			return mcplib.NewToolResultError(fmt.Sprintf("create attachment dir: %v", err)), nil
		}

		outName := fmt.Sprintf("%s_%s", msgID, filename)
		outPath := filepath.Join(attachDir, outName)
		if err := os.WriteFile(outPath, data, 0o640); err != nil {
			return mcplib.NewToolResultError(fmt.Sprintf("write attachment: %v", err)), nil
		}

		absPath, err := filepath.Abs(outPath)
		if err != nil {
			return mcplib.NewToolResultError(fmt.Sprintf("resolve output path: %v", err)), nil
		}
		return jsonResult(&downloadResult{
			Status:      "saved",
			Path:        absPath,
			Filename:    filename,
			ContentType: part.ContentType,
			Size:        part.Size,
		})
	})
}

// --- Helpers ---

// splitAddresses splits a comma-separated string of email addresses,
// trimming whitespace around each. Returns nil for empty input,
// whitespace-only input, or input that yields no non-empty addresses.
func splitAddresses(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// readAttachments extracts the attachments parameter, reads each file, and
// validates paths and sizes. Returns nil (not an error) when no attachments
// are provided. Enforces a 25 MB per-file and aggregate limit.
func readAttachments(req mcplib.CallToolRequest) ([]email.OutboundAttachment, error) {
	paths, err := stringSliceParam(req, "attachments")
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, nil
	}

	var totalSize int64
	attachments := make([]email.OutboundAttachment, 0, len(paths))
	for _, path := range paths {
		if !filepath.IsAbs(path) {
			return nil, fmt.Errorf("attachment path must be absolute: %q", path)
		}

		f, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("attachment %q: %w", filepath.Base(path), err)
		}

		info, err := f.Stat()
		if err != nil {
			f.Close()
			return nil, fmt.Errorf("attachment %q: %w", filepath.Base(path), err)
		}
		if info.Size() > maxAttachmentSize {
			f.Close()
			return nil, fmt.Errorf("attachment %q exceeds 25 MB limit (%d bytes)", filepath.Base(path), info.Size())
		}

		data, err := io.ReadAll(io.LimitReader(f, maxAttachmentSize+1))
		f.Close()
		if err != nil {
			return nil, fmt.Errorf("read attachment %q: %w", filepath.Base(path), err)
		}
		if int64(len(data)) > maxAttachmentSize {
			return nil, fmt.Errorf("attachment %q exceeds 25 MB limit", filepath.Base(path))
		}

		totalSize += int64(len(data))
		if totalSize > maxAttachmentSize {
			return nil, fmt.Errorf("total attachment size exceeds 25 MB limit (%d bytes)", totalSize)
		}

		ct := mime.TypeByExtension(filepath.Ext(path))
		if ct == "" {
			ct = "application/octet-stream"
		}

		attachments = append(attachments, email.OutboundAttachment{
			Filename:    filepath.Base(path),
			ContentType: ct,
			Data:        data,
		})
	}
	return attachments, nil
}

// stringSliceParam extracts a []string from the MCP request arguments.
// Returns nil, nil if the key is missing.
// Returns an error if the value is present but not an array, or if any
// element is not a string.
func stringSliceParam(req mcplib.CallToolRequest, key string) ([]string, error) {
	args := req.GetArguments()
	v, ok := args[key]
	if !ok {
		return nil, nil
	}
	arr, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("%s: expected array, got %T", key, v)
	}
	result := make([]string, 0, len(arr))
	for i, item := range arr {
		s, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("%s[%d]: expected string, got %T", key, i, item)
		}
		result = append(result, s)
	}
	return result, nil
}

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

// --- Contact Handlers ---

type contactResult struct {
	Name     string   `json:"name"`
	Email    string   `json:"email"`
	Aliases  []string `json:"aliases,omitempty"`
	GPGKeyID string   `json:"gpg_key_id,omitempty"`
	Notes    string   `json:"notes,omitempty"`
}

type removeContactResult struct {
	Status string `json:"status"`
	Name   string `json:"name"`
}

func contactToResult(c contacts.Contact) contactResult {
	return contactResult{
		Name:     c.Name,
		Email:    c.Email,
		Aliases:  c.Aliases,
		GPGKeyID: c.GPGKeyID,
		Notes:    c.Notes,
	}
}

func (h *handler) loadContacts() (*contacts.Store, error) {
	s := contacts.NewStore(h.contactsPath)
	if err := s.Load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (h *handler) listContacts(_ context.Context, _ mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	store, err := h.loadContacts()
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("load contacts: %v", err)), nil
	}
	results := make([]contactResult, 0, store.Count())
	for _, c := range store.Contacts() {
		results = append(results, contactToResult(c))
	}
	return jsonResult(results)
}

func (h *handler) findContact(_ context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	query, err := req.RequireString("query")
	if err != nil {
		return mcplib.NewToolResultError("query is required"), nil
	}
	store, err := h.loadContacts()
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("load contacts: %v", err)), nil
	}
	matches := store.Find(query)
	results := make([]contactResult, 0, len(matches))
	for _, c := range matches {
		results = append(results, contactToResult(c))
	}
	return jsonResult(results)
}

func (h *handler) addContact(_ context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	name, err := req.RequireString("name")
	if err != nil {
		return mcplib.NewToolResultError("name is required"), nil
	}
	email, err := req.RequireString("email")
	if err != nil {
		return mcplib.NewToolResultError("email is required"), nil
	}
	aliases, err := stringSliceParam(req, "aliases")
	if err != nil {
		return mcplib.NewToolResultError(err.Error()), nil
	}

	c := contacts.Contact{
		Name:     name,
		Email:    email,
		Aliases:  aliases,
		GPGKeyID: stringParam(req, "gpg_key_id", ""),
		Notes:    stringParam(req, "notes", ""),
	}

	store, err := h.loadContacts()
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("load contacts: %v", err)), nil
	}
	if err := store.Add(c); err != nil {
		return mcplib.NewToolResultError(err.Error()), nil
	}
	return jsonResult(contactToResult(c))
}

func (h *handler) removeContact(_ context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	name, err := req.RequireString("name")
	if err != nil {
		return mcplib.NewToolResultError("name is required"), nil
	}
	store, err := h.loadContacts()
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("load contacts: %v", err)), nil
	}
	if err := store.Remove(name); err != nil {
		return mcplib.NewToolResultError(err.Error()), nil
	}
	return jsonResult(removeContactResult{Status: "removed", Name: name})
}

// loadContactsIfNeeded loads the contacts store only if any token across
// the given address fields lacks @. Returns nil store (no error) when no
// resolution is needed. This ensures a corrupted contacts file does not
// break sending to raw email addresses.
func (h *handler) loadContactsIfNeeded(fields ...string) (*contacts.Store, error) {
	for _, raw := range fields {
		for _, tok := range strings.Split(raw, ",") {
			if t := strings.TrimSpace(tok); t != "" && !strings.Contains(t, "@") {
				return h.loadContacts()
			}
		}
	}
	return nil, nil
}

// resolveField resolves names in a comma-separated address string using
// a pre-loaded contacts store. If store is nil, returns raw unchanged.
func resolveField(store *contacts.Store, storeErr error, raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	if store == nil {
		if storeErr != nil {
			return "", fmt.Errorf("load contacts: %w", storeErr)
		}
		return raw, nil
	}
	return store.ResolveAddresses(raw)
}
