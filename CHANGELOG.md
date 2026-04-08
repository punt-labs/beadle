# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

## [0.10.0] - 2026-04-08

### Added

- Linux keychain backend: `internal/secret/keychain_linux.go` now
  resolves credentials via `pass` (primary) and `secret-tool`
  (libsecret / GNOME Keyring, fallback), replacing the v0.1.1-era
  stub that returned empty/false for everything. If both are
  installed, `pass` wins (GPG-encrypted at rest, matches Proton
  Bridge vault). Namespace: pass `beadle/<name>`, secret-tool
  `service=beadle account=<name>`. See DES-017 for the rationale
  and rejected alternatives. Closes beadle-9t8.
- `Available()` now reports the specific Linux backends present
  on the host (e.g. `["pass", "secret-tool", "file (…)",
  "environment variable"]`) instead of a hard-coded `"libsecret"`
  label that never fired.

### Changed

- `secret.Available()` delegates to per-platform
  `keychainBackendNames()` so each OS file contributes its own
  list. Removes the `runtime.GOOS` switch in `secret.go`.

### Fixed

- `contacts.Store.Contacts()` now returns contacts sorted alphabetically
  by name (case-insensitive). Previously they were returned in insertion
  order, which made `list_contacts` MCP tool output and `beadle-email
  contact list` CLI output unpredictable as the address book grew. The
  on-disk JSON layout is unchanged — sorting only affects the slice
  returned to callers, so existing files do not need rewriting.

## [0.9.0] - 2026-04-01

### Added

- Background inbox poller in the MCP server. The server checks INBOX via
  IMAP STATUS on a configurable interval and fires `tools/list_changed`
  when new mail arrives — no CronCreate or model cooperation needed.
  Polling survives session restarts because the server reads
  `poll_interval` from `email.json` on startup.
- `set_poll_interval` MCP tool to configure polling (5m, 10m, 15m, 30m,
  1h, 2h, or n to disable). Persists to `email.json`.
- `get_poll_status` MCP tool to check poller state: interval, active,
  last check time, unseen count.
- `Client.Status()` lightweight IMAP STATUS method for unseen count.
- `email.SaveConfig()` for writing config changes back to disk.
- `read_message`: optional `max_body_length` parameter for lightweight
  preview. When set, truncates the body to the specified character count
  and appends a truncation indicator with the original length.

### Removed

- `poll-reminder.sh` UserPromptSubmit hook — replaced by server-side poller.
- SessionStart hook polling section — no longer needed.
- `.claude/beadle.local.md` and `.claude/beadle.poll.ts` — config moved to
  MCP server's own `email.json`.

## [0.8.0] - 2026-03-29

### Added

- Integration test layer with in-process IMAP and SMTP servers
  (`internal/testserver`). MCP smoke tests verify tool registration and
  identity error handling. Handler tests exercise the full stack via
  `HandleMessage` against memory-backed mail servers. IMAP/SMTP tests
  (build tag `integration`) cover Dial, ListFolders, ListMessages,
  FetchMessage, MoveMessage, SMTPSend, and SMTPAvailable.
- `email.Dialer` interface and `DefaultDialer` for test injection at the
  `withClient` seam. `RegisterTools` accepts `WithDialer` option.
- `make test-integration` target for running integration tests.
- `switch_identity` MCP tool for mid-session identity switching. Pass an
  ethos handle to operate as a different identity (e.g., switch from agent
  to human). Requires ethos identity files. Empty handle resets to default.
- `whoami` now shows override source when identity is switched, and lists
  session participants from the ethos session roster when available.
- `Resolver.ResolveHandle(handle)` for resolving arbitrary ethos handles.
- `internal/session` package for reading the ethos session roster (process
  tree walk + YAML sidecar).

### Removed

- Legacy `email.json` `from_address` fallback from identity resolution chain.
  Identity must now come from ethos (repo-local config or global active file) or
  the `default-identity` file. The per-identity `email.json` (connection config)
  is unchanged.

## [0.7.0] - 2026-03-21

### Added

- `beadle-email identity` subcommand: show resolved identity (email, handle,
  source, contacts path). `identity set <handle>` writes per-repo ethos config.
- `whoami` MCP tool: returns active identity so Claude sessions can
  self-diagnose permission errors caused by wrong identity resolution.

### Changed

