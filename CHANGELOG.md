# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

## [0.13.0] - 2026-04-12

### Added

- Channels support: beadle-email declares `experimental['claude/channel']`
  capability. When the poller detects new mail, it fires
  `notifications/claude/channel` to push a prompt directly into Claude
  Code's conversation queue. Sessions with channels enabled process inbox
  autonomously without CronCreate. Requires `--channels plugin:beadle@punt-labs`
  on Claude Code launch. (beadle-9rb)

### Changed

- Upgraded mcp-go from v0.45.0 to v0.46.0 (adds `WithExperimental` for
  capability declaration).

## [0.12.0] - 2026-04-12

### Fixed

- `SendResult.Method` now reports `"smtp"` instead of hardcoded
  `"proton-bridge-smtp"` for all SMTP sends. Reflects that the SMTP host
  can be Fastmail, Gmail, or any other server since `smtp_host` was added
  in v0.11.3. (beadle-j25)
- `intParam` uses `math.Trunc(n) != n` for the fractional check instead of
  `n != float64(int(n))`, which is implementation-defined outside the int
  range per the Go spec. Adds explicit int32 bounds guard before
  conversion. (beadle-1tk)
- `read_message` now extracts text from nested multipart structures
  (e.g., `multipart/signed` wrapping `multipart/alternative`). Previously
  returned "(no text body)" for any message where text/plain was deeper
  than one MIME level. Fixes unreadable GPG Mail replies. (beadle-qoa)

### Added

- WebSocket transport: `beadle-email serve --transport ws --port 8420` starts
  a WebSocket server for Docker and mcp-proxy deployments. Each connection
  gets its own MCP session. 16 MB message size limit. (beadle-o1w)
- Health subcommand: `beadle-email health --port 8420` for Docker HEALTHCHECK.
  HTTP GET to /health, exits 0 on 200, 1 otherwise. (beadle-o1w)
- `tls_skip_verify` config field: when true, skips TLS certificate verification
  for IMAP and SMTP connections. Required for Proton Bridge via
  host.docker.internal in Docker deployments. (beadle-o1w)
- Docker image: `ghcr.io/punt-labs/beadle-email` based on debian:bookworm-slim
  with gnupg. GPG keyring copied from read-only mount to tmpfs at startup
  (private keys exist only in memory). Makefile `docker` and `docker-push`
  targets. (beadle-o1w)
- Outbound PGP encryption: when all recipients have a `gpg_key_id` in the
  contact book, `send_email` encrypts the message to their keys (RFC 3156
  `multipart/encrypted`). Messages are signed first, then encrypted
  (sign-then-encrypt). Falls back to signed-only when any recipient lacks
  a GPG key. (beadle-oep)
- Inbound PGP decryption: `read_message` automatically decrypts messages
  encrypted to the agent's GPG key (`multipart/encrypted`, RFC 3156).
  Decrypted content is parsed recursively for nested MIME (text, attachments,
  inner signatures). Encrypted+signed messages go through the normal
  verification path after decryption. (beadle-ksk)
- Implicit TLS support for IMAP (port 993) and SMTP (port 465). Standard
  email providers (Fastmail, Gmail, Migadu) use implicit TLS by default.
  Port-based auto-detection: 993 → IMAPS, 465 → SMTPS, others → STARTTLS.
  SMTP STARTTLS now sets ServerName for proper certificate verification
  with non-loopback hosts. (beadle-zle)
- Outbound PGP signing: when `gpg_signer` is configured in `email.json`,
  all outbound email via SMTP is signed as PGP/MIME (`multipart/signed`,
  RFC 3156). The signing key must have an expiration date (design invariant).
  Resend API fallback is blocked when signing is configured -- PGP signatures
  require raw MIME transport. (beadle-atz)

## [0.11.3] - 2026-04-11

### Changed

- README: updated prerequisites to reflect non-Bridge SMTP support, added
  external SMTP config example, documented CLI global flag support scope,
  added plugin vs standalone distinction in Quick Start, added Go and
  Go Report Card badges. (beadle-95l)

### Added

