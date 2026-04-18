# beadle

> A programmable agent daemon controlled by email, secured by GPG.

[![License](https://img.shields.io/github/license/punt-labs/beadle)](LICENSE)
[![CI](https://img.shields.io/github/actions/workflow/status/punt-labs/beadle/test.yml?label=CI)](https://github.com/punt-labs/beadle/actions/workflows/test.yml)
[![Go](https://img.shields.io/badge/Go-1.26+-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![Go Report Card](https://goreportcard.com/badge/github.com/punt-labs/beadle)](https://goreportcard.com/report/github.com/punt-labs/beadle)
[![Working Backwards](https://img.shields.io/badge/Working_Backwards-hypothesis-lightgrey)](./prfaq.pdf)

Beadle is an autonomous agent that receives instructions via email and
executes them as multi-stage pipelines. Commands are programs, the
daemon is the shell, pipelines are pipes, and GPG signatures are sudo.
The owner controls what beadle can do by signing command definitions.
The trust gate controls who can trigger it.

**How it works.** You send an email. Beadle verifies your identity
(PGP signature or Proton E2E encryption), checks your permissions,
decomposes your instruction into a pipeline of commands, and executes
them — mixing AI reasoning with fast CLI tools. You get the result
back as an email reply. The daemon runs on your machine. No cloud
service, no API keys shared with third parties.

**Example.** You email "summarize this" from your PGP-verified account:

1. Beadle verifies your identity and `x` (execute) permission
2. The planner maps "summarize" to a pipeline: `[summarize, notify, reply]`
3. Stage 0: Claude reads your email and produces a structured summary (45s)
4. Stage 1: `biff wall` broadcasts "New summary: Deploy plan" to the team (10ms)
5. Stage 2: Claude formats the summary and emails it back to you (45s)

The summary flows through the pipeline as JSON. Stage 1 is a
side-effect (passthrough) — it reads the data but doesn't modify it.
Stage 2 receives the full summary, not stage 1's "ok" output.

**Two types of commands:**

- **Claude commands** spawn an AI session for reasoning tasks:
  summarization, analysis, code generation. 45-60 seconds per stage.
- **CLI commands** exec binaries directly for deterministic operations:
  notifications, status checks, data transforms. Milliseconds per stage.

Both types are defined as YAML files, GPG-signed by the owner. The
daemon validates signatures at startup and rejects unsigned commands.

## beadle-email

The shipping component is `beadle-email` — an MCP server that gives
Claude Code a real email address with cryptographic trust at every
layer.

- **Claude emails you.** Session summaries, build reports, deploy
  notifications — anything Claude produces can be mailed to you or
  your team. No webhook plumbing, no Slack integration. Just email.
- **You email Claude.** Send instructions, ask questions, or forward
  context from your phone. Beadle's four-level PGP trust model and
  per-contact permissions (`rwx`) control exactly what Claude can
  read, reply to, and act on — nothing is implicit.

**Platforms:** macOS, Linux

## Quick Start

```bash
curl -fsSL https://raw.githubusercontent.com/punt-labs/beadle/5cdeaac/install.sh | sh
```

Downloads the `beadle-email` binary, verifies its SHA256 checksum, and attempts to install the Claude Code plugin (MCP tools + slash commands + hooks). If plugin installation fails, the script falls back to registering the standalone MCP server (no slash commands or hooks). Runs `doctor` to check your setup. Restart Claude Code after install. If you previously registered `beadle-email` as a standalone MCP server via `claude mcp add`, remove it first with `claude mcp remove beadle-email` to avoid duplicate registrations.

**Plugin install** provides MCP tools, slash commands (`/inbox`, `/mail`, `/send`, `/contacts`), and hooks (two-channel display, automatic session setup). **Standalone MCP** provides only MCP tools --- no slash commands or hooks.

<details>
<summary>Inspect before running</summary>

```bash
curl -fsSL https://raw.githubusercontent.com/punt-labs/beadle/5cdeaac/install.sh -o install.sh
cat install.sh
sh install.sh
```

</details>

<details>
<summary>Manual install (other MCP clients)</summary>

```bash
mkdir -p ~/.local/bin
curl -fsSL https://github.com/punt-labs/beadle/releases/latest/download/beadle-email-darwin-arm64 -o ~/.local/bin/beadle-email
chmod +x ~/.local/bin/beadle-email
```

Replace `darwin-arm64` with your platform: `darwin-amd64`, `linux-arm64`, `linux-amd64`.
Ensure `~/.local/bin` is on your `PATH`. Configure your MCP client to run `beadle-email serve`.

</details>

<details>
<summary>Prerequisites</summary>

- An IMAP server for reading (Proton Bridge on localhost, Fastmail, Gmail IMAP, etc.)
- An SMTP server for sending (Proton Bridge, or any SMTP with STARTTLS support; implicit TLS/SMTPS on port 465 is not yet supported)
- [GPG](https://gnupg.org/) for signature verification
- (Optional) [Resend](https://resend.com) API key for fallback sending

</details>

## Features

- **17 MCP tools** --- list, read, send, move/archive, download attachments, verify signatures, inspect MIME, classify trust, list folders, address book (list/find/add/remove contacts), whoami, `switch_identity`, inbox polling (set interval, get status)
- **Multi-identity via ethos** --- identity resolved per-request from ethos sidecar. Repo-local config pins identity. Mid-session switching via `switch_identity` tool. Fallback to `default-identity` file
- **Two-dimensional trust** --- transport trust (trusted/verified/untrusted/unverified) + identity permissions (rwx per contact per identity). Both must pass before autonomous action
- **Four-level transport trust** --- trusted (Proton-to-Proton E2E), verified (valid PGP), untrusted (bad PGP), unverified (no signature)
- **Inline PGP verification** --- `list_messages` runs `gpg --verify` on signed messages automatically
- **Slash commands** (plugin only) --- `/inbox` (process your inbox), `/mail` (email someone), `/send` (multi-channel outbound)
- **Two-channel display** (plugin only) --- compact panel summaries with full data in context, no raw JSON in conversation
- **Proton Bridge native** --- IMAP STARTTLS for reading, SMTP for sending, Resend API fallback
- **Credential isolation** --- secrets resolved at runtime from OS keychain, never stored in config files
- **Health checks** --- `doctor` validates all dependencies; `status` shows active identity and configuration

## MCP Tools

| Tool | Purpose |
|------|--------|
| `list_messages` | List messages with trust levels. PGP signatures verified inline. |
| `read_message` | Read full message body, headers, attachments, and trust classification. |
| `send_email` | Send via Proton Bridge SMTP (primary) or Resend API (fallback). Resolves contact names inline. |
| `move_message` | Move a message to another folder. Defaults to Archive. |
| `list_folders` | List all IMAP mailbox folders. |
| `show_mime` | Inspect multipart MIME structure, PGP parts, and attachments. |
| `verify_signature` | Verify PGP signature on a message. Returns signer info and key ID. |
| `check_trust` | Detailed trust classification with encryption type, origin analysis, and identity permission for sender. |
| `download_attachment` | Extract an attachment by MIME part index (from `show_mime`). Saves to identity-scoped directory. |
| `list_contacts` | List all contacts with effective permissions for the active identity. |
| `find_contact` | Look up a contact by name, email, or alias. Shows effective permissions. |
| `add_contact` | Add a contact (name, email, aliases, GPG key ID, permissions). |
| `remove_contact` | Remove a contact by name. |
| `whoami` | Return the active identity (email, display name, ethos handle). |
| `switch_identity` | Switch the active identity for this session. Pass an ethos handle or empty to reset. |
| `set_poll_interval` | Set automatic inbox polling interval (1m, 5m, 10m, 15m, 30m, 1h, 2h) or disable (`n`). |
| `get_poll_status` | Return current polling configuration: interval, active state, last check time, unseen count. |

## Commands

Available when installed as a Claude Code plugin.

| Command | What it does |
|---------|-------------|
| `/inbox` | Check beadle's email inbox. Optional natural language filter. |
| `/inbox 5m` | Set inbox polling interval (1m, 5m, 10m, 15m, 30m, 1h, 2h). |
| `/inbox n` | Disable automatic inbox polling. |
| `/inbox status` | Show current polling configuration. |
| `/mail` | Mail something to the owner or a specific recipient. |
| `/send` | Send via any channel (email today, Signal later). |
| `/contacts` | Manage address book (list, add with permissions, remove, find). |

## Setup

<details>
<summary>Credential setup</summary>

Beadle resolves credentials at runtime through a priority chain: OS keychain → secret file → environment variable. On Linux, the keychain layer tries `pass` first, then `secret-tool` (libsecret / GNOME Keyring). On macOS, it uses the system Keychain.

```bash
# macOS Keychain (macOS)
security add-generic-password -s beadle -a imap-password -w 'your-bridge-password'
security add-generic-password -s beadle -a resend-api-key -w 'your-resend-key'
security add-generic-password -s beadle -a gpg-passphrase -w 'your-gpg-passphrase'

# pass (Linux, recommended — GPG-encrypted at rest with your own key)
pass insert beadle/imap-password    # prompts for the value, hides input
pass insert beadle/resend-api-key
pass insert beadle/gpg-passphrase

# secret-tool (Linux, fallback — GNOME Keyring via libsecret)
secret-tool store --label='beadle imap-password' service beadle account imap-password
secret-tool store --label='beadle resend-api-key' service beadle account resend-api-key
secret-tool store --label='beadle gpg-passphrase' service beadle account gpg-passphrase

# Or secret files (~/.punt-labs/beadle/secrets/<name>, mode 600)
mkdir -p ~/.punt-labs/beadle/secrets
printf '%s' 'your-bridge-password' > ~/.punt-labs/beadle/secrets/imap-password
chmod 600 ~/.punt-labs/beadle/secrets/imap-password

# Or environment variables
export BEADLE_IMAP_PASSWORD='your-bridge-password'
export BEADLE_RESEND_API_KEY='your-resend-key'
```

Create the configuration file (`~/.punt-labs/beadle/email.json`) with your connection parameters.

**Proton Bridge (localhost):**

```bash
mkdir -p ~/.punt-labs/beadle
cat > ~/.punt-labs/beadle/email.json << 'EOF'
{
  "imap_host": "127.0.0.1",
  "imap_port": 1143,
  "imap_user": "you@example.com",
  "smtp_host": "127.0.0.1",
  "smtp_port": 1025,
  "smtp_user": "you@example.com",
  "from_address": "you@example.com",
  "poll_interval": "30m"
}
EOF
```

**External IMAP + SMTP (Fastmail, Gmail, etc.):**

```json
{
  "imap_host": "imap.fastmail.com",
  "imap_port": 143,
  "imap_user": "you@fastmail.com",
  "smtp_host": "smtp.fastmail.com",
  "smtp_port": 587,
  "smtp_user": "you@fastmail.com",
  "from_address": "you@fastmail.com",
  "poll_interval": "10m"
}
```

</details>

## Trust Model

| Level | Sender | Detection | What It Means |
|-------|--------|-----------|---------------|
| `trusted` | Proton → Proton | `X-Pm-Content-Encryption: end-to-end` + `X-Pm-Origin: internal` | End-to-end encrypted by Proton infrastructure |
| `verified` | External | `gpg --verify` returns exit 0 | Valid PGP signature from a known key |
| `untrusted` | External | `gpg --verify` returns non-zero | PGP signature present but invalid or from unknown key |
| `unverified` | External | No `multipart/signed` | No PGP signature present |

PGP verification uses an isolated GNUPGHOME per operation. When no key is attached to the message, keys are bridged from the system keyring (`~/.gnupg/`) into the isolated environment.

## Identity

Beadle reads identity from [ethos](https://github.com/punt-labs/ethos) (sidecar pattern — file reads, no import dependency). Resolution chain:

1. **Repo-local config** — `.punt-labs/ethos.yaml` with `agent: <handle>`
2. **Global ethos active** — `~/.punt-labs/ethos/active`
3. **Default identity** — `~/.punt-labs/beadle/default-identity` (plain email string)

Each identity gets its own directory under `~/.punt-labs/beadle/identities/<email>/` with separate `email.json`, `contacts.json`, and `attachments/`. Root files are auto-migrated on first use.

## Contact Permissions

Each contact has an optional `permissions` map keyed by identity email. Permissions use the Unix rwx model:

| Permission | Meaning |
|------------|--------|
| `r` (read) | Beadle reads and surfaces the message. No autonomous action. |
| `w` (write) | Beadle may compose and send replies to this contact. |
| `x` (execute) | Beadle may execute instructions from this contact. |

All permissions are stored explicitly. There are no implicit overrides. Contacts without explicit permissions default to `---` (no permissions). The address book is a whitelist: messages from unknown senders appear redacted in listings and cannot be read. `r` and `w` are enforced; `x` enforcement is planned. This is orthogonal to transport trust — both must be sufficient for autonomous action.

## CLI

```bash
beadle-email list [--folder F] [--count N] [--unread]   # List messages
beadle-email read <uid> [--folder F]                    # Read a message
beadle-email send --to ADDR --subject S --body B        # Send an email
beadle-email move <uid> [--folder F] [--to DEST]        # Move a message
beadle-email folders                                    # List IMAP folders
beadle-email contact list|add|remove|find               # Manage contacts
beadle-email install                                    # Set up beadle-email
beadle-email uninstall                                  # Remove beadle-email
beadle-email serve [--config PATH]                      # Start MCP server
beadle-email doctor [--config PATH]                     # Check installation health
beadle-email status [--config PATH]                     # Current state summary
beadle-email identity                                   # Show active identity
beadle-email identity set <handle>                      # Set per-repo identity
beadle-email version                                    # Print version

# Global flags (supported by most subcommands; not: install, uninstall, version)
beadle-email --json list                                # JSON output
beadle-email --verbose doctor                           # Debug logging
beadle-email --quiet send --to ...                      # Errors only
```

## Documentation

[Design Log](DESIGN.md) |
[Pipeline v2 Design](docs/pipeline-v2-design.md) |
[Email Channel Plan](docs/email-channel-plan.md) |
[Changelog](CHANGELOG.md)

## Development

```bash
make build                 # Build beadle-email binary
make check                 # All quality gates: vet + staticcheck + markdownlint + tests
make lint                  # Lint only (vet + staticcheck)
make test                  # Tests only (go test -race)
make dist                  # Cross-compile for darwin/linux arm64/amd64
make help                  # List all targets
```

## License

[MIT](LICENSE)
