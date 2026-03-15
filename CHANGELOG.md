# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Added

- `send_email`: file attachment support via `attachments` parameter (list of
  absolute file paths). Builds `multipart/mixed` MIME for SMTP and structured
  attachments for Resend API. Per-file 25 MB limit, auto-detected MIME types.

### Fixed

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
