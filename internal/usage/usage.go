// Package usage owns Beadle's closed-set, local usage telemetry.
package usage

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/punt-labs/beadle/internal/paths"
	"github.com/punt-labs/punt-observe-go/telemetry"
)

// Command identifies a declared CLI command path.
type Command string

// Tool identifies a declared MCP tool.
type Tool string

// Outcome describes a completed CLI command or MCP call.
type Outcome string

// Decision describes a new-mail mission decision.
type Decision string

// Declared CLI command paths.
const (
	CommandList                 Command = "list"
	CommandSearch               Command = "search"
	CommandRead                 Command = "read"
	CommandSend                 Command = "send"
	CommandReply                Command = "reply"
	CommandMove                 Command = "move"
	CommandMark                 Command = "mark"
	CommandFolders              Command = "folders"
	CommandContact              Command = "contact"
	CommandContactList          Command = "contact_list"
	CommandContactAdd           Command = "contact_add"
	CommandContactRemove        Command = "contact_remove"
	CommandContactFind          Command = "contact_find"
	CommandIdentity             Command = "identity"
	CommandIdentityShow         Command = "identity_show"
	CommandIdentitySet          Command = "identity_set"
	CommandInstall              Command = "install"
	CommandUninstall            Command = "uninstall"
	CommandEnable               Command = "enable"
	CommandDisable              Command = "disable"
	CommandDoctor               Command = "doctor"
	CommandStatus               Command = "status"
	CommandHealth               Command = "health"
	CommandVersion              Command = "version"
	CommandServe                Command = "serve"
	CommandCompletionBash       Command = "completion_bash"
	CommandCompletionZsh        Command = "completion_zsh"
	CommandCompletionFish       Command = "completion_fish"
	CommandCompletionPowershell Command = "completion_powershell"
)

// Declared MCP tools.
const (
	ToolListMessages       Tool = "list_messages"
	ToolSearchMessages     Tool = "search_messages"
	ToolReadMessage        Tool = "read_message"
	ToolListFolders        Tool = "list_folders"
	ToolSendEmail          Tool = "send_email"
	ToolReplyMessage       Tool = "reply_message"
	ToolVerifySignature    Tool = "verify_signature"
	ToolShowMime           Tool = "show_mime"
	ToolCheckTrust         Tool = "check_trust"
	ToolMoveMessage        Tool = "move_message"
	ToolBatchMoveMessages  Tool = "batch_move_messages"
	ToolMarkMessage        Tool = "mark_message"
	ToolBatchMarkMessages  Tool = "batch_mark_messages"
	ToolDownloadAttachment Tool = "download_attachment"
	ToolListContacts       Tool = "list_contacts"
	ToolFindContact        Tool = "find_contact"
	ToolAddContact         Tool = "add_contact"
	ToolRemoveContact      Tool = "remove_contact"
	ToolWhoami             Tool = "whoami"
	ToolSwitchIdentity     Tool = "switch_identity"
	ToolEnable             Tool = "enable"
	ToolSetPollInterval    Tool = "set_poll_interval"
	ToolGetPollStatus      Tool = "get_poll_status"
)

// Completed operation outcomes.
const (
	OK     Outcome = "ok"
	Error  Outcome = "error"
	Denied Outcome = "denied"

	Created      Decision = "created"
	BelowTrust   Decision = "below_trust"
	NoPermission Decision = "no_permission"
	NoCommand    Decision = "no_command"
	Failed       Decision = "failed"
)

