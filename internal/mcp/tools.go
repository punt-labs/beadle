// Package mcp defines the MCP tool surface for Beadle's email channel.
package mcp

import (
	"context"
	"errors"
	"fmt"
	"math"
	"io"
	"log/slog"
	"mime"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/punt-labs/beadle/internal/channel"
	"github.com/punt-labs/beadle/internal/contacts"
	"github.com/punt-labs/beadle/internal/email"
	"github.com/punt-labs/beadle/internal/identity"
	"github.com/punt-labs/beadle/internal/paths"
	"github.com/punt-labs/beadle/internal/pgp"
)

const maxAttachmentSize = 25 * 1024 * 1024 // 25 MB

// HandlerOption configures the MCP tool handler.
type HandlerOption func(*handler)

// WithDialer sets the IMAP dialer. Defaults to email.DefaultDialer{}.
func WithDialer(d email.Dialer) HandlerOption {
	return func(h *handler) { h.dialer = d }
}

// WithEthosDir sets the ethos directory for session roster reads.
// If not set, whoami omits participant information.
func WithEthosDir(dir string) HandlerOption {
	return func(h *handler) { h.ethosDir = dir }
}

// WithPoller sets the background inbox poller for poll tools.
func WithPoller(p *email.Poller) HandlerOption {
	return func(h *handler) { h.poller = p }
}

// RegisterTools adds all email channel tools to the MCP server.
func RegisterTools(s *server.MCPServer, resolver *identity.Resolver, logger *slog.Logger, opts ...HandlerOption) {
	h := &handler{resolver: resolver, logger: logger, dialer: email.DefaultDialer{}}
	for _, o := range opts {
		o(h)
	}

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

	s.AddTool(whoamiTool(), h.whoami)
	s.AddTool(switchIdentityTool(), h.switchIdentity)

	if h.poller != nil {
		s.AddTool(setPollIntervalTool(), h.setPollInterval)
		s.AddTool(getPollStatusTool(), h.getPollStatus)
	}
}

type handler struct {
	resolver         *identity.Resolver
	logger           *slog.Logger
	dialer           email.Dialer
	ethosDir         string
	poller           *email.Poller
	overrideMu       sync.RWMutex       // guards identityOverride
	identityOverride *identity.Identity // session-scoped: depends on process lifecycle matching session
}

// resolveIdentityAndConfig resolves the active identity and loads the
// corresponding email config. Used by tools that don't need contacts.
func (h *handler) resolveIdentityAndConfig() (*identity.Identity, *email.Config, string, error) {
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
			return nil, nil, "", fmt.Errorf("resolve identity: %w", err)
		}
	}

	// Ensure identity-scoped directory exists (auto-migrate)
	beadleDir, err := paths.DataDir()
	if err != nil {
		return nil, nil, "", fmt.Errorf("resolve data dir: %w", err)
	}
	idDir, err := identity.EnsureIdentityDir(beadleDir, id.Email)
	if err != nil {
		return nil, nil, "", fmt.Errorf("ensure identity dir: %w", err)
	}

	// Load config from identity dir, fall back to root only if missing
	configPath := filepath.Join(idDir, "email.json")
	cfg, err := email.LoadConfig(configPath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, nil, "", fmt.Errorf("load identity config %s: %w", configPath, err)
		}
		cfg, err = email.LoadConfig(email.DefaultConfigPath())
		if err != nil {
			return nil, nil, "", fmt.Errorf("load config: %w", err)
		}
	}

	return id, cfg, idDir, nil
}

