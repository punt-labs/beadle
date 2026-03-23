# beadle

> Autonomous agent daemon with cryptographic owner control.

[![License](https://img.shields.io/github/license/punt-labs/beadle)](LICENSE)
[![CI](https://img.shields.io/github/actions/workflow/status/punt-labs/beadle/test.yml?label=CI)](https://github.com/punt-labs/beadle/actions/workflows/test.yml)
[![Working Backwards](https://img.shields.io/badge/Working_Backwards-hypothesis-lightgrey)](./prfaq.pdf)

Beadle runs on your machine as a background daemon. Every action requires a GPG-signed instruction from the owner, every command declares its permissions upfront, and the audit log is tamperproof. The daemon executes no action without a GPG-signed instruction from the owner; no authority is implicit.

The first shipping component is `beadle-email` — an MCP server providing email communication tools over Proton Bridge with a four-level PGP trust model. Written in Go.

**Platforms:** macOS, Linux

## Quick Start

```bash
curl -fsSL https://raw.githubusercontent.com/punt-labs/beadle/9bb2f4b/install.sh | sh
```

Downloads the `beadle-email` binary, verifies its SHA256 checksum, and attempts to install the Claude Code plugin (MCP tools + slash commands + hooks). If plugin installation fails, the script falls back to registering the standalone MCP server (no slash commands or hooks). Runs `doctor` to check your setup. Restart Claude Code after install. If you previously registered `beadle-email` as a standalone MCP server via `claude mcp add`, remove it first with `claude mcp remove beadle-email` to avoid duplicate registrations.

<details>
<summary>Inspect before running</summary>

```bash
curl -fsSL https://raw.githubusercontent.com/punt-labs/beadle/9bb2f4b/install.sh -o install.sh
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

- [Proton Bridge](https://proton.me/mail/bridge) running on localhost (IMAP 1143, SMTP 1025)
- [GPG](https://gnupg.org/) for signature verification
- A Proton Mail account configured in Bridge
- (Optional) [Resend](https://resend.com) API key for fallback sending

</details>

## Features

- **15 MCP tools** --- list, read, send, move/archive, download attachments, verify signatures, inspect MIME, classify trust, list folders, address book (list/find/add/remove contacts), whoami, switch identity
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
| `switch_identity` | Switch the active identity for this session. Pass an ethos handle or empty to reset. |

## Commands

Available when installed as a Claude Code plugin.

| Command | What it does |
|---------|-------------|
| `/inbox` | Check beadle's email inbox. Optional natural language filter. |
| `/inbox 5m` | Set inbox polling interval (5m, 10m, 15m, 30m, 1h, 2h). |
| `/inbox n` | Disable automatic inbox polling. |
| `/inbox status` | Show current polling configuration. |
| `/mail` | Mail something to the owner or a specific recipient. |
| `/send` | Send via any channel (email today, Signal later). |
| `/contacts` | Manage address book (list, add, remove, find). |

## Setup

<details>
<summary>Credential setup</summary>

Beadle resolves credentials at runtime through a priority chain: macOS Keychain (macOS) or libsecret (Linux) → secret file → environment variable.

```bash
# macOS Keychain (recommended)
security add-generic-password -s beadle -a imap-password -w 'your-bridge-password'
security add-generic-password -s beadle -a resend-api-key -w 'your-resend-key'
security add-generic-password -s beadle -a gpg-passphrase -w 'your-gpg-passphrase'

# Or secret files (~/.punt-labs/beadle/secrets/<name>, mode 600)
mkdir -p ~/.punt-labs/beadle/secrets
echo -n 'your-bridge-password' > ~/.punt-labs/beadle/secrets/imap-password
chmod 600 ~/.punt-labs/beadle/secrets/imap-password

# Or environment variables
export BEADLE_IMAP_PASSWORD='your-bridge-password'
export BEADLE_RESEND_API_KEY='your-resend-key'
```

Create the configuration file (`~/.punt-labs/beadle/email.json`) with your connection parameters:

```bash
mkdir -p ~/.punt-labs/beadle
cat > ~/.punt-labs/beadle/email.json << 'EOF'
{
  "imap_host": "127.0.0.1",
  "imap_port": 1143,
  "imap_user": "you@example.com",
  "smtp_port": 1025,
  "from_address": "you@example.com"
}
EOF
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

1. **Repo-local config** — `.punt-labs/ethos/config.yaml` with `agent: <handle>`
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

# Global flags (work with any subcommand)
beadle-email --json list                                # JSON output
beadle-email --verbose doctor                           # Debug logging
beadle-email --quiet send --to ...                      # Errors only
```

## Documentation

[Design Log](DESIGN.md) |
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
