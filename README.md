# beadle

> Autonomous agent daemon with cryptographic owner control.

[![License](https://img.shields.io/github/license/punt-labs/beadle)](LICENSE)
[![Working Backwards](https://img.shields.io/badge/Working_Backwards-hypothesis-lightgrey)](./prfaq.pdf)

Beadle runs on your machine as a background daemon. Every action requires a GPG-signed instruction from the owner, every command declares its permissions upfront, and the audit log is tamperproof. The agent has zero authority of its own — trust is earned through cryptographic proof, not granted by default.

The first shipping component is `beadle-email` — an MCP server providing email communication tools over Proton Bridge with a four-level PGP trust model. Written in Go.

**Platforms:** macOS, Linux

## Install

Requires [Claude Code](https://docs.anthropic.com/en/docs/claude-code).

```bash
curl -fsSL https://raw.githubusercontent.com/punt-labs/beadle/02a2d03/install.sh | sh
```

<details>
<summary>Manual install</summary>

```bash
mkdir -p ~/.local/bin
curl -fsSL https://github.com/punt-labs/beadle/releases/latest/download/beadle-email-darwin-arm64 -o ~/.local/bin/beadle-email
chmod +x ~/.local/bin/beadle-email
```

Replace `darwin-arm64` with your platform: `darwin-amd64`, `linux-arm64`, `linux-amd64`.
Ensure `~/.local/bin` is on your `PATH`.

</details>

<details>
<summary>Inspect before running</summary>

```bash
curl -fsSL https://raw.githubusercontent.com/punt-labs/beadle/02a2d03/install.sh -o install.sh
cat install.sh
sh install.sh
```

</details>

<details>
<summary>Prerequisites</summary>

- [Proton Bridge](https://proton.me/mail/bridge) running on localhost (IMAP 1143, SMTP 1025)
- [GPG](https://gnupg.org/) for signature verification
- A Proton Mail account configured in Bridge
- (Optional) [Resend](https://resend.com) API key for fallback sending

</details>

Register with Claude Code by adding to `.mcp.json`:

```json
{
  "mcpServers": {
    "beadle-email": {
      "command": "~/.local/bin/beadle-email",
      "args": ["serve"]
    }
  }
}
```

<details>
<summary>Credential setup</summary>

Beadle resolves credentials at runtime through a priority chain: macOS Keychain → secret file → environment variable.

```bash
# macOS Keychain (recommended)
security add-generic-password -s beadle -a imap-password -w 'your-bridge-password'
security add-generic-password -s beadle -a resend-api-key -w 'your-resend-key'
security add-generic-password -s beadle -a gpg-passphrase -w 'your-gpg-passphrase'

# Or secret files (~/.config/beadle/<name>, mode 600)
echo -n 'your-bridge-password' > ~/.config/beadle/imap-password
chmod 600 ~/.config/beadle/imap-password

# Or environment variables
export BEADLE_IMAP_PASSWORD='your-bridge-password'
export BEADLE_RESEND_API_KEY='your-resend-key'
```

Configuration file (`~/.config/beadle/email.json`) stores connection parameters only:

```json
{
  "imap_host": "127.0.0.1",
  "imap_port": 1143,
  "imap_user": "you@example.com",
  "smtp_port": 1025,
  "from_address": "you@example.com"
}
```

</details>

## Features

- **7 MCP tools** --- list, read, send, verify signatures, inspect MIME, classify trust, list folders
- **Four-level trust model** --- trusted (Proton-to-Proton E2E), verified (valid PGP), untrusted (bad PGP), unverified (no signature)
- **Inline PGP verification** --- `list_messages` runs `gpg --verify` on signed messages automatically, no separate verification step needed
- **Proton Bridge native** --- connects via IMAP STARTTLS for reading, SMTP for sending, with Resend API fallback
- **Credential isolation** --- secrets resolved at runtime from OS keychain, never stored in config files
- **MIME inspection** --- full multipart structure, attachment enumeration, PGP part detection
- **Health checks** --- `doctor` validates all dependencies; `status` shows current configuration

## What It Looks Like

### List messages with trust levels

```text
> list_messages

[
  {"id": "1", "from": "jim@luminating.us", "subject": "Hello",       "trust_level": "trusted"},
  {"id": "4", "from": "user@icloud.com",   "subject": "External",    "trust_level": "unverified"},
  {"id": "5", "from": "user@icloud.com",   "subject": "Signed Test", "trust_level": "verified", "has_sig": true}
]
```

Messages from Proton-to-Proton senders show `trusted`. External messages with valid PGP signatures show `verified`. Unsigned external messages show `unverified`.

### Verify a PGP signature

```text
> verify_signature message_id="5"

{
  "valid": true,
  "signer": "Jim Freeman (Personal iCloud Key) <user@icloud.com>",
  "key_id": "2ACCA3DB52E5C2606E6F0883FFB3F64592BB7C3A",
  "trust_level": "verified"
}
```

### Inspect MIME structure

```text
> show_mime message_id="5"

[
  {"index": 0, "content_type": "text/plain", "size": 42},
  {"index": 1, "content_type": "application/pgp-signature", "filename": "signature.asc", "size": 917}
]
```

### Check installation health

```text
$ ./beadle-email doctor

[+] secret_backends  macOS Keychain, file (~/.config/beadle/), environment variable
[+] config           /Users/you/.config/beadle/email.json
[+] imap_password
[+] resend_api_key
[+] gpg              /opt/homebrew/bin/gpg
[+] gpg_signing_key  you@example.com
[+] gpg_passphrase
[+] smtp             127.0.0.1:1025
```

## MCP Tools

| Tool | Purpose |
|------|---------|
| `list_messages` | List messages with trust levels. PGP signatures verified inline. |
| `read_message` | Read full message body, headers, attachments, and trust classification. |
| `list_folders` | List all IMAP mailbox folders. |
| `send_email` | Send via Proton Bridge SMTP (primary) or Resend API (fallback). |
| `verify_signature` | Verify PGP signature on a message. Returns signer info and key ID. |
| `show_mime` | Inspect multipart MIME structure, PGP parts, and attachments. |
| `check_trust` | Detailed trust classification with encryption type and origin analysis. |

## CLI

```bash
beadle-email serve [--config PATH]    # Start MCP server (stdio transport, default)
beadle-email version                  # Print version
beadle-email doctor [--config PATH]   # Check installation health
beadle-email status [--config PATH]   # Current configuration summary
```

## Trust Model

Trust classification happens at two layers: header inspection during listing, and full PGP verification for signed messages.

| Level | Sender | Detection | What It Means |
|-------|--------|-----------|---------------|
| `trusted` | Proton → Proton | `X-Pm-Content-Encryption: end-to-end` + `X-Pm-Origin: internal` | End-to-end encrypted by Proton infrastructure |
| `verified` | External | `gpg --verify` returns exit 0 | Valid PGP signature from a known key |
| `untrusted` | External | `gpg --verify` returns non-zero | PGP signature present but invalid or from unknown key |
| `unverified` | External | No `multipart/signed` | No PGP signature present |

PGP verification uses an isolated GNUPGHOME per operation. When no key is attached to the message, keys are bridged from the system keyring (`~/.gnupg/`) into the isolated environment.

## Sending

Outbound email uses a two-tier sender chain:

1. **Proton Bridge SMTP** (primary) --- passes SPF/DKIM/DMARC for the configured domain. Proton handles its own encryption for Proton-to-Proton recipients.
2. **Resend API** (fallback) --- used when Proton Bridge is unavailable.

PGP signing of outbound messages is tracked as future work ([beadle-atz](https://github.com/punt-labs/beadle)). The blocker: Proton Bridge strips `multipart/signed` MIME envelopes from outbound messages, and Resend does not support raw MIME. Amazon SES (`SendRawEmail`) is the planned transport for PGP-signed outbound delivery.

## Architecture

```text
beadle/
├── cmd/beadle-email/       CLI entry point: serve, version, doctor, status
├── internal/
│   ├── channel/            Channel interface: Message, TrustLevel, shared types
│   ├── email/              IMAP client, MIME parser, trust classifier, SMTP/Resend senders
│   ├── mcp/                MCP tool definitions and handlers (7 tools)
│   ├── pgp/                GPG signature verification and signing via gpg CLI
│   └── secret/             Credential resolution: OS keychain → file → env var
├── Makefile                Quality gates (make check = vet + staticcheck + markdownlint + tests)
└── docs/
    └── email-channel-plan.md
```

## Design Principles

- **Zero agent authority.** Every action requires a GPG-signed instruction from the owner. The daemon has no independent decision-making.
- **Preflight before execute.** All permissions are validated before any command runs.
- **Isolated keychain.** PGP operations use temporary GNUPGHOME directories, never touching the user's system keyring.
- **Credentials never in config.** Connection parameters in JSON, secrets resolved at runtime from OS keychain or environment.
- **Non-expiring keys rejected.** All command-signing keys must have an expiration date.

## Roadmap

### Shipped

- Email channel MCP server (Go) with 7 tools
- Four-level trust model with inline PGP verification
- IMAP via Proton Bridge, sending via SMTP + Resend fallback
- Credential resolution chain (Keychain → file → env)
- MIME parsing and structure inspection
- GPG signature verification with system keyring bridging
- `doctor` and `status` CLI diagnostics

### Next

| Phase | What Ships |
|-------|-----------|
| **PGP outbound signing** | Amazon SES transport for `multipart/signed` delivery ([beadle-atz](https://github.com/punt-labs/beadle)) |
| **Linux keychain** | `libsecret` / `secret-tool` credential backend |
| **Daemon** | Pipeline execution, GPG-signed command documents, tamperproof audit log |

## Documentation

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

TBD