// resolveContext resolves identity, config, and contacts.
// Used by tools that need the address book.
func (h *handler) resolveContext() (*identity.Identity, *email.Config, *contacts.Store, error) {
	id, cfg, idDir, err := h.resolveIdentityAndConfig()
	if err != nil {
		return nil, nil, nil, err
	}

	contactsPath := filepath.Join(idDir, "contacts.json")
	store := contacts.NewStore(contactsPath)
	if loadErr := store.Load(); loadErr != nil {
		return nil, nil, nil, fmt.Errorf("load contacts: %w", loadErr)
	}

	return id, cfg, store, nil
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
		mcplib.WithNumber("max_body_length",
			mcplib.Description("Maximum body length in characters. When set, truncates the body and appends a truncation indicator. 0 or omitted returns full body."),
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
			mcplib.Description("Recipient(s), comma-separated. Accepts email addresses or contact names/aliases (e.g., 'alice' or 'alice,bob@example.com')"),
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
		mcplib.WithDescription("Add a contact to the address book. Name and email are required. Names and aliases must be unique. Permissions control what beadle can do with this contact for the active identity. The email field accepts a glob pattern (e.g. *@mail.anthropic.com) to cover rotating sender addresses within a domain; pattern contacts may not grant write or execute — allowed permissions are r-- (read-only) or --- (blocked)."),
		mcplib.WithString("name",
			mcplib.Required(),
			mcplib.Description("Contact display name (unique key)"),
		),
		mcplib.WithString("email",
			mcplib.Required(),
			mcplib.Description("Exact address (e.g. alice@example.com) or glob pattern (e.g. *@mail.anthropic.com). Patterns use path.Match syntax and may not grant write or execute — r-- or --- only."),
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
		mcplib.WithString("permissions",
			mcplib.Description("rwx permission string for active identity (e.g., 'rwx', 'rw-', 'r--'). Default: ---. Pattern contacts may not include 'w' or 'x' — use 'r--' or '---'."),
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
		mcplib.WithDescription("Extract an attachment from a message by MIME part index (from show_mime). Saves the file to ~/.punt-labs/beadle/attachments/<mailbox>/ and returns the path."),
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

func (h *handler) withClient(cfg *email.Config, fn func(*email.Client) (*mcplib.CallToolResult, error)) (*mcplib.CallToolResult, error) {
	client, err := h.dialer.Dial(cfg, h.logger)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("IMAP connection failed: %v", err)), nil
	}
	defer client.Close()
	return fn(client)
}

func (h *handler) listMessages(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	id, cfg, store, err := h.resolveContext()
	if err != nil {
		return mcplib.NewToolResultError(err.Error()), nil
	}

	folder := stringParam(req, "folder", "INBOX")
	count, err := intParam(req, "count", 10)
	if err != nil {
		return mcplib.NewToolResultError(err.Error()), nil
	}
	unreadOnly := boolParam(req, "unread_only")

	return h.withClient(cfg, func(c *email.Client) (*mcplib.CallToolResult, error) {
		lr, err := c.ListMessages(folder, count, unreadOnly)
		if err != nil {
			return mcplib.NewToolResultError(fmt.Sprintf("list messages: %v", err)), nil
		}

		// Verify PGP signatures inline so trust levels are accurate
		for i := range lr.Messages {
			if !lr.Messages[i].HasSig {
				continue
			}
			uid, parseErr := strconv.ParseUint(lr.Messages[i].ID, 10, 32)
			if parseErr != nil {
				continue
			}
			raw, fetchErr := c.FetchRaw(folder, uint32(uid))
			if fetchErr != nil {
				h.logger.Warn("pgp: fetch raw failed", "uid", lr.Messages[i].ID, "err", fetchErr)
				continue
			}
			result, verifyErr := pgp.Verify(cfg.GPGBinary, raw)
			if verifyErr != nil {
				h.logger.Warn("pgp: verify failed", "uid", lr.Messages[i].ID, "err", verifyErr)
				continue
			}
			if result.Valid {
				lr.Messages[i].TrustLevel = channel.Verified
			} else {
				lr.Messages[i].TrustLevel = channel.Untrusted
			}
		}

		// Enforce read permission: redact subjects for senders without r
		for i := range lr.Messages {
			perm, _ := senderPermission(store, id, lr.Messages[i].From)
			if !perm.Read {
				lr.Messages[i].Subject = "[redacted — no read permission]"
			}
		}

		return textResult(formatMessages(lr.Messages, lr.Total))
	})
}

func (h *handler) readMessage(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	id, cfg, store, err := h.resolveContext()
	if err != nil {
		return mcplib.NewToolResultError(err.Error()), nil
	}

	folder := stringParam(req, "folder", "INBOX")
	msgID, err := req.RequireString("message_id")
	if err != nil {
		return mcplib.NewToolResultError("message_id is required"), nil
	}

	uid, err := strconv.ParseUint(msgID, 10, 32)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("invalid message_id %q: %v", msgID, err)), nil
	}

	maxBody, err := intParam(req, "max_body_length", 0)
	if err != nil {
		return mcplib.NewToolResultError(err.Error()), nil
	}
	if maxBody < 0 {
		return mcplib.NewToolResultError("max_body_length must be non-negative"), nil
	}

	return h.withClient(cfg, func(c *email.Client) (*mcplib.CallToolResult, error) {
		msg, err := c.FetchMessage(folder, uint32(uid))
		if err != nil {
			return mcplib.NewToolResultError(fmt.Sprintf("read message: %v", err)), nil
		}

		// Enforce read permission — deny before exposing body to caller
		perm, senderEmail := senderPermission(store, id, msg.From)
		if !perm.Read {
			if senderEmail == "" {
				return mcplib.NewToolResultError(
					fmt.Sprintf("permission denied: unparseable sender %q", msg.From),
				), nil
			}
			return mcplib.NewToolResultError(
				fmt.Sprintf("permission denied: no read permission for sender %s", senderEmail),
			), nil
		}

		if msg.TrustLevel == channel.Unverified && email.HasPGPSignature(msg.RawHeaders["Content-Type"], nil) {
			raw, fetchErr := c.FetchRaw(folder, uint32(uid))
			if fetchErr != nil {
				h.logger.Warn("pgp: fetch raw failed", "uid", msgID, "err", fetchErr)
			} else {
				result, verifyErr := pgp.Verify(cfg.GPGBinary, raw)
				if verifyErr != nil {
					h.logger.Warn("pgp: verify failed", "uid", msgID, "err", verifyErr)
				} else if result.Valid {
					msg.TrustLevel = channel.Verified
				} else {
					msg.TrustLevel = channel.Untrusted
				}
			}
		}

		if maxBody > 0 {
			runes := []rune(msg.Body)
			if len(runes) > maxBody {
				origLen := len(runes)
				msg.Body = string(runes[:maxBody]) + fmt.Sprintf("\n[truncated — %d chars total]", origLen)
			}
		}

		return textResult(formatMessage(msg))
	})
}

