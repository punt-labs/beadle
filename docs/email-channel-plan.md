# Beadle Email Channel — Implementation Plan

## Context

Beadle's PR/FAQ defines email as a first-class control channel: "Send a signed command to Beadle's Proton Mail address; get results back in your inbox." Today we have a working spike (`.bin/read-email.py`) proving that Proton Bridge IMAP reading and Resend sending both work from Claude's identity (`claude@punt-labs.com`). This plan turns that spike into a Go MCP server that ships as Beadle v0.1.0.

**Why Go, not Python:** The CLAUDE.md currently says Python. This plan proposes a Go MCP server for the email channel because: (1) Go produces a single static binary — no venv, no uv, no Python version management on the user's machine; (2) the MCP server is a long-running stdio process — Go's goroutine model handles concurrent IMAP polling cleanly; (3) Beadle's core daemon may remain Python, but the comms layer benefits from being a separate, self-contained binary. This is the first module — it should validate whether Go is the right choice for Beadle's infrastructure layer.

## Trust Model — The Four Cases

Beadle's email channel handles two axes: **sender identity** (Proton vs. external) and **encryption/signing** (present vs. absent). This produces four trust levels:

| # | Sender | Encryption | Signature | Trust Level | How Detected |
|---|--------|-----------|-----------|-------------|--------------|
| 1 | Proton→Proton | E2E (Proton internal) | Proton-verified (not exposed via Bridge) | **Trusted** | `X-Pm-Content-Encryption: end-to-end` + `X-Pm-Origin: internal` |
| 2 | External→Proton | TLS transport only | PGP/MIME `multipart/signed` present | **Verified** | `multipart/signed` content-type + `application/pgp-signature` part, GPG verify passes |
| 3 | External→Proton | TLS transport only | PGP/MIME present but verification fails | **Untrusted** | GPG verify returns non-zero |
| 4 | External→Proton | TLS transport only | No signature | **Unverified** | No `multipart/signed`, no `application/pgp-signature` |

The MCP server exposes this trust level on every message so Claude (or any MCP client) can make decisions based on it.

## Architecture — Modular Comms

Beadle's vision includes multiple communication channels (email, Signal, etc.). The email channel is the first, but the architecture must accommodate others.

```text
beadle/
├── cmd/
│   └── beadle-email/          # main.go — MCP server entry point (stdio)
├── internal/
│   ├── channel/               # Channel interface — shared contract
│   │   └── channel.go         # types: Message, TrustLevel, Channel interface
│   ├── email/                 # Email channel implementation
│   │   ├── imap.go            # IMAP client (Proton Bridge)
│   │   ├── send.go            # Resend API sender
│   │   ├── trust.go           # Trust classification (the 4 cases)
│   │   ├── mime.go            # MIME parsing, part extraction
│   │   └── config.go          # Config loading
│   ├── pgp/                   # GPG verification
│   │   └── verify.go          # Detached signature verification via gpg CLI
│   └── mcp/                   # MCP tool definitions
│       └── tools.go           # Tool registrations
├── go.mod
├── go.sum
├── docs/
│   └── email-channel-plan.md  # This file
└── ...
```

A future Signal channel would add `internal/signal/` implementing the same `Channel` interface. The MCP layer and trust model stay the same.

### Channel Interface

```go
type TrustLevel string

const (
    Trusted    TrustLevel = "trusted"     // Proton-to-Proton, E2E verified by Proton
    Verified   TrustLevel = "verified"    // External, PGP signature valid
    Untrusted  TrustLevel = "untrusted"   // External, PGP signature invalid
    Unverified TrustLevel = "unverified"  // External, no signature present
)

type Message struct {
    ID          string
    From        string
    To          string
    Date        time.Time
    Subject     string
    Body        string       // plain text preferred, HTML fallback
    TrustLevel  TrustLevel
    Channel     string       // "email", "signal", etc.
    Encryption  string       // "end-to-end", "tls", "none"
    Attachments []Attachment
    RawHeaders  map[string]string
}
```