var schemas = []telemetry.Schema{
	{Name: "beadle.cli.command", Version: 1, Signal: telemetry.Span, Fields: map[string]telemetry.Field{
		"command": {Kind: telemetry.Enum, Required: true, Members: []telemetry.EnumMember{
			telemetry.EnumMember(CommandList),
			telemetry.EnumMember(CommandSearch),
			telemetry.EnumMember(CommandRead),
			telemetry.EnumMember(CommandSend),
			telemetry.EnumMember(CommandReply),
			telemetry.EnumMember(CommandMove),
			telemetry.EnumMember(CommandMark),
			telemetry.EnumMember(CommandFolders),
			telemetry.EnumMember(CommandContact),
			telemetry.EnumMember(CommandContactList),
			telemetry.EnumMember(CommandContactAdd),
			telemetry.EnumMember(CommandContactRemove),
			telemetry.EnumMember(CommandContactFind),
			telemetry.EnumMember(CommandIdentity),
			telemetry.EnumMember(CommandIdentityShow),
			telemetry.EnumMember(CommandIdentitySet),
			telemetry.EnumMember(CommandInstall),
			telemetry.EnumMember(CommandUninstall),
			telemetry.EnumMember(CommandEnable),
			telemetry.EnumMember(CommandDisable),
			telemetry.EnumMember(CommandDoctor),
			telemetry.EnumMember(CommandStatus),
			telemetry.EnumMember(CommandHealth),
			telemetry.EnumMember(CommandVersion),
			telemetry.EnumMember(CommandServe),
			telemetry.EnumMember(CommandCompletionBash),
			telemetry.EnumMember(CommandCompletionZsh),
			telemetry.EnumMember(CommandCompletionFish),
			telemetry.EnumMember(CommandCompletionPowershell),
		}},
		"outcome":    {Kind: telemetry.Enum, Required: true, Members: []telemetry.EnumMember{telemetry.EnumMember(OK), telemetry.EnumMember(Error)}},
		"elapsed_ms": {Kind: telemetry.Duration, Required: true},
	}},
	{Name: "beadle.mcp.tool", Version: 1, Signal: telemetry.Span, Fields: map[string]telemetry.Field{
		"tool": {Kind: telemetry.Enum, Required: true, Members: []telemetry.EnumMember{
			telemetry.EnumMember(ToolListMessages),
			telemetry.EnumMember(ToolSearchMessages),
			telemetry.EnumMember(ToolReadMessage),
			telemetry.EnumMember(ToolListFolders),
			telemetry.EnumMember(ToolSendEmail),
			telemetry.EnumMember(ToolReplyMessage),
			telemetry.EnumMember(ToolVerifySignature),
			telemetry.EnumMember(ToolShowMime),
			telemetry.EnumMember(ToolCheckTrust),
			telemetry.EnumMember(ToolMoveMessage),
			telemetry.EnumMember(ToolBatchMoveMessages),
			telemetry.EnumMember(ToolMarkMessage),
			telemetry.EnumMember(ToolBatchMarkMessages),
			telemetry.EnumMember(ToolDownloadAttachment),
			telemetry.EnumMember(ToolListContacts),
			telemetry.EnumMember(ToolFindContact),
			telemetry.EnumMember(ToolAddContact),
			telemetry.EnumMember(ToolRemoveContact),
			telemetry.EnumMember(ToolWhoami),
			telemetry.EnumMember(ToolSwitchIdentity),
			telemetry.EnumMember(ToolEnable),
			telemetry.EnumMember(ToolSetPollInterval),
			telemetry.EnumMember(ToolGetPollStatus),
		}},
		"outcome":    {Kind: telemetry.Enum, Required: true, Members: []telemetry.EnumMember{telemetry.EnumMember(OK), telemetry.EnumMember(Error), telemetry.EnumMember(Denied)}},
		"elapsed_ms": {Kind: telemetry.Duration, Required: true},
	}},
	{Name: "beadle.daemon.mission", Version: 1, Signal: telemetry.Span, Fields: map[string]telemetry.Field{
		"outcome":    {Kind: telemetry.Enum, Required: true, Members: []telemetry.EnumMember{telemetry.EnumMember(Created), telemetry.EnumMember(BelowTrust), telemetry.EnumMember(NoPermission), telemetry.EnumMember(NoCommand), telemetry.EnumMember(Failed)}},
		"elapsed_ms": {Kind: telemetry.Duration, Required: true},
	}},
}

// Client owns the process's independent telemetry lifecycle.
type Client struct {
	client *telemetry.Client
}

// New reads tool-wide configuration before starting asynchronous capture.
// Either configuration file can veto telemetry; unreadable settings fail closed.
func New(logger *slog.Logger) *Client {
	return &Client{client: telemetry.New(paths.Namespace, telemetry.Config{Disabled: disabledBySettings(), Diagnostics: logger}, schemas...)}
}

func disabledBySettings() bool {
	dir, err := paths.DataDir()
	if err != nil {
		return true
	}
	disabled := false
	for _, name := range []string{"email.json", "daemon.json"} {
		data, err := os.ReadFile(filepath.Clean(filepath.Join(dir, name)))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		var settings struct {
			Disabled bool `json:"telemetry_disabled"`
		}
		if err != nil || json.Unmarshal(data, &settings) != nil {
			return true
		}
		disabled = disabled || settings.Disabled
	}
	return disabled
}

// Shutdown permits a short best-effort flush, independent of operation results.
func (c *Client) Shutdown() {
	if c == nil || c.client == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	c.client.Shutdown(ctx)
}

// CommandForPath accepts Cobra's static command path, never its arguments.
func CommandForPath(path string) Command {
	command := strings.ReplaceAll(strings.TrimPrefix(path, "beadle-email "), " ", "_")
	for _, member := range schemas[0].Fields["command"].Members {
		if command == string(member) {
			return Command(member)
		}
	}
	return ""
}

// CLI records a completed CLI command.
func (c *Client) CLI(command Command, outcome Outcome, started time.Time) {
	c.record("beadle.cli.command", started, telemetry.EnumValue("command", telemetry.EnumMember(command)), telemetry.EnumValue("outcome", telemetry.EnumMember(outcome)))
}

// Mission records a completed new-mail decision.
func (c *Client) Mission(outcome Decision, started time.Time) {
	c.record("beadle.daemon.mission", started, telemetry.EnumValue("outcome", telemetry.EnumMember(outcome)))
}

type permissionKey struct{}

// StartMCP binds a per-call permission decision without inspecting result content.
func (c *Client) StartMCP(ctx context.Context, tool Tool) (context.Context, func(Outcome)) {
	started := time.Now()
	denied := &atomic.Bool{}
	return context.WithValue(ctx, permissionKey{}, denied), func(outcome Outcome) {
		if denied.Load() {
			outcome = Denied
		}
		c.record("beadle.mcp.tool", started, telemetry.EnumValue("tool", telemetry.EnumMember(tool)), telemetry.EnumValue("outcome", telemetry.EnumMember(outcome)))
	}
}

// PermissionDenied marks the current call at the permission gate.
func PermissionDenied(ctx context.Context) {
	if denied, ok := ctx.Value(permissionKey{}).(*atomic.Bool); ok {
		denied.Store(true)
	}
}

func (c *Client) record(name string, started time.Time, attrs ...telemetry.Value) {
	if c == nil || c.client == nil {
		return
	}
	elapsed := time.Since(started)
	attrs = append(attrs, telemetry.Elapsed("elapsed_ms", elapsed))
	c.client.Record(telemetry.Observation{Name: name, Version: 1, Started: started, Duration: elapsed, Attributes: attrs})
}