func (h *handler) listFolders(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	_, cfg, _, err := h.resolveIdentityAndConfig()
	if err != nil {
		return mcplib.NewToolResultError(err.Error()), nil
	}

	return h.withClient(cfg, func(c *email.Client) (*mcplib.CallToolResult, error) {
		folders, err := c.ListFolders()
		if err != nil {
			return mcplib.NewToolResultError(fmt.Sprintf("list folders: %v", err)), nil
		}
		return textResult(formatFolders(folders))
	})
}

func (h *handler) sendEmail(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	id, cfg, store, err := h.resolveContext()
	if err != nil {
		return mcplib.NewToolResultError(err.Error()), nil
	}

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

	// Resolve contact names to email addresses using the identity's contacts.
	ccRaw := stringParam(req, "cc", "")
	bccRaw := stringParam(req, "bcc", "")
	toResolved, err := email.ResolveField(store, nil, toRaw)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("to: %v", err)), nil
	}
	ccResolved, err := email.ResolveField(store, nil, ccRaw)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("cc: %v", err)), nil
	}
	bccResolved, err := email.ResolveField(store, nil, bccRaw)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("bcc: %v", err)), nil
	}

	to := splitAddresses(toResolved)
	if len(to) == 0 {
		return mcplib.NewToolResultError("to: at least one valid email address is required"), nil
	}
	cc := splitAddresses(ccResolved)
	bcc := splitAddresses(bccResolved)

	// Enforce write permission for all recipients
	allRecipients := make([]string, 0, len(to)+len(cc)+len(bcc))
	allRecipients = append(allRecipients, to...)
	allRecipients = append(allRecipients, cc...)
	allRecipients = append(allRecipients, bcc...)
	var denied []string
	for _, addr := range allRecipients {
		recipientEmail := email.ExtractEmailAddress(addr)
		if recipientEmail == "" {
			denied = append(denied, addr+" (invalid address)")
			continue
		}
		match, ok := findByEmail(store, recipientEmail)
		if !ok {
			denied = append(denied, recipientEmail+" (unknown contact)")
			continue
		}
		perm := contacts.CheckPermission(match, id.Email)
		if !perm.Write {
			denied = append(denied, recipientEmail+" (no write permission)")
		}
	}
	if len(denied) > 0 {
		return mcplib.NewToolResultError(
			fmt.Sprintf("permission denied: no write permission for: %s", strings.Join(denied, ", ")),
		), nil
	}

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
	result, sendErr := email.TrySendChain(cfg, h.logger, to, cc, bcc, subject, body, html, attachments)
	if sendErr != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("send email: %v", sendErr)), nil
	}
	return textResult(formatSendResult(result))
}