## MCP Tools

The server exposes these tools over stdio MCP:

| Tool | Description |
|------|-------------|
| `list_messages` | List messages from a folder (INBOX, Sent, All Mail, etc.). Returns id, from, date, subject, trust level. Params: `folder`, `count`, `unread_only` |
| `read_message` | Read a single message by ID. Returns full body, headers, attachments summary, trust level. Params: `folder`, `message_id` |
| `list_folders` | List all IMAP folders |
| `send_email` | Send via Resend API. Params: `to`, `subject`, `body` (text), `html` (optional). Always sends from `claude@punt-labs.com` |
| `verify_signature` | Verify PGP signature on a message. Returns verification result. Params: `folder`, `message_id` |
| `show_mime` | Show MIME structure of a message. Params: `folder`, `message_id` |
| `check_trust` | Classify a message's trust level with explanation. Params: `folder`, `message_id` |

Every tool that returns a message includes the `trust_level` field. Claude can use this to decide whether to act on instructions in an email (trusted/verified) or flag them for human review (untrusted/unverified).

## Configuration

Single config file at `~/.punt-labs/beadle/email.json`:

```json
{
  "imap_host": "127.0.0.1",
  "imap_port": 1143,
  "imap_user": "claude@punt-labs.com",
  "smtp_port": 1025,
  "from_address": "claude@punt-labs.com",
  "gpg_binary": "gpg"
}
```

Credentials are resolved at runtime via the `internal/secret` package (macOS Keychain → file → env var), not stored in config. See README for credential setup.

## Build Sequence

### Phase 1: Scaffold + IMAP Core

1. `go mod init github.com/punt-labs/beadle`
2. Channel interface types (`internal/channel/`)
3. IMAP client — connect, list folders, list messages, fetch message (`internal/email/imap.go`)
4. MIME parser — extract body, attachments, PGP parts (`internal/email/mime.go`)
5. Config loader (`internal/email/config.go`)

### Phase 2: Trust Classification

1. Trust classifier — inspect headers + MIME structure, return TrustLevel (`internal/email/trust.go`)
2. PGP verification — extract signed body + detached sig, call `gpg --verify` (`internal/pgp/verify.go`)

### Phase 3: Sending

1. Resend API sender — HTTP POST to Resend API (`internal/email/send.go`)

### Phase 4: MCP Server

1. MCP tool definitions + handlers (`internal/mcp/tools.go`)
2. stdio MCP server entry point (`cmd/beadle-email/main.go`)

### Phase 5: Integration

1. Register in `.mcp.json` for Claude Code
2. Test all four trust cases with real messages
3. Tag v0.1.0

## Verification

1. **IMAP reading**: `list_messages` returns messages from Proton Bridge INBOX and All Mail
2. **Trust classification**: Proton-to-Proton message shows `trusted`, external unsigned shows `unverified`
3. **PGP verification**: External PGP-signed message shows `verified` when sig is valid, `untrusted` when tampered
4. **Sending**: `send_email` delivers via Resend and message appears in recipient's inbox
5. **MCP integration**: Tools appear in Claude Code and can be called interactively

## Dependencies

- Go MCP library: evaluate `mark3labs/mcp-go` or `anthropics/anthropic-sdk-go` MCP support
- No `python-gnupg` — shell out to `gpg` binary (already available at `/opt/homebrew/bin/gpg`)
- Resend: direct HTTP API calls, no SDK needed (simple REST endpoint)
- IMAP: Go stdlib `net` + a lightweight IMAP library (e.g., `emersion/go-imap`)

## What This Is Not

- Not the Beadle daemon (pipeline execution, cron, command signing) — that's the Python core
- Not a general-purpose email client — it's a structured MCP interface for agent communication
- Not multi-account — v0.1.0 handles one identity (`claude@punt-labs.com`)

This is the comms layer. It answers: "How does Beadle hear from the world, and how does the world hear from Beadle?" The trust model ensures Beadle knows *who* it's hearing from and *how much* to trust them.