- `smtp_host` and `smtp_user` config fields in `email.json`. When omitted,
  both default to the corresponding IMAP values, preserving backward
  compatibility with existing configs. Allows IMAP and SMTP to be served
  by different hosts (required for standard IMAP/SMTP deployments without
  Proton Bridge). beadle-0ut.
- `SMTPPassword()` credential method. Resolves the `smtp-password` secret
  first; falls back to `IMAPPassword()` when absent, so single-password
  setups require no config change. beadle-0ut.
- GPG key expiry enforcement: `CheckKeyExpiry` validates that a signing key
  has a non-zero expiration date before any sign operation proceeds. Keys
  without an expiry date are rejected with an error. beadle-72e.

### Fixed

- `/inbox <interval>` (`5m`/`10m`/`15m`/`30m`/`1h`/`2h`) now creates a durable
  `CronCreate` job that survives session restarts, and cleanup reliably finds
  existing auto-poll jobs by prompt match instead of a nonexistent `description`
  field. Fixes a regression from #123 where the autonomous inbox loop silently
  failed to take effect. (beadle-07h)
- `smtp.go` was using `cfg.IMAPHost` and `cfg.IMAPUser` for all SMTP
  connections. Now uses `cfg.SMTPHost`, `cfg.SMTPUser`, and
  `cfg.SMTPPassword()`. beadle-0ut.
- `secret.Get` previously swallowed credential file permission errors
  (e.g., wrong mode on `~/.punt-labs/beadle/secrets/smtp-password`) by
  silently falling through to the next backend. Permission errors are now
  returned directly with a diagnostic message. A new `secret.ErrNotFound`
  sentinel distinguishes "credential absent" from "credential inaccessible".
  beadle-0ut.

## [0.11.2] - 2026-04-10

### Fixed

- `/inbox <interval>` now creates a CronCreate loop job in addition to setting
  the MCP server poll interval. Previously, the MCP poller ran but only sent
  `tools/list_changed` notifications — nothing autonomously invoked `/inbox`,
  so mail was never processed unless the user was interactive. beadle-by4.
- `/inbox n` now deletes the CronCreate loop job when polling is disabled.
- Session-start: `/inbox` with no argument now checks `get_poll_status` and
  recreates the CronCreate job if polling is active but no loop job exists,
  restoring autonomous operation after a Claude Code restart.

## [0.11.1] - 2026-04-10

### Fixed

- `/inbox` now emits the `list_messages` table before processing unread
  messages. Previously the table was only shown in the no-unread fallback,
  making `/inbox` appear to silently skip the listing.
- `install.sh` no longer silently swallows plugin uninstall failures. On a
  fresh machine (plugin not installed) uninstall is skipped entirely. On an
  upgrade, if `claude plugin uninstall` exits non-zero the script now prints an
  actionable error to stderr and exits 1 rather than continuing and leaving the
  user on the old version. beadle-2nk.
- `intParam` now returns an error when the argument is present but has the
  wrong type (e.g. string instead of number). Previously it silently returned
  the fallback value. Affects `count`, `max_body_length`, and `part_index`.
  beadle-1l3.
- `intParam` rejects fractional `float64` values (e.g. `count: 10.5`) instead
  of silently truncating to `10`.
- `max_body_length` validation in `readMessage` moved before `withClient` so
  type and range errors fail fast without an IMAP round-trip.

### Changed

- `/inbox` skill removed hardcoded personal names from examples; all behavior
  is permission-driven via `find_contact`/`check_trust`.
- `/contacts add` flow now prompts for permissions (was silently omitted).
  Hint text updated to `[list | add <name> <email> [permissions] | ...]`.
- README: MCP tool count corrected from 15 to 17; added `whoami`,
  `set_poll_interval`, `get_poll_status` to the tools table.
- `.biff` team members now includes `claude-puntlabs` for inter-agent
  routing.

### Added

- `TestFormatMessages_EmptyFrom` — pins the empty-From rendering path with
  an 80-rune width assertion. beadle-2x8.

## [0.11.0] - 2026-04-09

### Added