// trySendChain is now email.TrySendChain — called directly above.

func (h *handler) verifySignature(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	_, cfg, _, err := h.resolveIdentityAndConfig()
	if err != nil {
		return mcplib.NewToolResultError(err.Error()), nil
	}

	folder := stringParam(req, "folder", "INBOX")
	msgID, err := req.RequireString("message_id")
	if err != nil {
		return mcplib.NewToolResultError("message_id is required"), nil
	}

	uid, err := strconv.ParseUint(msgID, 10, 32)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("invalid message_id: %v", err)), nil
	}

	return h.withClient(cfg, func(c *email.Client) (*mcplib.CallToolResult, error) {
		raw, err := c.FetchRaw(folder, uint32(uid))
		if err != nil {
			return mcplib.NewToolResultError(fmt.Sprintf("fetch message: %v", err)), nil
		}

		result, err := pgp.Verify(cfg.GPGBinary, raw)
		if err != nil {
			return mcplib.NewToolResultError(fmt.Sprintf("verify signature: %v", err)), nil
		}

		// Map PGP result to trust level
		trustLevel := channel.Untrusted
		if result.Valid {
			trustLevel = channel.Verified
		}

		vr := &verifyResult{
			TrustLevel:  trustLevel,
			Valid:       result.Valid,
			KeyID:       result.KeyID,
			Signer:      result.Signer,
			KeyImported: result.KeyImported,
			GPGOutput:   result.Output,
		}
		return textResult(formatVerifyResult(vr))
	})
}

func (h *handler) showMIME(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	_, cfg, _, err := h.resolveIdentityAndConfig()
	if err != nil {
		return mcplib.NewToolResultError(err.Error()), nil
	}

	folder := stringParam(req, "folder", "INBOX")
	msgID, err := req.RequireString("message_id")
	if err != nil {
		return mcplib.NewToolResultError("message_id is required"), nil
	}

	uid, err := strconv.ParseUint(msgID, 10, 32)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("invalid message_id: %v", err)), nil
	}

	return h.withClient(cfg, func(c *email.Client) (*mcplib.CallToolResult, error) {
		raw, err := c.FetchRaw(folder, uint32(uid))
		if err != nil {
			return mcplib.NewToolResultError(fmt.Sprintf("fetch message: %v", err)), nil
		}

		parts, err := email.ParseMIMEStructure(raw)
		if err != nil {
			return mcplib.NewToolResultError(fmt.Sprintf("parse MIME: %v", err)), nil
		}
		return textResult(formatMIME(parts))
	})
}

