# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Added

- CLI parity for email operations: `list`, `read`, `send`, `move`, `folders`
  subcommands calling the same internal functions as MCP tools. Contact
  name resolution works in CLI send (`--to jim` resolves to stored email).
- Global CLI flags: `--json`/`-j` for JSON output, `--verbose`/`-v` for
  debug logging, `--quiet`/`-q` for errors only. `-v` changed from
  `--version` to `--verbose` per punt-kit standard.
- `install` subcommand: creates `~/.punt-labs/beadle/` dirs, interactive
  `email.json` config creation, MCP server registration, runs doctor.
- `uninstall` subcommand: removes MCP registration, deployed commands,
  and beadle permissions from `settings.json`.
- `TrySendChain` extracted from MCP handler to `internal/email/chain.go`
  for CLI reuse. Contact resolve helpers extracted to `internal/email/resolve.go`.
- Configurable inbox polling. SessionStart hook auto-schedules `/inbox` via
  CronCreate at a configurable interval (default 30m). Configure with
  `/inbox 5m`, `/inbox 1h`, or `/inbox n` to disable. Settings persist in
  `.claude/beadle.local.md`. Run `/inbox status` to check current config.
- Address book with name-based recipient resolution. 4 new MCP tools
  (`list_contacts`, `find_contact`, `add_contact`, `remove_contact`) and
  matching CLI subcommands (`beadle-email contact list/add/remove/find`).
  `send_email` resolves names inline — `/mail jim` works in a single
  roundtrip. Contacts stored at `~/.punt-labs/beadle/contacts.json` with
  GPG key ID and alias support.
- Filesystem consolidation: all runtime data now under `~/.punt-labs/beadle/`.
  Config at `email.json`, secrets at `secrets/`, attachments at
  `attachments/<mailbox>/`, contacts at `contacts.json`. Single root function
  (`paths.DataDir()`) in `internal/paths/`.
- `send_email`: cc and bcc support. `to` now accepts comma-separated addresses
  for multiple recipients. New optional `cc` and `bcc` string parameters
  (comma-separated). BCC addresses are envelope-only (never in headers or tool
  output). Resend API path passes cc/bcc as native array fields.

### Fixed

- `ComposeRaw`: multipart boundary value now properly quoted via
  `mime.FormatMediaType` per RFC 2046 §5.1.1.

## [0.3.1] - 2026-03-15

### Added

- Version injection from git tags via ldflags at build time

### Fixed

- `list_messages`: use `UIDSearch` for unread filter instead of `Search`, return
  empty slice instead of nil when no messages match

## [0.3.0] - 2026-03-15

### Added

- `send_email`: file attachment support via `attachments` parameter (list of
  absolute file paths). Builds `multipart/mixed` MIME for SMTP and structured
  attachments for Resend API. Per-file 25 MB limit, auto-detected MIME types.
- `download_attachment`: extract attachment content by MIME part index (from
  `show_mime`). Saves to `~/.punt-labs/beadle/attachments/<mailbox>/` and returns the path.

### Fixed

- Trailing CRLF missing from text body in multipart MIME messages
- `ParseMIME` now surfaces unreadable parts as attachments with `(read error)`
  instead of silently dropping them
- PostToolUse suppress-output hook: add `download_attachment` handler to prevent
  raw JSON from leaking into the conversation panel.
- SessionStart hook: deploy top-level commands in dev mode when no prod plugin
  is installed. Previously, `/inbox`, `/mail`, and `/send` were never deployed.

## [0.2.0] - 2026-03-13

### Added

- Go MCP server (`beadle-email`) for the email communication channel
- 7 MCP tools: `list_messages`, `read_message`, `list_folders`, `send_email`,
  `verify_signature`, `show_mime`, `check_trust`
- IMAP client connecting to Proton Bridge via STARTTLS
- SMTP sender via Proton Bridge with Resend API fallback
- MIME parser with body extraction, attachment summary, and structure inspection
- Four-level trust classification: trusted (Proton-to-Proton), verified
  (external + valid PGP), untrusted (external + bad PGP), unverified (no sig)
- Inline PGP verification in `list_messages` — signed messages show `verified`
  or `untrusted` in the listing without a separate verification call
- PGP signature verification via `gpg` CLI with isolated GNUPGHOME and system
  keyring bridging for keys not attached to the message
- PGP signing infrastructure (`pgp/sign.go`) ready for future SES integration
- Credential resolution: macOS Keychain, secret files, environment variables
- CLI subcommands: `serve`, `version`, `doctor`, `status`
- `doctor` checks: secret backends, config, IMAP password, Resend API key, GPG
  binary, GPG signing key, GPG passphrase, Proton Bridge SMTP reachability
- Modular channel interface (`internal/channel/`) for future Signal etc.