- CLI framework: migrated from hand-rolled arg parsing to
  [cobra](https://cobra.dev/). All commands now support `--flag=value` syntax,
  auto-generated `--help` with per-flag documentation, and global flags
  (`--json`, `--verbose`, `--quiet`) in any position. Cobra is the Go CLI
  standard per punt-kit/standards/cli.md.
- Default behavior: running `beadle-email` without a subcommand now prints help
  instead of starting the MCP server. Use `beadle-email serve` explicitly.
- Contact commands now route through `g.printResult()` — `--json` and `--quiet`
  work for `contact list`, `contact add`, `contact remove`, and `contact find`.

### Fixed

- Repo-local ethos config reads `agent:` field (was `active:`). Ethos changed
  the contract in DES-011 because both a human and an agent are active in every
  Claude Code session. The old field name caused silent identity resolution
  failure — beadle fell through to fallback identity, breaking email permissions.
- `beadle-email contact add --name="Jim" --email="jim@x.com"` now works. The
  `--flag=value` syntax was rejected by the hand-rolled parser with "unexpected
  argument."
- Unknown flags now show cobra's auto-generated usage with all valid flags,
  instead of the vague "unexpected argument" error.
- Markdownlint config: added `.tmp/` to ignores so leaked pytest temp files
  don't break `make check`.

## [0.6.1] - 2026-03-21

### Fixed

- `/inbox` command: `rwx` (owner) messages now auto-reply when the message asks
  a question, using the same reply rules and safety constraints as `rw-`. Previously
  said "Never auto-reply," which was more restrictive than trusted contacts — the
  `x` bit is for command execution, not reply permission.
- Plugin hooks: moved `hooks/hooks.json` to `.claude-plugin/hooks/hooks.json`
  so Claude Code discovers PostToolUse output suppression. Previously the hook
  was not firing, causing raw MCP output to appear in the tool-result panel
  (truncated behind ctrl+o) instead of the two-channel display pattern.
- Plugin release: the release tooling now swaps `plugin.json`'s `"name"` field
  from `"beadle-dev"` (in the repo) to `"beadle"` in tagged builds, so
  marketplace installs show `/beadle:*` commands instead of `/beadle-dev:*`.
- README Quick Start: removed broken `claude plugin install` path that installed
  the plugin without the `beadle-email` binary, causing MCP server startup
  failures. `install.sh` is now the single recommended install method — it
  downloads the binary, verifies checksums, and installs the plugin.
- `install.sh`: bumped VERSION from 0.4.0 to 0.6.0 to match current release.
- README install SHA now uses `main` ref instead of pinned commit SHA that
  pointed to a stale version of `install.sh`.
- `make dist`: now generates `checksums.txt` alongside binaries. v0.5.0 and
  v0.6.0 were released without checksums, breaking `install.sh` at the SHA256
  verification step.

## [0.6.0] - 2026-03-20

### Added

- Table formatter (`internal/mcp/table.go`) matching biff's `format_table`
  pattern: `▶` header prefix, 3-space row prefix, aligned columns, 80-col
  budget, variable column with `…` truncation.
- `ExtractDisplayName` for clean FROM column display (name only, no email).
- `ListMessages` returns total count alongside messages. Panel summary
  shows "showing X of Y" (e.g., "showing 10 of 178 messages").
- Trust column uses single-char icons: `✓` trusted, `+` verified, `?`
  unverified, `✗` untrusted.
- Read status column (`R`) with `●` for unread.

### Changed

- All list tools (`list_messages`, `list_contacts`, `list_folders`,
  `show_mime`) use the table formatter with headers and aligned columns.
- `formatMessage` uses key-value block with 3-space prefix for consistency.
- SessionStart hook CronCreate instruction simplified (no unnecessary
  CronList/CronDelete since cron doesn't persist across sessions).

### Fixed

- `ExtractEmailAddress` now handles malformed RFC 5322 display names with
  unquoted brackets (e.g., `github-actions[bot] <notifications@github.com>`).
  Falls back to angle-bracket extraction when `net/mail.ParseAddress` fails.
- Rewrote `suppress-output.sh` PostToolUse hook to follow punt-kit two-channel
  display standard. Fixes: removed `set -euo pipefail` (hooks must degrade
  gracefully), switched `echo` to `printf '%s'`, added kill-switch, improved
  summaries (count-based instead of first-line for list tools).

## [0.5.0] - 2026-03-20

### Added

- `r` permission enforcement on `list_messages`, `read_message`, and
  `download_attachment`. Messages from senders without read permission show
  redacted subjects in listings, and return "permission denied" when read
  or when attachments are downloaded.
- `w` permission enforcement on `send_email`. All recipients (to, cc, bcc)
  must have write permission; unknown contacts are denied.
- Multi-identity support via ethos sidecar (DES-013). Beadle reads the active
  identity from ethos (`~/.punt-labs/ethos/identities/<handle>.yaml`) with
  fallback to `default-identity` file and legacy `email.json`. Identity is
  resolved per MCP tool call — no restart needed to switch identities.
- Identity-scoped directories (`~/.punt-labs/beadle/identities/<email>/`) for
  config, contacts, and attachments. Auto-migrates root files on first use.
- Repo-local ethos config (`.punt-labs/ethos.yaml`) pins beadle's
  identity to `claude` regardless of global active identity.
- Per-identity rwx contact permissions (DES-012). Each contact can have
  different permissions per identity: `r` (read/surface), `w` (reply),
  `x` (execute instructions). All permissions are stored explicitly.
  Default for contacts without explicit permissions: `---`.
- `add_contact` accepts optional `permissions` parameter (e.g., "rwx", "rw-").
- `list_contacts` and `find_contact` show effective permissions for active identity.
- `check_trust` includes identity permission alongside transport trust level.
- `beadle-email status` shows active identity, source, and handle.
- `beadle-email doctor` checks ethos availability.

### Changed

- Default permission for unknown contacts changed from `r--` to `---`.
  The address book is now a whitelist — only known contacts with explicit
  read permission have their messages surfaced.

### Fixed

- Removed incorrect owner override from `CheckPermission` — all permissions
  are now stored explicitly per DES-012. The owner identity no longer gets
  implicit `rwx`; permissions must be set in the contact's permissions map.
- Removed permission gate from `remove_contact` — rwx permissions govern
  inbound mail processing behavior, not address book CRUD operations.

## [0.4.0] - 2026-03-18

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