func (h *handler) checkTrust(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	id, cfg, store, err := h.resolveContext()
	if err != nil {
		return mcplib.NewToolResultError(err.Error()), nil
	}

	folder := stringParam(req, "folder", "INBOX")
	msgID, err := req.RequireString("message_id")
	if err != nil {
		return mcplib.NewToolResultError("message_id is required"), nil
	}

	uid, err := strconv.ParseUint(msgID, 10, 32)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("invalid message_id: %v", err)), nil
	}

	return h.withClient(cfg, func(c *email.Client) (*mcplib.CallToolResult, error) {
		raw, err := c.FetchRaw(folder, uint32(uid))
		if err != nil {
			return mcplib.NewToolResultError(fmt.Sprintf("fetch message: %v", err)), nil
		}

		_, _, headers := email.ParseMIME(raw)
		result := email.ClassifyTrustDetailed(headers, raw)

		// Look up sender in contacts for identity permission
		from := headers["From"]
		perm, _ := senderPermission(store, id, from)
		senderPerm := perm.String()

		return textResult(formatTrustResultWithPerm(result, senderPerm))
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
	_, cfg, _, err := h.resolveIdentityAndConfig()
	if err != nil {
		return mcplib.NewToolResultError(err.Error()), nil
	}

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

	return h.withClient(cfg, func(c *email.Client) (*mcplib.CallToolResult, error) {
		if err := c.MoveMessage(folder, uint32(uid), destination); err != nil {
			return mcplib.NewToolResultError(fmt.Sprintf("move message: %v", err)), nil
		}
		mr := &moveResult{
			Status:      "moved",
			MessageID:   msgID,
			Source:      folder,
			Destination: destination,
		}
		return textResult(formatMoveResult(mr))
	})
}

func (h *handler) downloadAttachment(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	id, cfg, store, err := h.resolveContext()
	if err != nil {
		return mcplib.NewToolResultError(err.Error()), nil
	}

	folder := stringParam(req, "folder", "INBOX")
	msgID, err := req.RequireString("message_id")
	if err != nil {
		return mcplib.NewToolResultError("message_id is required"), nil
	}
	partIndex, err := intParam(req, "part_index", -1)
	if err != nil {
		return mcplib.NewToolResultError(err.Error()), nil
	}
	if partIndex < 0 {
		return mcplib.NewToolResultError("part_index is required"), nil
	}

	uid, err := strconv.ParseUint(msgID, 10, 32)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("invalid message_id %q: %v", msgID, err)), nil
	}

	return h.withClient(cfg, func(c *email.Client) (*mcplib.CallToolResult, error) {
		raw, err := c.FetchRaw(folder, uint32(uid))
		if err != nil {
			h.logger.Warn("download_attachment: fetch failed", "uid", msgID, "folder", folder, "err", err)
			return mcplib.NewToolResultError(fmt.Sprintf("fetch message: %v", err)), nil
		}

		// Enforce read permission before extracting attachment content
		_, _, headers := email.ParseMIME(raw)
		perm, senderEmail := senderPermission(store, id, headers["From"])
		if !perm.Read {
			if senderEmail == "" {
				return mcplib.NewToolResultError(
					fmt.Sprintf("permission denied: unparseable sender %q", headers["From"]),
				), nil
			}
			return mcplib.NewToolResultError(
				fmt.Sprintf("permission denied: no read permission for sender %s", senderEmail),
			), nil
		}

		part, data, err := email.ExtractPart(raw, partIndex)
		if err != nil {
			h.logger.Warn("download_attachment: extract failed", "uid", msgID, "part", partIndex, "err", err)
			return mcplib.NewToolResultError(fmt.Sprintf("extract part: %v", err)), nil
		}

		// Build output path under identity-scoped directory.
		// Use filepath.Base to prevent path traversal via attacker-controlled filenames.
		filename := filepath.Base(part.Filename)
		if filename == "" || filename == "." {
			filename = fmt.Sprintf("part_%d", partIndex)
		}
		idDir, err := paths.IdentityDir(id.Email)
		if err != nil {
			return mcplib.NewToolResultError(fmt.Sprintf("resolve identity directory: %v", err)), nil
		}
		attachDir := filepath.Join(idDir, "attachments", filepath.Base(cfg.IMAPUser))
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
		dr := &downloadResult{
			Status:      "saved",
			Path:        absPath,
			Filename:    filename,
			ContentType: part.ContentType,
			Size:        part.Size,
		}
		return textResult(formatDownloadResult(dr))
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

func intParam(req mcplib.CallToolRequest, key string, fallback int) (int, error) {
	args := req.GetArguments()
	v, ok := args[key]
	if !ok {
		return fallback, nil
	}
	switch n := v.(type) {
	case float64:
		if math.Trunc(n) != n {
			return 0, fmt.Errorf("%s: expected a whole number, got %g", key, n)
		}
		if n < math.MinInt32 || n > math.MaxInt32 {
			return 0, fmt.Errorf("%s: value %g out of int32 range", key, n)
		}
		return int(n), nil
	case int:
		return n, nil
	}
	return 0, fmt.Errorf("%s: expected number, got %T", key, v)
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

// jsonResult is no longer used — all tools return pre-formatted text via
// textResult() in format.go. Kept as a comment for reference.

// senderPermission looks up the sender's permission for the active identity.
// Returns the Permission and the extracted sender email.
func senderPermission(store *contacts.Store, id *identity.Identity, from string) (contacts.Permission, string) {
	senderEmail := email.ExtractEmailAddress(from)
	if senderEmail == "" {
		return contacts.Permission{}, ""
	}
	match, ok := findByEmail(store, senderEmail)
	if !ok {
		return contacts.Permission{}, senderEmail
	}
	return contacts.CheckPermission(match, id.Email), senderEmail
}

// findByEmail returns the contact that matches addr. Exact non-pattern
// matches beat glob patterns; among pattern matches, the longest pattern
// wins. Delegates to Store.FindByAddress — see its doc comment for the
// full precedence rules.
func findByEmail(store *contacts.Store, addr string) (contacts.Contact, bool) {
	return store.FindByAddress(addr)
}

// --- Contact Handlers ---

type contactResult struct {
	Name        string   `json:"name"`
	Email       string   `json:"email"`
	Aliases     []string `json:"aliases,omitempty"`
	GPGKeyID    string   `json:"gpg_key_id,omitempty"`
	Notes       string   `json:"notes,omitempty"`
	Permissions string   `json:"permissions,omitempty"` // effective rwx for active identity
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

// contactToResultWithPerms includes the effective permission for the active identity.
func contactToResultWithPerms(c contacts.Contact, identityEmail string) contactResult {
	r := contactToResult(c)
	perm := contacts.CheckPermission(c, identityEmail)
	r.Permissions = perm.String()
	return r
}

func (h *handler) listContacts(_ context.Context, _ mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	id, _, store, err := h.resolveContext()
	if err != nil {
		return mcplib.NewToolResultError(err.Error()), nil
	}
	results := contactsToResultsWithPerms(store.Contacts(), id.Email)
	return textResult(formatContacts(results))
}

func (h *handler) findContact(_ context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	id, _, store, err := h.resolveContext()
	if err != nil {
		return mcplib.NewToolResultError(err.Error()), nil
	}
	query, err := req.RequireString("query")
	if err != nil {
		return mcplib.NewToolResultError("query is required"), nil
	}
	matches := store.Find(query)
	results := contactsToResultsWithPerms(matches, id.Email)
	return textResult(formatContacts(results))
}

func (h *handler) addContact(_ context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	id, _, store, err := h.resolveContext()
	if err != nil {
		return mcplib.NewToolResultError(err.Error()), nil
	}

	name, err := req.RequireString("name")
	if err != nil {
		return mcplib.NewToolResultError("name is required"), nil
	}
	addr, err := req.RequireString("email")
	if err != nil {
		return mcplib.NewToolResultError("email is required"), nil
	}
	aliases, err := stringSliceParam(req, "aliases")
	if err != nil {
		return mcplib.NewToolResultError(err.Error()), nil
	}

	c := contacts.Contact{
		Name:     name,
		Email:    addr,
		Aliases:  aliases,
		GPGKeyID: stringParam(req, "gpg_key_id", ""),
		Notes:    stringParam(req, "notes", ""),
	}

	// Set permissions for active identity if provided
	permStr := stringParam(req, "permissions", "")
	if permStr != "" {
		if _, parseErr := contacts.ParsePermission(permStr); parseErr != nil {
			return mcplib.NewToolResultError(fmt.Sprintf("invalid permissions %q: %v", permStr, parseErr)), nil
		}
		c.Permissions = map[string]string{
			strings.ToLower(id.Email): permStr,
		}
	}

	// Validate before handing off to the store so pattern-only rules
	// (e.g. patterns are restricted to r--) surface from the handler
	// with the request context. store.Add validates again; the duplication
	// is defense in depth.
	if err := contacts.Validate(c); err != nil {
		return mcplib.NewToolResultError(err.Error()), nil
	}

	normalized, err := store.Add(c)
	if err != nil {
		return mcplib.NewToolResultError(err.Error()), nil
	}
	return textResult(formatContactAdded(contactToResultWithPerms(normalized, id.Email)))
}

func (h *handler) removeContact(_ context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	_, _, store, err := h.resolveContext()
	if err != nil {
		return mcplib.NewToolResultError(err.Error()), nil
	}

	name, err := req.RequireString("name")
	if err != nil {
		return mcplib.NewToolResultError("name is required"), nil
	}

	// Find matches by name/email/alias to resolve the canonical name,
	// since Remove works by canonical name.
	matches := store.Find(name)
	if len(matches) == 0 {
		return mcplib.NewToolResultError(fmt.Sprintf("contact %q not found", name)), nil
	}
	canonical := matches[0].Name

	if err := store.Remove(canonical); err != nil {
		return mcplib.NewToolResultError(err.Error()), nil
	}
	return textResult(formatContactRemoved(removeContactResult{Status: "removed", Name: canonical}))
}

// loadContactsIfNeeded and resolveField are now email.LoadContactsIfNeeded
// and email.ResolveField — called directly in sendEmail above.

// whoamiTool and whoami are in identity_tools.go.
// switchIdentityTool and switchIdentity are in identity_tools.go.