- Contact matching supports glob patterns in the `email` field. A
  single entry like `*@mail.anthropic.com` with `r--` covers rotating
  sender addresses (e.g. `no-reply-abc123@mail.anthropic.com`) that
  previously missed exact-match lookup and fell through to the
  default `---` redaction. Pattern contacts may not grant write or
  execute — allowed permissions are `r--` (read-only) or `---`
  (blocked, for negative grants); full `rwx` grants require an exact
  address. Exact contacts always take precedence over patterns; among
  matching patterns, the longest pattern wins. Uses `path.Match`
  syntax (`*`, `?`, `[set]`). See `DESIGN.md` § DES-019 for the full
  precedence rules and rationale. beadle-a7v.

## [0.10.2] - 2026-04-08

### Changed

- `list_messages` output has been redesigned to fit the 80-column row
  budget while surfacing the full sender email address on every row.
  The new 5-column layout is a 3-character right-aligned ID row
  prefix, a single `FROM` column carrying `Name <email>` in RFC 5322
  form (width 37), a 6-character `DATE` column showing `Apr 08` (no
  time, no year), a 1-character trust glyph `T`, and a variable
  `SUBJECT` column (22 characters in the typical case). Display
  names are truncated when necessary so the email address stays
  visible in full — permission enforcement keys on the raw address,
  and the operator must be able to read it. Bot names on 24-character
  relay domains (e.g. `cursor[bot] <notifications@github.com>`)
  truncate to `cursor[bo… <notifications@github.com>` so the email
  stays intact. Every rendered row is exactly 80 runes wide, enforced
  by a row-width assertion helper in the test suite.

  Two earlier iterations on this branch are superseded by this
  layout and did not ship to users: a standalone `EMAIL` column
  between `FROM` and `DATE`, and a `(via <domain>)` annotation on
  the `FROM` cell for notification relays. Both are unnecessary now
  that the full email address is visible inside `FROM` — a row like
  `Pat Singh <notifications@github.com>` shows the actual sender
  domain without an annotation. See `DESIGN.md` § DES-018 for the
  byte-accurate mockup, slot widths, FROM column rules, and the
  complete test requirements. beadle-z34, beadle-0he.

### Fixed

- `beadle-email doctor` no longer flags a missing `gpg-passphrase`
  credential as a failure when the configured signing key has no
  passphrase. Doctor now probes the key with
  `gpg --batch --pinentry-mode=error --passphrase '' --dry-run --sign`
  after the `gpg_signing_key` check passes. Three-way classification:
  - Key needs passphrase, credential stored → `[+] gpg_passphrase`
  - Key needs passphrase, credential missing → `[!] gpg_passphrase
    credential "gpg-passphrase" not found`
  - Key has no passphrase → `[+] gpg_passphrase not required (<signer>
    has no passphrase — filesystem access grants signing authority)`.
    The detail text surfaces the posture concern so the unprotected
    state is visible, not silenced, but it does not fail doctor.
  New helper: `pgp.KeyRequiresPassphrase(gpgBinary, signer)`. beadle-cbo.

## [0.10.1] - 2026-04-08

### Fixed

- `.claude-plugin/hooks/hooks.json` SessionStart entry declared
  `"matcher": ""` (empty string), which Claude Code rejects as an
  invalid matcher. Result: beadle's SessionStart hook has never fired
  across any install — no top-level commands deployed to
  `~/.claude/commands/`, no permission rules written to
  `~/.claude/settings.json`, no session-start `additionalContext`
  emitted. Every other Punt Labs plugin (biff, quarry, ethos,
  dungeon, vox, lux) either omits the `matcher` field or uses a
  specific value (`startup`, `resume|compact`). beadle was the only
  outlier. Fix: remove the `"matcher": ""` line. The
  `session-start.sh` script itself is correct — verified by running
  it manually with `CLAUDE_PLUGIN_ROOT` set, after which it deploys
  all 4 commands and writes all 6 permission rules on first try.
  beadle-1aj.

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
- `beadle-email contact add --name="Sam" --email="sam@x.com"` now works. The
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
  name resolution works in CLI send (`--to sam` resolves to stored email).
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
  `send_email` resolves names inline — `/mail sam` works in a single
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
