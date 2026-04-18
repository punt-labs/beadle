# Design Decisions

Log of architectural decisions, alternatives considered, and outcomes.
Consult before proposing changes to settled architecture.

## DES-001: Trust model — four levels, not binary

**Decision:** Trust classification uses four discrete levels (trusted, verified,
untrusted, unverified) rather than a binary trusted/untrusted split.

**Why:** Proton-to-Proton messages have infrastructure-level E2E encryption
that is qualitatively different from PGP-signed external messages. Collapsing
them into a single "trusted" category loses information. The four-level model
lets consumers make graduated decisions.

**Detection:**

| Level | Method |
|-------|--------|
| trusted | `X-Pm-Content-Encryption: end-to-end` + `X-Pm-Origin: internal` |
| verified | `gpg --verify` exit 0 |
| untrusted | `gpp --verify` exit non-zero |
| unverified | No `multipart/signed` in Content-Type |

## DES-002: Inline PGP verification in list_messages

**Decision:** `list_messages` runs `gpg --verify` on every message with a PGP
signature during listing, not as a separate step.

**Alternative:** Return `unverified` for signed messages in listings and require
a separate `verify_signature` call. This was the original design and led to user
confusion — the listing showed `unverified` for a message that was actually
verifiable.

**Trade-off:** Adds ~1-2s per signed message to listing time. Acceptable because
most messages are unsigned, and trust accuracy matters more than latency.

## DES-003: Isolated GNUPGHOME per PGP operation

**Decision:** Every `Verify()` and `Sign()` call creates a temporary GNUPGHOME
under `/tmp` (short path for Unix socket limit), imports needed keys, runs gpg,
and deletes the temp dir.

**Why:** Prevents keyring pollution. A malicious attached key can't persist in
the user's keyring. Each operation is hermetic.

**Socket path:** GPG agent communicates via Unix domain socket with a 108-byte
path limit. macOS `os.MkdirTemp("")` yields `/var/folders/...` paths that exceed
this. Using `/tmp/bg-*` keeps it short.

## DES-004: System keyring bridge for verification

**Decision:** When a message has a PGP signature but no attached public key,
`exportAll()` copies all public keys from `~/.gnupg/` into the isolated
GNUPGHOME before running `gpg --verify`.

**Why:** Many senders sign messages without attaching their public key (e.g.,
GPG Mail on macOS). Without bridging, these messages always fail verification
with "No public key." The bridge is read-only — it exports from the system
keyring but never writes to it.

## DES-005: Proton Bridge strips multipart/signed

**Decision:** Do not PGP-sign outbound messages through Proton Bridge SMTP.

**Evidence:** Tested 2026-03-13. Sent a `multipart/signed` RFC 3156 message
through Proton Bridge SMTP. The message arrived with the signature as a detached
`signature.asc` attachment — the `multipart/signed` envelope was stripped. GPG
Mail did not auto-verify because the MIME structure was wrong.

**Root cause:** Proton Bridge re-processes all outbound messages through its own
encryption pipeline, disassembling and reassembling MIME structure.

**Workaround:** Fastmail SMTP (port 465, implicit TLS) preserves raw MIME
including `multipart/signed` envelopes. Verified 2026-04-11: GPG Mail on
macOS shows green "Signed" checkmark on messages sent via Fastmail SMTP.
Amazon SES `SendRawEmail` was the original workaround candidate but was
never tested. See DES-022 for the shipping outbound signing architecture.

## DES-006: Resend does not support raw MIME

**Decision:** Resend API cannot be used for PGP-signed outbound mail.

**Evidence:** Tested 2026-03-13. Resend's POST /emails endpoint only accepts
structured fields (from, to, subject, text, html). There is no `raw` field.
The API docs confirm no raw MIME support. Additionally tested 2026-04-11:
Resend uses Amazon SES as its backend, which also strips `multipart/signed`
envelopes even when raw MIME is provided through SES's `SendRawEmail` API.

**Impact:** Resend is fallback-only for unsigned plain text delivery. When
`gpg_signer` is configured, `TrySendChain` blocks the Resend fallback
entirely — see DES-022.

## DES-007: Sender chain — SMTP primary, Resend fallback

**Decision:** Outbound email tries Proton Bridge SMTP first, falls back to
Resend API.

**Why:** Proton Bridge passes SPF/DKIM/DMARC for punt-labs.com. Resend has no
DKIM records configured for punt-labs.com (DMARC policy: quarantine). Messages
sent through Resend may land in spam.

**Future:** SES will become primary for external recipients (with PGP signing).
Proton Bridge for Proton-to-Proton. Resend becomes last resort.

## DES-008: Credential resolution chain

**Decision:** Secrets resolved at runtime through: OS keychain → file → env var.
Config files store only connection parameters.

**Why:** Prevents accidental secret commits. The config file
(`~/.punt-labs/beadle/email.json`) can be shared or version-controlled safely.
Secrets live in the OS keychain (macOS Keychain, Linux libsecret in v0.1.1)
or mode-600 files.

**Path traversal:** `secret.Get()` rejects names containing `/` or `\`.
`fileGet()` rejects files with group/world-readable permissions.

## DES-009: Install via curl | sh with SHA256 verification

**Decision:** Distribution via GitHub Releases with a curled install script,
matching the mcp-proxy standard.

**Structure:** 4 static binaries (darwin/arm64, darwin/amd64, linux/arm64,
linux/amd64) + checksums.txt. Install script downloads, verifies SHA256,
installs to `~/.local/bin`, registers marketplace, and registers the MCP
server via `claude mcp add -s user`.

**No manual steps after install.** The installer completes all registration.
User just restarts Claude Code.

## DES-010: Command naming — /mail, /inbox, /send

**Decision:** Three top-level slash commands for beadle-email, designed as verbs:

| Command | Meaning | Scope |
|---------|---------|-------|
| `/mail` | "Mail this to me/someone" | Email-specific outbound. Always means email. |
| `/inbox` | "Process your inbox" | Beadle checks its own inbox for owner instructions. |
| `/send` | "Send via any channel" | Multi-channel outbound. Email today, Signal later. |

**Examples:**

- `/mail me a summary` — email a summary to the owner
- `/mail this to kai@example.com` — email to a specific recipient
- `/inbox` — beadle checks its inbox for new messages
- `/inbox check for anything from me` — filtered inbox check
- `/send me an email` — same as `/mail` today
- `/send me a link on Signal` — future: routes to Signal channel

**Why three verbs, not one:** `/mail` and `/send` overlap today (both send email)
but diverge when new channels arrive. `/send` routes by channel; `/mail` always
means email. `/inbox` is the inbound verb — beadle processing its own inbox, not
the user checking their email. The beadle checks for orders from the authority.

**Namespace conflicts avoided:** `/read` (biff), `/write` (biff), `/recap` (vox),
`/check` (z-spec) are all taken. `/mail`, `/inbox`, `/send` are clean.

**Future:** `/read` may evolve from biff-only to multi-channel inbox (biff +
email + Signal), but that's a cross-plugin design decision for later.

**Plugin namespace:** `beadle:mail`, `beadle:inbox`, `beadle:send` as
plugin-scoped commands. `/mail`, `/inbox`, `/send` deployed to
`~/.claude/commands/` by the SessionStart hook.

## DES-011: Dual install — Claude Plugin or MCP-only

**Decision:** Two mutually exclusive installation paths:

| Path | What you get | How | Use case |
|------|-------------|-----|----------|
| **Claude Plugin** | MCP server + hooks + commands + output suppression | `claude plugin install punt-labs/beadle` | Claude Code users |
| **MCP-only** | MCP server only | `install.sh` → `claude mcp add` (or manual) | GitHub Copilot, other MCP clients |

**Why mutually exclusive:** `plugin.json` registers the MCP server via `mcpServers`.
The installer registers it via `claude mcp add`. Running both creates a
double-registration. The installer should detect plugin installation and skip MCP
registration (or vice versa).

**Scope:** This pattern applies to all Punt Labs projects that ship both a plugin
and a standalone MCP server. The plugin path is the full experience; the MCP-only
path is for non-Claude-Code clients that speak MCP but have no plugin system.

**MCP-only path:** The binary is the complete product (CLI standards: "the CLI is
the complete product"). Any MCP client that can spawn `beadle-email serve` gets
the full MCP tool set. What they miss: output suppression (two-channel display),
slash commands, and lifecycle hooks. These are Claude Code affordances, not
capabilities.

## DES-012: Two-party identity trust model (rwx)

**Decision:** Beadle's address book implements a two-party permission system
orthogonal to transport trust. Permissions are stored per (identity, contact)
pair using the Unix rwx model.

**Two entities:**

- **Identity** — who beadle is operating as. Owned by ethos, not beadle
  (see DES-013). Beadle reads `email`, `name`, `handle` from the ethos
  identity YAML. Today: `claude@punt-labs.com`. Future:
  `sam@example.com`, `builds@punt-labs.com`, etc.

- **Contact** — who beadle is interacting with. Stored in the address book with
  name, email, aliases, GPG key ID, notes, and a permissions map.

**Permission matrix:**

```text
permissions[identity_email][contact] → "rwx" | "rw-" | "r--" | "---"
```

| Permission | Meaning |
|------------|---------|
| `r` (read) | Beadle reads and surfaces the message to the owner. No autonomous action. |
| `w` (write) | Beadle may compose and send replies to this contact. |
| `x` (execute) | Beadle may execute instructions/commands from this contact. |

Example for identity `claude@punt-labs.com`:

| Contact | Permissions | Effect |
|---------|------------|--------|
| Sam Jackson | `rwx` | Full authority — read, reply, execute tasks |
| Eric | `rw-` | Read and reply, but not execute instructions |
| Vendor X | `r--` | Read only, surface to owner for action |
| Unknown sender | `---` | Default: no permissions (whitelist) |

**Orthogonal to transport trust:** Transport trust (trusted/verified/untrusted/
unverified from DES-001) answers "is this message authentic?" Identity trust
answers "given it's authentic, what should beadle do?" Both must pass: an
unverified message from an `rwx` contact should NOT be executed (identity claim
not verified). An authenticated message from an `r--` contact should NOT trigger
autonomous action (sender lacks authority).

**No inheritance between identities.** Sam may grant Eric `rwx` on
`sam@example.com` but only `rw-` on `claude@punt-labs.com`. Each cell in the
matrix is explicit. No implicit propagation.

**Default permissions:** Contacts without explicit permissions for an identity
default to `---` (no permissions). The address book is a whitelist — only known
contacts with explicit `r` get their messages surfaced. All permissions are
stored explicitly. There are no implicit overrides.

**Enforcement:** `r` is enforced on `list_messages` (redacted subject for
senders without read), `read_message` (permission denied), and
`download_attachment` (permission denied). `w` is enforced on `send_email`
(all recipients must have write permission). `x` (execute) is not yet
enforced — it requires instruction parsing infrastructure.

**Exempt tools:** `check_trust`, `verify_signature`, and `show_mime` are
diagnostic — they return metadata (trust classification, signature validity,
MIME structure) without exposing message body content. They are intentionally
ungated. `move_message` is identity-local inbox management (archiving,
organizing), not a sender-directed action — no sender permission required.

**Redacted listings:** `list_messages` shows sender, date, and trust level
for all messages but redacts the subject for senders without `r`. This lets
the owner discover unknown senders and decide whether to add them to contacts.

**Data model:**

```text
Identity {
    name          string   // "Claude"
    email         string   // "claude@punt-labs.com"
    gpg_key_id    string   // signing key for this identity
    config_path   string   // path to this identity's email.json
}

Contact {
    name          string
    email         string
    aliases       []string
    gpg_key_id    string
    notes         string
    permissions   map[string]string  // identity_email → "rwx"
}
```

**Processing inbound mail:**

1. Which identity's mailbox am I reading? → "who am I"
2. Who sent this message? → look up contact by sender address
3. What permissions does this contact have for this identity? → gate behavior
4. Combined with transport trust: only act if both identity trust AND transport
   trust are sufficient

**Status:** Implemented. Identity resolved via ethos sidecar (DES-013).
Contact `Permissions` field stores `map[identity_email]string` with rwx
values. `CheckPermission()` looks up stored permissions and, when none are
set, defaults to `---` (no access; whitelist model). No implicit overrides.
MCP tools expose effective permissions in `list_contacts`, `find_contact`,
and `check_trust` responses.

**Scope of rwx permissions:** The rwx model governs inbound mail processing
behavior — how beadle handles messages from a given sender when operating as
a given identity. It does NOT govern address book CRUD. Any identity can add
or remove contacts regardless of permissions. The permissions answer "what
should beadle do with mail from this person?", not "who may edit the address
book?"

## DES-013: Identity via ethos sidecar with namespaced extensions

**Decision:** Beadle does not own identity. Ethos does. Beadle reads identity
from the ethos sidecar and stores beadle-specific data in ethos's namespaced
extension mechanism (ethos DES-008).

**Core identity** (owned by ethos, read-only for beadle):

- File: `~/.punt-labs/ethos/identities/<handle>.yaml`
- Fields beadle reads: `email` (which mailbox), `name` (display), `handle` (key)

**Beadle extension** (owned by beadle, ethos preserves but never interprets):

- File: `~/.punt-labs/ethos/identities/<handle>.ext/beadle.yaml`
- Fields: `gpg_key_id`, contact permissions, any beadle-specific state
- CLI access: `ethos ext set <handle> beadle gpg_key_id <value>`
- Merged view: `ethos show <handle>` includes `ext.beadle`

**Identity resolution at session start:**

1. SessionStart hook calls `ethos whoami` (CLI, ~10ms in Go)
2. Gets active handle → reads `email` field from identity YAML
3. Loads beadle identity directory keyed by email address
4. If ethos not installed → fall back to beadle default identity

Fall back uses the same directory structure as ethos-sourced identities. No
split between "ethos mode" and "legacy mode." One code path.

**Default identity:**

Beadle always has a default identity. File `~/.punt-labs/beadle/default-identity`
contains the default email address. When ethos is absent or has no active
identity, beadle uses this default. Same per-identity directory structure either
way.

**Identity-scoped directory structure:**

```text
~/.punt-labs/beadle/
  identities/
    claude@punt-labs.com/
      email.json          # IMAP/SMTP config for this identity
      contacts.json       # contacts + permissions for this identity
      attachments/        # downloaded attachments
    sam@example.com/
      email.json
      contacts.json
      attachments/
  default-identity        # file: default email address
```

**Filesystem layout (both systems):**

```text
~/.punt-labs/
├── ethos/                              ← ethos owns this tree
│   ├── active                          ← global active handle ("sam")
│   ├── sessions/                       ← session roster data
│   └── identities/
│       ├── sam.yaml                    ← Sam's persona (kind: human)
│       ├── sam.ext/                    ← extensions for Sam
│       ├── claude.yaml                 ← Claude's persona (kind: agent)
│       └── claude.ext/
│           └── beadle.yaml             ← beadle extension (gpg_key_id)
│
└── beadle/                             ← beadle owns this tree
    ├── default-identity                ← fallback email when ethos absent
    └── identities/
        └── claude@punt-labs.com/       ← scoped by email (pivot key from ethos)
            ├── email.json              ← IMAP/SMTP connection config
            ├── contacts.json           ← address book + rwx permissions
            └── attachments/            ← downloaded files
```

Plus repo-local identity pin:

```text
<repo>/.punt-labs/ethos.yaml              ← "agent: claude" (overrides global)
```

**Ownership boundaries:**

| Concern | Owner | File |
|---------|-------|------|
| Who am I? (name, email, handle, kind) | ethos | `identities/<handle>.yaml` |
| What's my GPG key? | beadle (via ethos extension) | `identities/<handle>.ext/beadle.yaml` |
| Which identity for this repo? | ethos (repo-local) | `<repo>/.punt-labs/ethos.yaml` |
| How do I connect to mail? | beadle | `beadle/identities/<email>/email.json` |
| Who do I know + permissions? | beadle | `beadle/identities/<email>/contacts.json` |

**The email address is the pivot key.** Ethos provides `email` in the identity
YAML. Beadle uses that email to locate its own scoped directory under
`~/.punt-labs/beadle/identities/<email>/`. Ethos does not know that directory
exists. Beadle does not write to ethos directories (except its own extension).

**Why repo-local config exists:** The global `active` file may say `sam`
(Sam is the active human). But in the beadle repo, Claude is the agent that
operates. The repo-local `config.yaml` pins `agent: claude` so beadle
operates as Claude in this repo regardless of the global identity. This is
how one machine supports a human identity globally and an agent identity
per-repo.

**How beadle accesses ethos data:**

| Path | Method | Why |
|------|--------|-----|
| Hook → identity resolution | CLI (`ethos whoami`) | Hook is shell, fast |
| MCP tool → read identity | Read file directly | No subprocess needed |
| MCP tool → read extensions | Read `.ext/beadle.yaml` | Same |

The LLM never reads ethos files directly. Beadle's MCP tools read them
internally and expose relevant data through beadle's own tool responses.

**Ethos is a peer identity system.** The same schema describes humans and
agents — only a `kind` field distinguishes them. There is no "owner" concept
in ethos. Ownership and authority relationships (e.g., "Sam owns Claude") are
application-level policy that belongs in the consuming application (beadle),
not in the identity layer (ethos). Beadle previously encoded `owner_email` in
the ethos extension; this was removed in PR #46 because it violated this
boundary.

**Identity switching:**

`ethos iam <handle>` writes the active identity. On next SessionStart or MCP
tool invocation, beadle reads `ethos whoami`, gets the email, loads the
corresponding identity directory.

**User identification:**

The user is identified at session start. Priority chain:

1. `ethos iam <handle>` was called previously → active identity exists
2. Logged-in OS username → map to ethos handle
3. Beadle default identity

**Rejected alternatives:**

- Beadle builds its own Identity struct with its own config files — duplicates
  ethos, creates a parallel identity system.
- Beadle adds a "beadle binding" block to the ethos schema — tight coupling,
  violates ethos DES-001 (sidecar, no import dependency).
- Beadle writes freeform fields into the identity YAML — conflicts with ethos
  schema ownership. The extension mechanism exists for exactly this purpose.
- Two directory structures (ethos mode vs legacy) — unnecessary complexity.
  One structure with a default identity fallback.

**Depends on:** ethos DES-008 (generic extension mechanism).

## DES-014: Cobra for CLI framework

**Decision:** Migrate from hand-rolled arg parsing to
[cobra](https://cobra.dev/) for all CLI commands.

**Why:** The hand-rolled parser caused a user-facing bug: `--flag=value` syntax
was rejected because `switch args[i]` matched exact strings only. The parser also
lacked per-flag help generation, global flag propagation to subcommands, and
actionable error messages. These are solved problems in cobra.

**Status:** Implemented. All 14 subcommands migrated. punt-kit/standards/cli.md
updated with Go/cobra guidance alongside Python/typer.

**Rejected alternatives:**

- **Keep hand-rolled parser, add `parseFlag` helper** — Fixes the immediate bug
  but leaves the structural problem. Every new flag or subcommand requires manual
  parsing code, help text maintenance, and global flag forwarding. The bug
  recurrence risk is high.
- **stdlib `flag` package** — Handles `--flag=value` natively but has no
  subcommand support. Would require hand-rolled dispatch for `contact list`,
  `contact add`, etc.
- **urfave/cli** — Lighter than cobra but less ecosystem adoption. Cobra is used
  by kubectl, docker, gh, and is the de facto Go CLI standard.

## DES-015: Two-layer inbox polling — detection + processing

**Status:** SETTLED (PR #84, 2026-04-01; revised 2026-04-12)

**Decision:** Autonomous inbox processing requires two cooperating layers:
a server-side poller for **detection** and a durable CronCreate job for
**processing**. Neither layer is sufficient alone.

**Problem:** The original design relied on the model to re-register a
CronCreate job at session start (via SessionStart hooks or CLAUDE.md
instructions). Tested 2026-04-01: the model ignores these instructions.
Session-scoped CronCreate jobs die when the session ends. There is no
reliable mechanism to make the model call a tool at session start.

The server-side poller (background goroutine) solved the detection problem
but not the processing problem. `tools/list_changed` is a metadata signal —
it tells Claude Code that tool descriptions changed. It does NOT trigger
the model to invoke `list_messages`, read messages, or process the inbox.
The gap between "Claude Code knows tools changed" and "Claude Code acts"
has no MCP spec mechanism to bridge.

**Design — two layers:**

| Layer | Owner | Mechanism | Purpose |
|-------|-------|-----------|---------|
| Detection | MCP server | Background goroutine, IMAP STATUS | Detect new mail, fire `tools/list_changed` |
| Processing | Claude Code | Durable CronCreate `/inbox` | Read, classify, archive, reply, flag |

**Detection layer (MCP server, autonomous):**

1. `email.json` stores `poll_interval` (valid: 1m, 5m, 10m, 15m, 30m, 1h, 2h, n)
2. On startup, the server reads the config and starts a background goroutine
3. The goroutine calls IMAP STATUS on the configured interval
4. When unread count increases, it fires `tools/list_changed` (MCP notification)
5. MCP tools `set_poll_interval` and `get_poll_status` manage the config
6. Survives session restarts — config persisted to disk, goroutine starts on boot

**Processing layer (Claude Code, durable CronCreate):**

1. `/inbox <interval>` creates a durable CronCreate job (`durable: true`)
2. The job fires `/inbox` on the configured interval
3. `/inbox` calls `list_messages`, classifies by permission, reads, archives,
   replies, and flags messages for the owner
4. Durable jobs persist to `.claude/scheduled_tasks.json` and survive restarts
5. `/inbox n` disables both layers (calls `set_poll_interval` + deletes the job)

**Why both layers:**

- Detection without processing: server detects new mail, fires
  `tools/list_changed`, but Claude Code never reads the messages. The inbox
  fills up. This is what happens without the CronCreate job.
- Processing without detection: CronCreate `/inbox` runs on a timer and
  processes the inbox. This works but is redundant with the server-side
  poller — both check on the same interval. The server-side poller adds
  value because it can fire `tools/list_changed` between CronCreate ticks
  (e.g., when a high-priority message arrives and the user is interacting).
- Both together: server detects and signals; CronCreate processes. The server
  catches arrivals between cron ticks; the cron ensures processing happens
  even when `tools/list_changed` doesn't trigger model action.

**Configuration — `/inbox <interval>` sets both layers:**

1. Calls `set_poll_interval` MCP tool → saves to `email.json`, starts goroutine
2. Creates durable CronCreate job → persists to `scheduled_tasks.json`
3. Both use the same interval. `/inbox n` disables both.

**Key properties of the detection layer:**

- No model cooperation needed — server pushes notifications autonomously
- First poll runs immediately, subsequent polls on the configured interval
- First poll suppresses notification (avoids false positive on existing unread)
- Failure tracking: consecutive failure count and last error in status output
- Atomic config writes (temp + rename) prevent corruption
- Config fallback: identity-scoped path, then default path, only on `ErrNotExist`

**Pattern:** Same as biff's notification system (see `punt-labs/biff` repo,
`docs/notification.tex`). Background poller detects changes, fires
`tools/list_changed` from the server's own goroutine context.

**Rejected alternatives:**

- **SessionStart hook with "Execute:" instruction** — Model ignores
  `additionalContext` instructions. Tested and verified broken.
- **CLAUDE.md instruction** — Model doesn't reliably act on CLAUDE.md
  instructions at session start. Biff's `/loop` instruction fails regularly.
- **SessionStart hook with `type: "prompt"`** — SessionStart hooks only support
  `type: "command"`. No prompt injection at session start.
- **Server-side poller alone** — Detection works but processing does not.
  `tools/list_changed` is a metadata signal, not a prompt. Claude Code
  re-lists tools but does not autonomously invoke `/inbox`. Tested
  2026-04-12: without the CronCreate job, mail accumulates unprocessed.
- **CronCreate alone (session-scoped)** — Dies with the session. The
  durable variant (`durable: true`) survives restarts but was not available
  when DES-015 was originally written. Durable CronCreate solves the
  session-scoping problem; it does not solve detection (no `tools/list_changed`
  between ticks).

**Future — channels (eliminates the CronCreate layer):**

Claude Code's channels feature (`notifications/claude/channel`) lets an MCP
server push messages directly into the conversation queue as real prompts at
`priority: 'next'`. When channels ships for general use, beadle-email's
poller can fire a channel notification instead of `tools/list_changed`:

```text
notifications/claude/channel { content: "3 new messages. Process with /inbox." }
```

Claude Code picks this up between turns and acts on it — no CronCreate
needed. This collapses the two-layer design to one layer with perfect sync.

Channels is currently experimental and heavily gated (build flag, GrowthBook
gate, OAuth, managed policy, marketplace allowlist). Not available for
general use yet. When it ships, beadle-email should declare
`experimental['claude/channel']` in its MCP capabilities and upgrade the
poller to emit channel notifications. The CronCreate layer becomes
unnecessary. See `42-channels-architecture.tex` for the full protocol.

**Future — Docker sandbox (atomic launch):**

In the Docker sandbox model, beadle-email is the long-running daemon and
Claude Code is launched into it via:

```text
sbx run claude --prompt-file .beadle/startup.md
```

Where `startup.md` contains:

```text
/loop 5m /inbox 5m
```

This sets both layers atomically in one prompt: `/loop` creates the durable
CronCreate job, its immediate first execution runs `/inbox 5m` which sets
the server-side poll interval. No sync gap because both layers are configured
in the same turn. See beadle-vyv for the full orchestrator design.

## DES-016: Contact matching by email domain pattern

**Status:** SETTLED (beadle-a7v). Superseded by **DES-019**, which carries
the shipped design, the precedence rules, and the r-- safety constraint.

**Decision:** Add glob/domain-pattern matching to the contact system so a single
contact entry can cover all messages from a domain (e.g., `*@mail.anthropic.com`).

**Problem:** Anthropic uses per-message randomized sender addresses
(`no-reply-<random>@mail.anthropic.com`). The current contact system matches by
exact email address, so each new Anthropic message arrives from an unknown address
and gets blocked as `---`. Adding individual addresses doesn't scale — new ones
arrive with every message.

**See DES-019** for the implemented design.

## DES-017: Linux keychain backend — pass primary, secret-tool fallback

**Status:** SETTLED (beadle-9t8)

**Decision:** On Linux, the keychain resolution layer tries `pass`
(`pass show beadle/<name>`) first, then falls back to `secret-tool`
(`secret-tool lookup service beadle account <name>`). If neither binary
is installed, the resolution chain continues to the file backend and
environment variable as before.

**Why pass first:**

- **GPG-encrypted at rest with the user's own key.** `pass` is a thin
  wrapper over `gpg`: every entry is encrypted to the keys listed in
  `~/.password-store/.gpg-id`. The same trust anchor as beadle's own
  PGP signing identity. `secret-tool` delegates to libsecret which
  delegates to the session keyring (GNOME Keyring or KDE Wallet);
  the at-rest storage format and unlock ceremony are opaque to the
  user.
- **Matches Proton Bridge's vault backend on Linux.** When running
  Bridge as the unsandboxed .deb (see setup-guide.md), Bridge itself
  uses `pass` to store its account credentials when available. A
  developer who already has `pass` set up for Bridge gets beadle
  credentials in the same store, with the same unlock ceremony,
  without a second credential manager.
- **Cross-machine portability.** `pass` stores live in a plain
  directory tree (`~/.password-store/`) that can be synced via git,
  backed up as a tarball, or inspected without tooling. The libsecret
  backing store is a SQLite database in `~/.local/share/keyrings/`
  that is not portable without running its daemon.

**Why secret-tool as fallback, not primary:**

- `secret-tool` is pre-installed on Ubuntu GNOME desktops and the
  Secret Service API is the freedesktop.org standard, so a developer
  without `pass` still has a working keychain path.
- Fallback (not absence) means the test `keychainAvailable()` returns
  true whenever either binary is present, and `Available()` reports
  both so doctor and status commands are honest about what is wired.

**Namespace:**

- pass: `beadle/<name>` (e.g. `beadle/imap-password`)
- secret-tool: service=`beadle`, account=`<name>` (matches the Darwin
  `security` convention exactly, so users coming from macOS do not
  need to relearn the namespace)

**Resolution order inside `keychainGet`:**

1. Call `passRunner(name)`. If it returns a non-empty value with no
   error, return that value.
2. Else call `secretToolRunner(name)`. Same check.
3. Else return the last error seen (or a synthesized "no Linux
   keychain backend available" when both runners returned nil/empty
   without error). The caller in `secret.Get()` treats any error as
   "try the next backend in the resolution chain" and falls through
   to the file backend.

**Rejected alternatives:**

- **secret-tool only.** Simpler code path, but forces developers who
  already use `pass` (and Punt Labs conventions favor `pass`) to
  maintain a second credential manager just for beadle.
- **pass only.** Excludes the default GNOME desktop experience and
  forces every Linux user to install and initialize `pass` before
  beadle-email has any OS keychain integration.
- **D-Bus libsecret library binding.** Would avoid the subprocess
  cost and give better error granularity, but introduces a cgo/D-Bus
  dependency for a single code path that currently averages one
  keychain read per tool invocation. Not worth the dependency weight.
- **`claude plugin` style runner config file.** Out of scope — the
  existing Darwin pattern is a single hard-coded subprocess call and
  the Linux implementation matches it for consistency.

**Test strategy:** The runners are package-level vars
(`passRunner`, `secretToolRunner`) so unit tests swap in fakes and
exercise the priority-and-fallback logic without invoking real
subprocesses. Integration tests against real `pass` and `secret-tool`
binaries with a sandboxed fixture are future work (tracked in the
cross-cutting hardening bead beadle-2nk, which needs similar seams
for install.sh failure detection).

**Testing limitations (explicit):** This PR verifies the runner
priority logic and the "not installed" error paths. The following
behaviors were NOT directly verified with real subprocesses:

- `pass show` on a locked GPG keyring and its pinentry interaction
  with a stdio MCP server parent (may hang on a daemon host with no
  GUI session).
- `secret-tool lookup` on a locked GNOME keyring.
- Round-trip store-and-retrieve against a real `pass` or
  `secret-tool` binary.

The mocked tests are sufficient to prove the dispatch logic. The
empirical behaviors above get verified the first time a developer
runs `install.sh` on a Linux machine with these backends configured.

## DES-018: list_messages output format — single FROM column, ID as row prefix

**Status:** PROPOSED 2026-04-08. Replaces the post-0he/post-z34 format
that overflowed the 80-column budget and crushed SUBJECT to a 10-char
stub. Supersedes the EMAIL column from beadle-0he and the "(via X)"
relay annotation from beadle-z34.

**Decision:** Render `list_messages` as a 5-column table with a
3-character right-aligned ID slot in the row prefix position. The
sender display name and full email address are merged into a single
FROM column in the form `Name <email>`. The DATE column is compressed
to date-only (`Apr 08`, 6 chars). The trust glyph stays in its own
1-char column. The SUBJECT column is variable and absorbs the
remaining width. The 80-character row budget and the 3-character `▶`
indentation are both hard constraints; FROM is the elastic column
that gives way to satisfy them.

**Layout:**

```text
[ruler: 12345678901234567890123456789012345678901234567890123456789012345678901234567890]
▶    R  FROM                                   DATE    T  SUBJECT               
319  ●  Copilot <notifications@github.com>     Apr 08  ?  Re: [punt-labs/beadle…
320  ●  Pat Singh <notifications@github.com>   Apr 08  ?  Re: [punt-labs/beadle…
322  ●  cursor[bo… <notifications@github.com>  Apr 08  ?  Re: [punt-labs/beadle…
335  ●  vercel[bo… <notifications@github.com>  Apr 08  ?  Re: [punt-labs/public…
340  ●  Sam Jackson <sam@example.co.uk>        Apr 08  ✓  Re: [punt-labs/punt-k…
  8     Claude Agento <claude@punt-labs.com>   Apr 07  ✓  doctor fix landed     
  7     Alice Chen <alice@example.com>         Apr 06  ?  lunch thursday?       
```

**Slot widths and positions** (row width = 80 chars exactly):

| Slot | Char positions | Width | Content |
|------|---------------|-------|---------|
| ID | 1–3 | 3 | Right-aligned beadle message ID. Header position 1 contains `▶`; positions 2–3 are blank. |
| sep | 4–5 | 2 | Standard column separator. |
| R | 6 | 1 | Read marker: `●` for unread, space for read. |
| sep | 7–8 | 2 | |
| FROM | 9–45 | 37 | `Name <email>` form. |
| sep | 46–47 | 2 | |
| DATE | 48–53 | 6 | `Apr 08` format. |
| sep | 54–55 | 2 | |
| T | 56 | 1 | Trust glyph. |
| sep | 57–58 | 2 | |
| SUBJECT | 59–80 | 22 | Variable, takes the remaining budget. |

Total: 3 + 2 + 1 + 2 + 37 + 2 + 6 + 2 + 1 + 2 + 22 = 80.

**FROM column rules:**

1. **Format with display name:** `<displayname> <<email>>` —
   for example `Copilot <notifications@github.com>`.
2. **Format without display name:** bare email, no angle brackets —
   `ops@vendor.example`. The angle bracket form is reserved for the
   name + email combination.
3. **Email is never truncated.** Permission enforcement keys on the
   raw address; the operator must read it in full to make trust
   decisions.
4. **Display-name truncation:** when the rendered cell
   `name + " <" + email + ">"` exceeds 37 chars, the display name is
   truncated with a trailing ellipsis. The name cap is
   `37 − len(email) − 3` where 3 covers the space, the opening angle
   bracket, and the closing angle bracket.
5. For `notifications@github.com` (24 chars), the wrapped form
   `<notifications@github.com>` is 26 chars, leaving **10 chars** for
   the display name after the separating space. `Copilot` (7),
   `Pat Singh` (9), and `Alice Chen` (10) fit. `cursor[bot]`,
   `vercel[bot]`, `claude[bot]` (all 11 chars) truncate to
   `cursor[bo…`, `vercel[bo…`, `claude[bo…`, losing the trailing
   `]`. This is the deliberate trade-off: the `[bot]` suffix is
   recoverable from context, the email address is not.
6. For shorter emails (e.g. `claude@punt-labs.com` at 20 chars), the
   wrapped form is 22 chars, leaving 14 chars for the display name.
   `Claude Agento` (13) fits without truncation.
7. **The z34 `(via <domain>)` annotation is removed.** With the full
   email address visible inside FROM, a row like
   `Pat Singh <notifications@github.com>` already shows the actual
   sender domain — no annotation is needed to disambiguate. The
   annotation introduced unnecessary FROM-cell width and is replaced
   by the email itself.

**ID slot rules:**

1. ID is **not** a column with a header. It is a 3-character
   right-aligned row prefix. The header row puts `▶` at position 1
   with positions 2 and 3 blank.
2. Width grows beyond 3 when message IDs reach 4+ digits. The growth
   shifts every column right by the extra width and shrinks SUBJECT
   correspondingly. SUBJECT minWidth is 10; if growth would push
   SUBJECT below 10 the table widens past `tableWidth`.
3. Right-alignment puts shorter IDs flush-right in the slot
   (`"  8"`, `" 42"`, `"319"`).

**DATE format:**

- `Apr 08` — three-letter month, space, zero-padded day. Width = 6.
- No year. Inboxes are short-lived and the year context is not
  useful.
- No time-of-day. List view is day-precision; for finer time the
  operator opens `read_message`, which still displays the full
  `Date:` header.

**Trust glyph values (T column):**

| Glyph | Level |
|-------|-------|
| `✓` | trusted (Proton↔Proton internal E2E) |
| `+` | verified (external with valid PGP signature) |
| `?` | unverified (external, no signature) |
| `✗` | untrusted (external, invalid PGP signature) |

**Read marker values (R column):**

| Glyph | State |
|-------|-------|
| `●` | unread |
| (space) | read |

**SUBJECT column rules:**

- Variable-width column. Default width 22 with the 80-char row
  budget. The column takes whatever budget remains after every
  fixed-width slot.
- Truncated with a trailing ellipsis when content exceeds the cap.
  At width 22, `"Re: [punt-labs/beadle] fix(mcp): ..."` renders as
  `"Re: [punt-labs/beadle…"`. The closing `]` is replaced by the
  ellipsis; the operator sees the repo name (`beadle`) which is the
  load-bearing identifier in PR notification subjects.

**Width budget enforcement:**

`tableWidth` in `internal/mcp/table.go` stays at 80. FROM is the
elastic column: when the math forces a tradeoff, FROM gives way
before any other column. Concretely, this spec sets FROM = 37 (one
char narrower than would have been ideal) so that SUBJECT = 22 fits
within the 80-char budget. The 80-char convention is a hard
constraint shared with biff and every other beadle table.

**What this spec removes:**

- The EMAIL column added by beadle-0he. Email moves into FROM.
- The `(via <domain>)` annotation added by beadle-z34. Redundant once
  the full email address is visible.
- The 12-character `Apr 08 17:19` DATE format. Replaced by 6-char
  date-only.
- The ID column header. ID becomes a row prefix.

**What this spec preserves:**

- The 3-character `▶` indentation marker on header rows and matching
  3-character indent slot on data rows (occupied by the ID).
- The 2-character column separator convention.
- The trust glyph on every row.
- Every operator-facing field that was visible before 0he/z34.

**Test requirements:**

Every test added for this format must assert against rendered row
width, not just substring presence. Specifically:

1. A test that asserts every rendered row in `formatMessages` output
   is exactly `tableWidth` characters wide on representative inputs:
   short emails, long emails (24+ chars), long names, bot names, no
   display name, multibyte trust/read glyphs.
2. A regression test for the 0he+z34 width defect: rendering a
   message with `Pat Singh <notifications@github.com>` produces a
   row where the bare email substring is fully present in the FROM
   cell (no truncation of the email).
3. A test for bare-email senders that asserts the rendered FROM cell
   is `email@example.com` (no leading angle bracket) and not
   `<email@example.com>`.
4. A test for the `Re: [punt-labs/beadle]…` truncation behavior
   verifying SUBJECT renders the closing `]` before the ellipsis at
   the spec'd width.
5. A test for ID growth: when a message ID is 4 digits, the row
   prefix expands to 4 characters and SUBJECT shrinks by 1.

**Implementation notes:**

- The ID is currently a regular column in `formatMessages` (with
  minWidth 2, header `"ID"`). Convert it to a row-prefix slot. The
  table renderer in `internal/mcp/table.go` will need a new
  `idPrefix` per-row mechanism, or `formatMessages` will need to
  build its own header/row strings instead of going through
  `formatTable`.
- The EMAIL column is removed from the column list. The `splitSender`
  helper that produces `(name, addr)` is still used internally; the
  rendering now combines them via a new `formatFromCell` rule that
  outputs `Name <email>` instead of just `Name`.
- The `formatFromCell` helper from beadle-z34 (which currently emits
  the `(via X)` annotation) is replaced with a new helper that emits
  the `Name <email>` form with name truncation.
- All beadle-z34 helpers (`isRelay`, `relayDomainLabel`,
  `isAutomationLocal`, `isBotName`, `tokenize`, `domainLabels`,
  `splitAddress`) become unused and should be deleted along with
  their tests.
- `formatMessages_test.go` and `format_relay_test.go` need rewriting
  against the new format. The 0he and z34 tests that validated the
  EMAIL column and the `(via X)` annotation are no longer applicable.

**Open questions:**

1. **ID growth past 3 digits.** beadle's current message IDs are
   3-digit. When the inbox accumulates past 999 IDs, the prefix grows
   to 4 and SUBJECT shrinks to 21. The spec is forward-compatible;
   no mitigation needed today.
2. **Locale-aware DATE.** `Apr 08` is hardcoded English. If the
   operator runs in a non-English locale, the month abbreviation
   should follow Go's `time` package localization. Out of scope for
   this spec.

## DES-019: Domain-pattern contact matching (r-- only)

**Status:** SETTLED (beadle-a7v). Supersedes DES-016.

**Decision:** The `Contact.Email` field is dual-purpose. If the value
contains `*` or `?`, it is a glob pattern matched by `path.Match`;
otherwise it is an exact address. A single contact like
`*@mail.anthropic.com` with `r--` grants read to every sender whose
address satisfies the pattern, which solves the rotating-sender problem
for services like Anthropic, GitHub notifications, Amazon SES, and
SendGrid.

Pattern contacts are restricted to `r--`. Any permission string containing
`w` or `x` is rejected at `Validate` time and at the `add_contact` handler
with the error `pattern contacts may only grant read permission (r--), got
%q`. Full `rwx` grants still require an exact address.

**Why r-- only:** Granting write or execute to a whole domain is unsafe.
Any sender capable of submitting from that domain — including anyone who
spoofs the `From:` header at an upstream relay that does not enforce DMARC
alignment — would inherit reply or command authority. Read is the
maximum defensible grant for a glob. Exact addresses remain the only way
to grant write or execute because exact-match is scoped to a single
identity the operator has vetted.

**Lookup precedence:** `Store.FindByAddress(addr)` implements this:

1. Exact case-insensitive match on a non-pattern contact wins — first hit.
   A specific `attacker@mail.anthropic.com` with `---` beats a permissive
   `*@mail.anthropic.com` with `r--`, so the operator can blocklist a
   single rotating sender without revoking the whole domain.
2. Among pattern contacts whose `Email` matches `path.Match(pattern, addr)`,
   the longest pattern (rune count via `utf8.RuneCountInString`) wins.
   Since the variable part is always `*`, longer patterns carry more
   literal characters and are therefore more specific. Ties go to the
   contact added first.
3. No match returns `(Contact{}, false)`. The caller (`senderPermission`)
   treats that as unknown sender and falls through to the default `---`.

**Worked examples:**

| Store contents                                            | Lookup                              | Match                          |
|-----------------------------------------------------------|-------------------------------------|--------------------------------|
| `*@mail.anthropic.com` r--, `attacker@mail.anthropic.com` --- | `attacker@mail.anthropic.com` | `attacker@…` (exact beats pattern) |
| `*@mail.anthropic.com` r--                                | `no-reply-abc123@mail.anthropic.com` | pattern — grants r--           |
| `*@vercel.app` r--, `*@ci.vercel.app` r--                 | `sam@ci.vercel.app`                 | `*@ci.vercel.app` (longer)     |
| `*@MAIL.ANTHROPIC.COM` r--                                | `no-reply@mail.anthropic.com`       | pattern — case-insensitive     |

**Matcher:** `path.Match` from the standard library. It supports `*`
(any sequence), `?` (single char), and `[set]` brackets. No path-separator
semantics apply here since email addresses contain no `/`. Malformed
patterns like `[abc*@example.com` are rejected at `Validate` time by
probing the pattern against a throwaway string; a bad pattern surfaces
as `invalid pattern syntax: %w` rather than lying dormant until lookup.

**Schema invariants:** The `contacts.json` on-disk format is unchanged —
`Email` is still a string. No migration code is needed. A pattern entry is
indistinguishable from an exact entry at rest; the `IsPattern()` method
classifies it at read time by scanning for `*` or `?`. Existing exact
contacts continue to work without change.

**Why no caching:** Contact lookup happens once per message during
`list_messages`. A contact book with fewer than 100 entries runs pattern
iteration in microseconds. Cached lookups would add invalidation
complexity for a cost that does not show up in any profile.

**Why not a separate `Patterns` field:** A separate field would double
every code path that reads contacts (the store, the CLI, the MCP tools,
the `list_contacts` formatter) without changing behavior. Reusing `Email`
with a pure-function classifier keeps the data model and the on-disk
format unchanged.

**Rejected alternatives:**

- **Regex matching.** More expressive but dangerous — untrusted patterns
  could cause catastrophic backtracking. `path.Match` is linear and bounded.
- **Third-party glob library.** `path.Match` in the standard library covers
  everything email addresses need. Adding a dependency for no capability gain
  is tech debt.
- **Allowing rwx on patterns "if the user really wants it".** The CEO
  directive on 2026-04-09 was explicit: `rwx` for a pattern is never safe.
  The code enforces the rule so the operator cannot unknowingly grant
  reply authority to a whole domain.

**What this does not change:**

- Existing exact-address contacts. Their behavior, storage, and
  permission semantics are untouched.
- The `CheckPermission(c, identityEmail)` function. Pattern enforcement
  happens at `Validate` time, so any stored contact is already safe to
  pass through the normal permission lookup.
- The `contacts.json` file format. Old files load unchanged.
- The redaction rule. Unknown senders still get `---` and still see their
  subjects redacted in `list_messages`.

## DES-020: GPG signing keys must have an expiration date

**Status:** PROPOSED (beadle-72e)

**Decision:** Beadle rejects any GPG key as a signing key if it has no
expiration date. `CheckKeyExpiry` runs before every signing operation and
returns an error if the key is non-expiring. The signing path fails fast;
no partial signing occurs.

**Why:** A non-expiring key is a permanent credential with no automatic
revocation timeline. If the key is compromised, the attacker has unlimited
time to forge instructions. An expiring key bounds the damage window and
creates a periodic review forcing function. This invariant is stated in
CLAUDE.md under "Design Invariants" and is now enforced in code, not just
documentation.

**Detection:** `gpg --list-keys --with-colons <keyID>` emits
colon-delimited output. The `pub` record's field 6 (0-indexed) is the
expiry as a Unix timestamp. An empty field or literal `0` means no
expiry. `CheckKeyExpiry` parses this field and returns an error when
absent or zero.

**Rejected alternatives:**

- **Log a warning, proceed anyway.** Reduces signing security to
  advisory-only. The invariant must be enforced; a warning that is ignored
  is not an invariant.
- **Enforce at key registration time only.** An operator could generate a
  non-expiring key and load it without going through a registration
  step. Pre-signing checks are the only guaranteed enforcement point.
- **Check key validity (not just expiry).** A broader validity check
  (`gpg --status-fd` looking for `VALIDSIG`) would catch expired,
  revoked, and untrusted keys. The narrower expiry-only check is
  sufficient here: revoked keys already fail signing outright; the
  invariant specifically targets the "valid but non-expiring" case that
  would otherwise pass silently.

## DES-021: GPG key rotation procedure

**Status:** PROPOSED (beadle-72e)

**Decision:** The key rotation procedure for beadle signing keys is
defined here as the authoritative reference. All agents operating as a
beadle identity must follow this procedure when their signing key nears
expiry.

**Target expiry policy:** Signing keys expire in 1 year. Rotate when the
key has fewer than 30 days remaining.

**Rotation procedure:**

1. **Generate the new key** on the machine that will use it:

   ```bash
   gpg --batch --gen-key <<EOF
   %no-protection
   Key-Type: ed25519
   Key-Usage: sign
   Subkey-Type: cv25519
   Subkey-Usage: encrypt
   Name-Real: Claude Agento
   Name-Email: claude@punt-labs.com
   Expire-Date: 1y
   %commit
   EOF
   ```

2. **Verify the key has an expiry date:**

   ```bash
   gpg --list-keys --with-colons claude@punt-labs.com
   # pub field 7 (1-indexed) / field 6 (0-indexed) must be a non-zero Unix timestamp
   ```

3. **Export the public key** and distribute to correspondents who
   verify beadle signatures:

   ```bash
   gpg --armor --export claude@punt-labs.com > claude-pubkey.asc
   ```

4. **Update the ethos extension** with the new key ID:

   ```bash
   ethos ext set claude beadle gpg_key_id <NEW_KEY_ID>
   ```

5. **Update beadle config** (`~/.punt-labs/beadle/identities/claude@punt-labs.com/email.json`):
   set `gpg_signer` to the new key ID.

6. **Revoke and remove the old key** from the local keyring:

   ```bash
   gpg --gen-revoke <OLD_KEY_ID> > old-key-revocation.asc
   gpg --import old-key-revocation.asc
   gpg --delete-secret-and-public-key <OLD_KEY_ID>
   ```

7. **Verify beadle signs with the new key** by sending a signed test
   message and running `verify_signature` on the received copy.

**Operational note for `claude@punt-labs.com`:** The current signing key
must have an expiration date to satisfy the DES-020 invariant. If the
existing key is non-expiring, run step 6 first (revoke + delete old key)
then steps 1–5 to generate a compliant replacement.

**Why ed25519:** Compact, fast, immune to the parameter-choice
vulnerabilities that affect ECDSA (Sony PS3 attack). Standard choice for
new signing keys as of 2024.

**Why `%no-protection` in batch mode:** Batch key generation on a
headless daemon host has no pinentry. The private key is protected by
filesystem permissions (`mode 600`, owned by the agent user) and the
OS keychain (for export operations). Adding a passphrase would require
human intervention or storing the passphrase in a file — equivalent
protection at worse UX.

**Rejected alternatives:**

- **Extend expiry instead of rotating.** `gpg --quick-set-expire` can push
  the expiry date forward without generating a new key. Simpler, but
  carries forward any key material that may have been exposed. A full
  rotation is a harder security boundary.
- **Hardware security key (YubiKey/OpenPGP card).** The correct long-term
  answer for high-value signing keys. Out of scope for a daemon that must
  operate unattended — hardware tokens require physical presence for signing.

## DES-022: Outbound PGP signing via SMTP with MIME-preserving transport

**Status:** SETTLED (beadle-atz, PR #132, 2026-04-11)

**Decision:** All outbound email is PGP-signed (RFC 3156 `multipart/signed`)
when `gpg_signer` is configured in `email.json`. Signing is opt-in: when
`gpg_signer` is empty (the default), messages are sent unsigned. The signing
path requires a MIME-preserving SMTP transport; the Resend API fallback is
blocked when signing is enabled.

**Architecture:**

1. `ComposeSignedRaw()` builds the body part (text/plain, or multipart/mixed
   when attachments are present), canonicalizes line endings to CRLF (RFC 3156
   requirement), calls `pgp.DetachSignBody()` to create a detached signature,
   and wraps both in a `multipart/signed` envelope with full RFC 822 headers.
2. `DetachSignBody()` is a public wrapper around `detachSign()` that enforces
   the DES-020 key-expiry invariant before signing. The passphrase is passed
   to gpg via a mode-600 temp file to avoid `ps` exposure.
3. `TrySendChain()` checks `cfg.GPGSigner != ""`, resolves the passphrase
   (tolerating `secret.ErrNotFound` for unprotected keys), calls
   `ComposeSignedRaw` for the SMTP path, and blocks the Resend fallback.
   If SMTP fails and signing is enabled, the SMTP error is returned
   directly — no silent downgrade to unsigned delivery.

**CRLF canonicalization:** RFC 3156 requires the signed body part to use
canonical CRLF line endings. `ComposeSignedRaw` and `buildMixedBodyPart` both
normalize `textBody` via two-pass replacement: `\r\n` → `\n` → `\r\n`. This
handles mixed-ending input without double-converting.

**Transport requirement:** PGP-signed messages must transit via an SMTP
server that preserves raw MIME structure. Proton Bridge and Resend/SES both
strip `multipart/signed` envelopes (DES-005, DES-006). Fastmail SMTP
(port 465, implicit TLS) preserves `multipart/signed` — verified 2026-04-11
with GPG Mail green "Signed" checkmark on the recipient side.

**Rejected alternatives:**

- **Silent unsigned fallback via Resend when SMTP fails.** Trades
  cryptographic integrity for delivery reliability. An unsigned message that
  appears signed (because the config says to sign) is worse than a delivery
  failure. The operator expects signed mail; failing visibly is the correct
  behavior.
- **Inline PGP signing (clearsign).** Simpler to implement (no MIME
  manipulation) but not recognized by modern mail clients. GPG Mail, Outlook
  S/MIME, and Thunderbird all expect `multipart/signed` (RFC 3156).
- **Sign at the MCP tool layer instead of the transport layer.** Would
  require every MCP tool that sends email to know about signing. Placing it
  in `TrySendChain` makes signing transparent to all callers.
- **Default signing to on.** The original implementation defaulted
  `GPGSigner` to `FromAddress`, making signing implicit. Changed to opt-in
  (empty default) because signing requires a working GPG key with an expiry
  date, a passphrase in the credential store, and a MIME-preserving SMTP
  transport. Failing on every send for an unconfigured user is worse than
  sending unsigned until they opt in.

## DES-023: Port-based TLS auto-detection for IMAP and SMTP

**Status:** SETTLED (beadle-zle, PR #133, 2026-04-11)

**Decision:** IMAP and SMTP connections auto-detect the TLS mode based on
the configured port number. No explicit `tls_mode` config field is needed.

| Port | Protocol | TLS Mode | Implementation |
|------|----------|----------|----------------|
| 993 | IMAP | Implicit TLS (IMAPS) | `imapclient.DialTLS()` |
| 465 | SMTP | Implicit TLS (SMTPS) | `tls.DialWithDialer()` + `smtp.NewClient()` |
| Other | IMAP | STARTTLS | `net.DialTimeout()` + `imapclient.NewStartTLS()` |
| Other | SMTP | STARTTLS | `net.DialTimeout()` + `smtp.NewClient()` + `c.StartTLS()` |

**Why port-based:** RFC 8314 (2018) recommends implicit TLS on ports 993
(IMAPS) and 465 (SMTPS) as the preferred connection method. STARTTLS on
ports 143/587 is the legacy approach. Every standard email provider
(Fastmail, Gmail, Migadu, iCloud) uses implicit TLS on 993/465. Proton
Bridge uses STARTTLS on non-standard localhost ports. Port-based detection
handles both cases without user configuration.

**ServerName:** Both IMAP and SMTP set `tls.Config.ServerName` to the
configured host. For loopback addresses (127.0.0.1, ::1, localhost),
`InsecureSkipVerify` is set to true because Proton Bridge uses self-signed
certificates on localhost.

**Why not an explicit `tls_mode` config field:**

- Port numbers are already in the config. Adding a `tls_mode` field creates
  a redundant knob that can contradict the port.
- The mapping from port to TLS mode is an industry convention codified in
  RFC 8314. No standard email provider uses a non-standard combination.
- If a future provider uses an unusual combination, `tls_mode` can be added
  as an override without breaking the default behavior.

**Rejected alternatives:**

- **Always STARTTLS.** Breaks standard email providers on port 993/465 —
  they expect TLS from the first byte, not a plaintext upgrade.
- **Always implicit TLS.** Breaks Proton Bridge on localhost, which speaks
  STARTTLS on non-standard ports.
- **Explicit `tls_mode: "starttls" | "implicit" | "none"`.** Unnecessary
  complexity for the default case. Would be the only config field that
  duplicates information already encoded in the port number.

## DES-024: Inbound PGP decryption uses system keyring

**Status:** PROPOSED (beadle-ksk, PR #134, 2026-04-11). Pending security
review discussion — deferred by CEO.

**Decision:** `pgp.Decrypt()` runs `gpg --decrypt` using the default
GNUPGHOME (system keyring), not an isolated temporary keyring. This is a
deliberate departure from DES-003, which uses isolated GNUPGHOME for
verification.

**Why the asymmetry with verification:**

- **Verification** (DES-003) imports only *public* keys into a temp dir.
  Public keys are safe to copy — no secret material is exposed.
- **Decryption** requires the agent's *private* key. Copying a private key
  into a temp directory would expose it in a second filesystem location,
  doubling the attack surface. The system keyring (`~/.gnupg`) is the
  canonical location for the private key, protected by filesystem permissions
  and (optionally) a passphrase.

**Integration point:** `parseMessage()` in `imap.go` checks
`pgp.IsEncrypted(raw)` and `cfg.GPGSigner != ""` before attempting
decryption. The `GPGSigner` check gates decryption on having a configured
signing identity — a proxy for "this agent has PGP credentials."

**Silent fallback:** If decryption fails (wrong key, missing passphrase,
corrupted ciphertext), the error is silently discarded and the original
encrypted bytes are passed through to `ParseMIME`. The user sees the raw
PGP ciphertext instead of the decrypted body. This trades visibility for
robustness — a decryption failure should not prevent reading other messages
in the same inbox scan.

**RFC 3156 payload extraction:** `extractEncryptedPayload()` uses a dual
strategy: raw byte splitting on MIME boundary markers first, with
`mime/multipart` parsing as a fallback. The raw-split approach handles edge
cases where `mime/multipart` has already consumed the body reader. The
second MIME part (index 1) contains the ciphertext per RFC 3156.

**Passphrase handling:** Same temp-file pattern as `detachSign()` — the
passphrase is written to a mode-600 temp file and passed via
`--passphrase-file` to avoid `ps` exposure. The file is removed via `defer`.

**Test coverage:** 6 tests covering round-trip decrypt, encrypt+sign,
wrong-key failure, non-encrypted rejection, `IsEncrypted` classification,
and temp-file cleanup verification.

**Open question:** The CEO flagged the system keyring decision for post-
testing security review. The alternative — an isolated GNUPGHOME with the
private key imported per-operation — would provide hermetic isolation but
requires either copying the private key (exposure) or symlinking
`~/.gnupg/private-keys-v1.d/` (fragile, gpg-agent socket path issues).
Neither option is clearly better. This ADR will move to SETTLED or be
revised after the deferred discussion.

**Rejected alternatives:**

- **Isolated GNUPGHOME with key import.** Would require copying the private
  key to a temp directory per decryption operation. Doubles the locations
  where the private key exists on disk. The security benefit (hermetic
  isolation) is outweighed by the exposure risk.
- **Error on decryption failure.** Would prevent `read_message` from
  returning any content for encrypted messages when decryption fails.
  The `/inbox` processing loop would break on the first undecryptable message.
  Silent fallback is the safer operational choice.
- **Decrypt in list_messages (like DES-002 for verification).** Decryption
  is expensive (subprocess + key material access) and would add latency to
  every inbox scan. Only `read_message` decrypts — listing shows encrypted
  messages as `unverified` trust level.

## DES-025: Recursive MIME parsing for nested multipart

**Status:** SETTLED (beadle-qoa, PR #135, 2026-04-11)

**Decision:** `ParseMIME` uses a recursive `walkParts()` helper that
descends into nested multipart containers via `part.MultipartReader()`.

**Problem:** The original `ParseMIME` used flat iteration over the top-level
multipart reader. Messages with nested multipart structures — common in
PGP-signed email — returned "(no text body)" because the text/plain part
was one or more levels deeper than the iterator reached.

**Real-world trigger:** GPG Mail on macOS sends replies as:

```text
multipart/signed
├── multipart/alternative
│   ├── text/plain          ← actual body (depth 2)
│   └── text/html
└── application/pgp-signature
```

The flat iterator saw `multipart/alternative` as a leaf part, could not read
it as text, and returned "(no text body)".

**Solution:** `walkParts(mr, &plainBody, &htmlBody, &attachments)` checks
each part for `part.MultipartReader()`. If non-nil, it recurses into the
nested reader before continuing. Leaf parts (text/plain, text/html,
attachments) are accumulated via pointer parameters across recursive calls.
First text/plain wins; first text/html wins; PGP signatures are skipped.

**Recursion strategy:** Depth-first traversal using Go's call stack. No
explicit depth limit — MIME nesting deeper than 5 levels is pathological
and not observed in real mail. The `go-message` library's `MultipartReader()`
returns nil for non-multipart parts, providing the natural recursion base
case.

**Test coverage:** Three nested structures tested:

| Test | Structure | Depth |
|------|-----------|-------|
| `TestParseMIME_NestedMultipartAlternative` | signed → alternative → text + html | 2 |
| `TestParseMIME_NestedMultipartMixed` | signed → mixed → text + attachment | 2 |
| `TestParseMIME_TripleNested` | signed → mixed → alternative + attachment | 3 |

All three verify correct text extraction and attachment discovery at depth.

**What this does not change:**

- `ParseMIMEStructure` and `ExtractPart` still use flat iteration. These
  diagnostic tools show the raw MIME structure; recursive flattening would
  lose the structural information they exist to expose.
- Trust classification. `ClassifyTrust` reads the top-level Content-Type
  header for signature detection, which is unaffected by body parsing depth.

## DES-026: Docker containerization — WebSocket + mcp-proxy + sandbox

**Status:** SETTLED (beadle-o1w, 2026-04-12). Design reviewed through 3
security rounds (adb architecture, djb evaluation).

**Decision:** beadle-email ships as a Docker image
(`ghcr.io/punt-labs/beadle-email`) with a WebSocket transport. Claude Code
connects via mcp-proxy. The image targets both Docker Sandbox (macOS, microVM
isolation) and traditional Docker (Linux, container isolation).

**Architecture:**

```text
Claude Code <--stdio--> mcp-proxy <--WebSocket--> [Container: beadle-email]
                                                          |
                                                          +-- IMAP --> mail server
                                                          +-- SMTP --> mail server
```

**Transport:** WebSocket at `/mcp` on port 8420 (`--transport ws --port 8420`).
mcp-proxy bridges stdio (Claude Code side) to WebSocket (container side).
N:1 multi-session supported via `?session_key=<pid>` (accepted on the
WebSocket upgrade URL but not yet used for routing — reserved for future
per-repo message filtering). stdio remains the default for local
(non-Docker) installs.

**Base image:** `debian:bookworm-slim` + gnupg + ca-certificates. ~80 MB.
Non-root user (beadle, UID 1000). Read-only rootfs. All capabilities dropped.

**GPG keyring:** Host keyring mounted read-only at `/mnt/gpg-source`.
Entrypoint script copies to tmpfs-backed `~/.gnupg` at startup. Private key
material exists only in memory — destroyed on container stop.

**Credentials:** Secret files mounted read-only at
`~/.punt-labs/beadle/secrets/`. Existing `secret.Get` chain works unchanged
(keychain fails gracefully in Docker, file backend succeeds). Env var
fallback for convenience.

**TLS for Proton Bridge:** `tls_skip_verify: true` in `email.json` for
Bridge's self-signed certs via `host.docker.internal`. The `isLoopback()`
function is NOT extended to match `host.docker.internal` — that hostname
resolves via DNS, which is a mutable trust boundary. Explicit config field
instead (security review FINDING-7).

**Health check:** Built-in `beadle-email health --port 8420` subcommand.
No wget in the image.

**Security model (8 threats):**

| # | Threat | Residual |
|---|--------|----------|
| 1 | Secret exposure via docker inspect | Low |
| 2 | GPG private key (tmpfs only) | High |
| 3 | Secret file permission bypass | Low |
| 4 | Container escape via gpg subprocess | Low |
| 5 | Network exposure (sandbox: none; Docker: mcp-proxy token) | Low/Medium |
| 6 | mcp-proxy as trusted computing base | Low |
| 7 | Image supply chain (digest-pinned) | Low |
| 8 | Persistent compromise via writable data volume | Medium |

**Rejected alternatives:**

- **Streamable HTTP** — Requires custom bearer token + CSRF defense in beadle.
  Sandbox isolation eliminates this need. WebSocket is bidirectional (server
  push for inbox notifications) and matches quarry's pattern.
- **stdio via docker exec** — Requires docker CLI on host, conflicts with
  health checks, prevents multi-session.
- **Alpine base** — musl libc + gnupg has socket and pinentry issues.
- **Proton Bridge in same container** — 500+ MB, Qt dependencies, complex
  entrypoint. Bridge runs on host instead.
- **`isLoopback()` extension for host.docker.internal** — DNS-dependent
  trust decision. Explicit `tls_skip_verify` config instead.

**Full design document:** `.tmp/missions/results/o1w-docker-design.yaml`

## DES-027: Orchestrator — daemon spawns Claude Code workers via print mode

**Status:** PROPOSED (beadle-vyv, 2026-04-13).

**Decision:** Beadle's orchestrator is a Go daemon that spawns ephemeral
Claude Code sessions in print mode (`claude -p`) to execute ethos missions
triggered by email. The daemon owns detection, classification, authorization
(x-bit), mission lifecycle, and pipeline orchestration. Claude Code owns
execution.

**Why a daemon, not channels or CronCreate:** Channels (beadle-9rb) is blocked
on the Claude Code client gate as of 2026-04-13 (see
`docs/channels-testing-blocked.md`). CronCreate depends on a human having
Claude Code open. Neither path delivers autonomous, always-on inbox processing.
A standalone daemon does.

**Spawn mechanism:**

```text
beadle-daemon (Go, always-on)
    |
    +-- polls INBOX via existing Poller (detection)
    |
    +-- checks x-bit authorization gate
    |    (Phase 1: all emails from x-bit contacts are instructions)
    |
    +-- ethos mission create --file contract.yaml
    |
    +-- exec.Command("claude", "-p",
    |       "--bare",
    |       "--mcp-config", missionMCP,
    |       "--append-system-prompt-file", missionPrompt,
    |       "--output-format", "json",
    |       "--max-turns", "50",
    |       "--max-budget-usd", "5.00",
    |       "--permission-mode", "auto",
    |       "--allowedTools", "Bash,Read,Edit,Write,Glob,Grep,Agent",
    |       "Execute mission " + missionID)
    |
    +-- parse JSON output {result, session_id, ...}
    |
    +-- ethos mission results <id> --json
    |
    +-- verify ethos result artifact exists
    |       +-- missing → treat mission as incomplete
    |       +-- present → ethos mission close <id>
```

**`--bare` mode is mandatory.** Bare mode skips hooks, plugins, MCP servers,
CLAUDE.md, and auto memory. The daemon provides exactly the context each
mission needs via `--mcp-config` and `--append-system-prompt-file`. No
ambient state from the host leaks into the worker session. This is both a
security property (worker sees only what the daemon authorizes) and a
reproducibility property (same inputs → same behavior on any machine).

**Authentication:** `ANTHROPIC_API_KEY` for API-billed usage, or a long-lived
OAuth token generated via `claude setup-token` for subscription billing.
Stored in beadle's secret store (`~/.punt-labs/beadle/secrets/claude-api-key`
or `secrets/claude-token`). The daemon sets the env var before spawning.

**MCP config per mission:** The daemon writes a temporary `mission-mcp.json`
with exactly the MCP servers the mission needs:

```json
{
  "mcpServers": {
    "ethos": {"command": "ethos", "args": ["mcp"]},
    "beadle-email": {"command": "beadle-email", "args": ["serve"]}
  }
}
```

Different mission types may need different tool stacks. An inbox-reply mission
needs beadle-email. A code-review mission needs only filesystem tools.

**Mission prompt template:** Written to a temp file per mission, injected via
`--append-system-prompt-file`:

```text
You are a beadle mission worker. Your mission contract is {mission_id}.
Read it: ethos mission show {mission_id}
Execute within the write_set and budget constraints.
When done, submit your result: ethos mission result {mission_id} --file <path>
Do not commit, push, or merge unless the contract explicitly says to.
```

**Result collection:** Two channels:

1. **stdout JSON** — `--output-format json` returns `{result: string}`. The
   daemon captures stdout and parses JSON for the text result and session
   metadata.
2. **Ethos result artifact** — The worker runs
   `ethos mission result <id> --file result.yaml`. The daemon reads it via
   `ethos mission results <id> --json`.

Both must succeed. If stdout JSON parses but no ethos result artifact exists,
the daemon treats the mission as incomplete.

**Safety bounds:**

| Bound | Flag | Default | Purpose |
|-------|------|---------|---------|
| Turns | `--max-turns` | 50 | Prevent runaway loops |
| Budget | `--max-budget-usd` | 5.00 | Cost cap per mission |
| Timeout | `exec.CommandContext` | 30 min | Process-level kill |
| Permissions | `--permission-mode auto` | — | No human in the loop |

**Failure modes:**

| Failure | Detection | Response |
|---------|-----------|----------|
| Non-zero exit | `cmd.Wait()` error | Mark mission failed, notify requester |
| Max turns exceeded | Exit with error, JSON `is_error: true` | Mark failed, retriable |
| Budget exceeded | Exit with error | Mark failed, not retriable |
| Process timeout | Context deadline exceeded | Kill process, mark failed |
| Worker hangs | Timeout fires | Same as timeout |
| Malformed output | JSON parse error | Mark failed, log raw stdout |
| IMAP error | Poller `recordFailure` | Retry next poll cycle |

**Concurrency:** Serial to start (one mission at a time, `sync.Mutex`).
The daemon queues pending missions and drains them one by one. Future:
bounded parallelism via `chan struct{}` semaphore. Serial-first because
the orchestrator is new and concurrent mission execution introduces
failure modes (resource contention, interleaved file edits) that are
harder to debug than serial bottlenecks.

**Instruction detection:** Phase 1 treats ALL emails from x-bit contacts as
instructions. This is correct by the rwx model — `x` means "I authorize
this contact to trigger autonomous actions." If a contact has `x` and sends
a message, they intend it to be acted on. Phase 2 (future) may add LLM
classification to distinguish instructions from informational messages
within x-bit contacts, but this is an optimization, not a prerequisite.

**Deployment modes (revised):**

| Mode | Poller | Processing | Mission Control | Human required |
|------|--------|------------|-----------------|----------------|
| Attended (today) | MCP server goroutine | CronCreate `/loop 5m /inbox` | Manual | Yes |
| Daemon (vyv) | Daemon goroutine | Daemon spawns `claude -p` | Daemon | No |
| Docker daemon (9zh) | Same daemon, in container | Same spawn, in container | Same | No |

Attended mode and daemon mode are independent. Attended mode serves the
"Claude is my assistant" workflow. Daemon mode serves the "beadle runs
autonomously" workflow. Both use the same Poller, the same email tools,
the same trust model.

**Pipeline orchestration:** Piped missions (per orchestrator-design.md) are
daemon-driven: mission 1 completes → daemon reads result → daemon creates
mission 2 with previous output as input. Pipeline state lives in the daemon,
not in ethos. Ethos provides the individual mission primitive.

**Relationship to channels (beadle-9rb):** When/if channels unblocks, it
provides a third processing mode (attended + channels = autonomous inbox
inside an interactive session). Channels and daemon are complementary, not
competing. Channels works when a human has Claude Code open. Daemon works
when no one is home.

**Rejected alternatives:**

- **Channels-only architecture** — Requires Claude Code running with a human
  session. Not suitable for always-on autonomous processing. Also currently
  blocked by the client gate.
- **CronCreate-only** — Requires Claude Code running. `/loop 5m /inbox` only
  fires when idle. Not reliable for SLA-bound inbox processing.
- **Interactive mode spawn** — `claude "prompt"` (no `-p`) opens an interactive
  session. Cannot be driven by a daemon. No structured output. No budget cap.
- **Agent SDK (Python/TypeScript)** — The SDK packages offer programmatic
  control, but the daemon is Go. Shelling out to `claude -p` is simpler and
  avoids a polyglot runtime. If the Go SDK materializes, migrate.
- **Full `--mcp-config` with all tools** — Rejected in favor of per-mission
  configs. Each mission gets exactly the tools it needs. Principle of least
  privilege.
- **Concurrent missions from day 1** — Serial first. Concurrent introduces
  file-edit races and resource contention. Add parallelism after serial works.

**Full design document:** `docs/orchestrator-design.md`

## DES-028: Pipeline orchestrator — missions as typed commands with composition

**Status:** PROPOSED (beadle-88g, 2026-04-14).

**Decision:** The daemon's pipeline orchestrator treats ethos missions as
typed commands. An email instruction decomposes into a sequence of commands
that execute as chained missions. Each command has a name, write_set, budget,
prompt template, and input/output contract. Pipelines are strictly linear
(no branching). The daemon owns pipeline state.

**Insight:** Missions are not just "tasks for Claude to figure out." They are
the command abstraction for the daemon. `Bash(git log)` wrapped in a mission
gets x-bit gating, audit trail, and bounded execution for free. Pipeline
composition is Unix pipes for an agent daemon.

**Ethos dependency:** Beadle's extensibility is hard-dependent on ethos.
Commands, pipelines, and audit all flow through ethos missions. This is
intentional — identity, authorization, delegation, and audit are ethos
primitives. Building a parallel system would be worse than the dependency.

**Three layers of abstraction:**

```text
Email instruction (natural language)
  ↓ decomposition
Pipeline definition [cmd1 | cmd2 | cmd3]
  ↓ execution (daemon)
Mission sequence [m-001 → m-002 → m-003]
```

**Command definition (typed args, no string interpolation):**

```yaml
# ~/.punt-labs/beadle/commands/wall.yaml
name: wall
description: Broadcast a message to all active agents via biff
signature: <owner GPG signature>
args:
  - name: message
    type: string
    max_length: 500
    required: true
input: none              # none | optional | required
output: prose            # prose | json | files
write_set: []
budget:
  rounds: 1
  reflection_after_each: false
timeout: 2m              # per-command timeout
prompt: |
  You have a structured argument "message" in the mission contract's
  inputs.args field. Read it with: ethos mission show {mission_id}
  Call biff wall with the message value. Do not interpolate the value
  into a shell string — pass it as a direct argument.
tools:
  - Bash
mcp_servers:
  - ethos
  - biff
env_vars:
  - BIFF_TOKEN            # daemon resolves from secret store
```

**Critical: no `{input}` string interpolation.** Args flow as structured
data in the mission contract's `inputs.args` field, not as string
substitution into prompts. The worker reads args from the contract
via `ethos mission show`. When calling Bash, the worker assembles
argument lists from structured data, never from template strings.
This prevents shell injection via attacker-influenced planner output.

Commands are YAML files in `~/.punt-labs/beadle/commands/`. Each file
is GPG-signed by the owner's key. The daemon verifies signatures at
startup and rejects unsigned or tampered files. This prevents privilege
escalation via filesystem write → command injection.

Validated at load time: required fields present, arg types recognized,
write_set is a list, budget.rounds > 0, timeout parseable. Malformed
files are logged and excluded from the available command set.

**Pipeline execution:**

1. Email arrives from x-bit contact — daemon verifies transport trust
   (trusted or verified required; unverified messages rejected)
2. Daemon creates a "planner" mission — Claude decomposes the instruction
   into a command sequence
3. Daemon validates planner output: each command name exists, each arg
   matches the command's declared schema (type, required, constraints)
4. Daemon executes commands sequentially:
   - Create mission from command template + validated args
   - Spawn worker, collect result
   - Pass result as structured input to next command
5. On completion: notify requester with final output
6. On failure: else clause fires, requester gets fixed-text error

**Planner mission and validation:** The planner returns a JSON array:

```json
[
  {"command": "deploy", "args": {"target": "production"}},
  {"command": "wall", "args": {"message": "deployed to production"}}
]
```

The daemon validates this output BEFORE creating any command mission:

- Each `command` name must exist in the loaded (signed) command set
- Each `args` entry must match the command's declared arg schema
  (type check, required check, max_length check, enum check)
- Unknown arg names are rejected
- Missing required args are rejected

If validation fails, the pipeline enters the else clause immediately.
The planner's output is untrusted LLM text influenced by the email —
validation is the security boundary.

**Planner interface:** The planner is behind an interface:

```go
type Planner interface {
    Plan(ctx context.Context, meta EmailMeta, body string) ([]CommandCall, error)
}
```

Two implementations: `LLMPlanner` (ethos mission) and `RulePlanner`
(regex/keyword config file). The daemon uses the configured planner.
RulePlanner enables fast-path execution (skip LLM for known patterns)
and deterministic CI testing.

**Input/output flow:** Each command's output is the ethos result artifact's
`prose` field (free text) or structured JSON per the command's `output`
declaration. The daemon reads it via `ethos mission results <id> --json`
and passes it as `inputs.previous_output` in the next mission contract.
Files flow via the result artifact's `files_changed` paths.

**Pipeline state (persisted):**

```go
type Pipeline struct {
    Version   int          // schema version for forward compatibility
    ID        string       // unique pipeline ID
    CreatedAt time.Time    // pipeline creation time
    Email     EmailMeta    // triggering email
    Commands  []Command    // try: planned sequence
    ElseCmd   *Command     // else: error handler (default: reply)
    Current   int          // index of current command
    Results   []string     // collected outputs
    Status    string       // running, completed, failed
    Error     string       // failure reason (internal, not sent to user)
    WriteSet  []string     // union of all command write_sets (for locking)
}
```

Pipeline state is persisted to `~/.punt-labs/beadle/pipelines/<id>.json`
at each state transition via atomic rename. On daemon startup, the daemon
scans for pipelines in `running` status and sends failure notifications
to the requester. This is Phase 1 work — silent failure on crash is
unacceptable for an autonomous agent.

**Write_set concurrency:** The Pipeline records the union of all its
command write_sets at creation. Before starting a pipeline, the daemon
checks for overlap with any in-flight pipeline's WriteSet. Overlapping
pipelines are queued, not rejected. Serial execution makes this a no-op
today but the data structure is in place for parallelism.

**Error handling — try/else with fixed-text errors:**

```text
try:  [plan | deploy | wall "deployed"]
else: [reply <fixed-text error>]
```

The else clause sends a fixed-text error to the requester:
"Your request could not be completed. Reference: pipeline-abc123."
Internal details (exit codes, stderr, budget amounts, command names)
go to the daemon log only, not to the email reply. This prevents
information leakage to attackers who trigger specific failures as an
oracle. The owner can look up the reference in the daemon log.

**Safety model:**

- **Transport trust required** — unverified messages from x-bit contacts
  are rejected (DES-012 mandate)
- **x-bit gates the pipeline** — same as single missions
- **Command files GPG-signed** — prevents filesystem → code execution
- **Typed args, no string interpolation** — prevents shell injection
- **Planner output validated against schemas** — untrusted LLM output
  is the security boundary
- **Each command has its own write_set** — scoped per stage
- **Pipeline write_set union locked** — prevents concurrent overlap
- **Each command has its own budget + timeout** — bounded per stage
- **Fixed-text error replies** — no internal state leakage
- **Pipeline state persisted** — no silent loss on crash
- **Env vars declared per command** — daemon resolves from secret store,
  never passes full env
- **Audit trail** — each mission links to the pipeline via `inputs.ticket`

**Rejected alternatives:**

- **String interpolation `{input}` in prompts** — Shell injection vector.
  Replaced with structured args in mission contract.
- **Unsigned command YAML files** — Filesystem write = arbitrary code
  execution. GPG signatures required.
- **In-memory-only pipeline state** — Daemon crash = silent failure.
  Persist at each transition.
- **Full error details in reply** — Information oracle for attackers.
  Fixed-text errors with reference ID.
- **LLM-free decomposition only** — Too brittle for natural language.
  But RulePlanner as fast-path is supported via Planner interface.
- **Single mission for entire pipeline** — Loses per-stage scoping,
  budget control, and audit granularity.
- **DAG execution (parallel stages)** — Complexity not justified yet.
  Linear covers the use cases.
- **Pipeline state in ethos** — Over-engineers the ethos primitive for
  one consumer.

**Full design document:** `docs/orchestrator-design.md`

## DES-029: Reply is a pipeline stage, not daemon I/O

**Status:** SETTLED (2026-04-17).

**Decision:** The daemon auto-appends a `reply` command as the terminal stage
of every pipeline. The planner returns work stages only (e.g., `[summarize]`).
The executor appends `reply` with the originating sender and final output as
args. The else handler also appends `reply` with a fixed-text error.

**Why a stage, not daemon I/O:**

1. **Multi-channel.** Email is one channel. Chat (biff), Telegram, Discord
   could be others. `reply_via_email`, `reply_via_chat` are different
   commands with different tools. A stage is the right abstraction because
   different channels need different MCP servers.

2. **Commands produce output, the daemon delivers it.** Unix metaphor:
   `summarize` writes to stdout, the shell displays it. The daemon is the
   shell. But the delivery mechanism depends on the channel, so the reply
   stage is where channel knowledge lives.

3. **Reply always happens.** Every pipeline ends with acknowledgment. The
   else handler replies with a fixed-text error. The originator never
   gets silence.

4. **Send vs reply.** `send` routes output to a different address (mid-pipeline).
   `reply` acknowledges back to the originator (terminal, auto-appended).

**Rejected alternatives:**

- **Reply as daemon I/O** — Daemon sends reply directly. Hardcodes email
  as the only channel. Adding chat requires modifying the daemon.
- **Planner specifies reply** — Every rule must include a reply stage.
  Verbose, error-prone. The daemon should handle it.
- **No auto-reply** — Originator expects a response. Auto-reply is the
  safe default. Future: commands can declare `reply: false` to suppress.

## DES-030: Multi-runner commands — claude and cli

**Status:** SETTLED (2026-04-18).

**Decision:** Commands declare a `runner` field that determines how the
daemon executes them. Two runners: `claude` (spawn `claude -p --bare`,
current behavior) and `cli` (exec a binary directly). The executor
dispatches to the appropriate runner per stage. Output piping between
stages is runner-agnostic.

**Why:** The current implementation wraps every command in a Claude Code
session. A `biff wall "deployed"` takes 45-60 seconds through Claude
when the actual operation is a 10ms subprocess. CLI tools that already
exist (biff, ethos, git, curl) should execute directly. Claude sessions
are for work that requires reasoning — summarization, analysis, code
generation. Deterministic operations should not pay the LLM tax.

**Command YAML with runner field:**

```yaml
# CLI runner — direct binary execution
name: wall
runner: cli
mode: passthrough
description: Broadcast a message to all active agents via biff
binary: biff
args:
  - name: message
    type: string
    max_length: 500
    required: true
    position: 2          # positional: biff wall <message>
fixed_args: ["wall"]     # prepended before positional args
output_schema: text
timeout: 30s
```

```yaml
# Claude runner — LLM session (default, current behavior)
name: summarize
runner: claude            # default if omitted
mode: process             # default if omitted
description: Summarize the triggering email
mcp_servers: [ethos, beadle-email]
output_schema:
  type: object
  properties:
    title: { type: string }
    summary: { type: string }
write_set: []
budget:
  rounds: 1
  reflection_after_each: false
timeout: 5m
prompt: |
  Read the triggering message via beadle-email and produce a JSON
  summary with title and summary fields.
```

**Executor dispatch:**

```text
for each stage in pipeline:
  switch command.runner:
    case "claude":
      create mission → build MCP config → spawn claude -p --bare
      → collect output from mission result → close mission
    case "cli":
      resolve binary path → build arg list from typed args
      → exec.CommandContext with timeout → capture stdout
      → validate exit code (0 = success, nonzero = else clause)
```

**CLI runner execution model:**

1. **Binary resolution.** The `binary` field names an executable. The
   daemon resolves it against a whitelist of allowed paths, not the
   system PATH. Default whitelist: `~/.local/bin/`, the beadle install
   directory, and paths declared in a daemon config. Binaries outside
   the whitelist are rejected at load time.
2. **Argument assembly.** `fixed_args` are prepended. Typed args with
   a `position` field are appended in position order. Named args without
   `position` become `--name=value` flags. No shell expansion, no
   globbing, no interpolation — `exec.Command(binary, args...)` only.
3. **Environment.** Minimal env: `PATH` (restricted), `HOME`, `USER`.
   Additional env vars declared in the command's `env_vars` field are
   resolved from the daemon's secret store and injected. The daemon's
   own credentials (ANTHROPIC_API_KEY, IMAP password) are never passed
   unless explicitly declared.
4. **Output capture.** Stdout is captured as the stage output (piped to
   the next stage or to auto-reply). Stderr goes to the daemon log.
   Output is capped at the same limit as Claude runner output.
5. **Exit code.** 0 = success, proceed to next stage. Nonzero = stage
   failure, enter else clause.
6. **Timeout.** `context.WithTimeout` on the exec, same as Claude runner.

**No missions for CLI runner.** Ethos missions add value for Claude
sessions: write-set enforcement, round budgets, structured results,
evaluator review. For a deterministic CLI call, missions are pure
overhead. The pipeline's own audit trail (pipeline ID, stage index,
command name, exit code, duration, truncated output) provides
sufficient traceability.

**Hybrid pipelines:**

```text
try: [summarize(claude) | wall(cli) | reply(claude)]
```

Stage 0: Claude summarizes the email (45s). Stage 1: biff broadcasts
the summary (10ms). Stage 2: auto-appended reply sends the summary
back to the sender. The executor doesn't care about runner boundaries
— each stage produces output, the next stage consumes it.

**Security model by runner:**

| Concern | claude runner | cli runner |
|---------|--------------|------------|
| Binary trust | `claude` binary is fixed | Whitelist-only resolution |
| Arg injection | Typed args in mission contract | Typed args in exec.Command |
| Shell injection | No shell involved | No shell involved |
| Env leakage | Minimal env + declared vars | Minimal env + declared vars |
| Output leakage | Mission result, not raw | Stdout captured, stderr logged |
| Timeout | 30m default | Per-command (default 30s) |
| Audit | Ethos mission trail | Pipeline state log |
| Isolation | --bare mode, adversarial prompt | No LLM, no prompt injection surface |

CLI commands have a smaller attack surface than Claude commands — no
prompt injection vector exists because there is no prompt. The main
risk is binary substitution (mitigated by whitelist) and argument
injection (mitigated by typed args with no shell expansion).

**Command YAML signing applies to both runners.** GPG-signed YAML
prevents an attacker from adding a `runner: cli` command that execs
an arbitrary binary. The signature covers the binary path, args schema,
and all fields. The whitelist is defense in depth — even a validly
signed command can only exec whitelisted binaries.

**Rejected alternatives:**

- **Shell execution (`sh -c`)** — Shell injection surface. The daemon
  must never invoke a shell. `exec.Command` with explicit arg list only.
- **Missions for CLI commands** — 2-3 seconds of ethos overhead per
  CLI call (create, result, close). For a 10ms `biff wall`, that's
  300x overhead. Pipeline audit trail is sufficient.
- **Separate command directories per runner** — Unnecessary complexity.
  The `runner` field in the YAML is sufficient. Both types coexist in
  `~/.punt-labs/beadle/commands/`.
- **Dynamic binary resolution via system PATH** — Attacker who controls
  PATH controls execution. Whitelist-only resolution.
- **Runner as a daemon-level config, not per-command** — Some commands
  are inherently LLM tasks (summarize), others are inherently CLI
  (wall). The command author knows which. Per-command declaration.
- **Plugin/extension model instead of runners** — Over-engineers
  the dispatch. Two runners cover the space: reasoning (claude) and
  deterministic (cli). A third runner (e.g., `http` for webhook calls)
  can be added later without changing the model.

## DES-031: Pipeline v2 — process/passthrough data flow with compound commands

**Status:** SETTLED (2026-04-18).

**Decision:** Pipeline stages operate in one of two modes: `process`
(output replaces the pipe) or `passthrough` (pipe carries forward
unchanged). CLI commands support compound `steps` — a mini-pipeline of
binaries chained by stdin/stdout. The pipe payload is JSON by default,
validated by `output_schema`. Commands declare runner-conditional fields:
`prompt` and `budget` for `claude`, `binary` and `steps` for `cli`.

**Why:** Side-effect commands (notifications, logging) destroyed the
pipe under v1's linear model. The `biff wall` example: its stdout
("ok") overwrote the summary from the prior stage, so the reply
command received "ok" instead of the summary. `passthrough` mode
solves this — the command reads the pipe but doesn't modify it.

Compound steps solve data extraction: `jq -r ".title" | biff wall`
extracts a field from JSON before passing it to a CLI tool, without
invoking a shell.

**Key design points:**

- One pipe value, no accumulator, no stage-indexed history
- `mode: process` (default) replaces the pipe; `mode: passthrough`
  preserves it
- `output_schema` replaces the retired `output` and `input` fields
- Compound steps run as concurrent goroutines with `io.Pipe` chaining
- Binary whitelist checked at both load time and execution time
  (with `filepath.EvalSymlinks` to follow symlinks)
- 1 MB output cap via `io.LimitReader` on the read side
- Pipe value passed as `inputs.pipeline_output` in mission contracts
- Else-path reply receives fixed-text error, not internal pipe state

**Full design document:** `docs/pipeline-v2-design.md`

**Rejected alternatives:**

- **Accumulator model** — violates information hiding. Any stage can
  read any prior output, expanding the data surface.
- **Shell execution for compound commands** — injection surface.
- **Unstructured (text) pipe by default** — CLI tools need JSON for
  `jq` field extraction. Text is the explicit opt-out (`output_schema: text`).
- **Missions for CLI commands** — 300x overhead for a 10ms operation.

## DES-032: Pipeline files — attachments, generated artifacts, and file flow

**Status:** PROPOSED (2026-04-18).

**Decision:** The pipe carries two parts: `data` (JSON value, as in
DES-031) and `files` (array of file references). Both follow the same
process/passthrough semantics. The daemon pre-downloads email
attachments before the pipeline starts and populates the initial
`files` array. Process-mode commands can add, remove, or replace files.
Passthrough-mode commands can read files but the array passes through
unchanged.

**Why:** Email attachments (PDFs, images, logs) are invisible to the
current pipeline. A user sends "summarize the attached report" but no
stage can access the attachment. Generalizing from "attachments" to
"files" covers both inbound attachments and stage-generated artifacts
(charts, compiled output, transformed documents). The reply stage can
attach files to the outbound email.

**Pipe representation:** The pipe remains a JSON string (`pipe string`
in the Runner interface). The JSON shape changes from a flat value to
`{"data": ..., "files": [...]}`. No Runner signature change, no mock
migration, no persistence change. The executor parses and serializes
the struct internally. This is consistent with DES-031's decision to
keep the pipe as a string.

**PipeValue struct (internal to executor):**

```go
type PipeValue struct {
    Data  json.RawMessage `json:"data"`
    Files []PipeFile      `json:"files"`
}

type PipeFile struct {
    Name     string `json:"name"`
    Path     string `json:"path"`      // relative to pipeline directory
    MIMEType string `json:"mime_type"`
    Size     int64  `json:"size"`
    Origin   string `json:"origin"`    // "attachment" or "stage-N"
}
```

`Data` is `json.RawMessage` to preserve the arbitrary JSON value from
each stage without a round-trip through `any` (which loses integer
precision and map key ordering). The executor marshals `PipeValue` to
`string` before passing to runners and unmarshals the runner's output
back to `PipeValue` after each stage.

**Pipe structure:**

```json
{
  "data": {"title": "Q3 Report Summary", "summary": "..."},
  "files": [
    {
      "name": "02-report.pdf",
      "path": "02-report.pdf",
      "mime_type": "application/pdf",
      "size": 245000,
      "origin": "attachment"
    },
    {
      "name": "chart.png",
      "path": "chart.png",
      "mime_type": "image/png",
      "size": 82000,
      "origin": "stage-1"
    }
  ]
}
```

The `origin` field tracks where each file came from: `"attachment"` for
email attachments, `"stage-N"` for files produced by pipeline stages.
This is metadata for audit — commands don't use it for routing.

**File paths are relative** to the pipeline directory. Relative paths
simplify validation (reject paths containing `..` or starting with
`/`) and are portable across HOME changes.

**Path resolution round-trip:** Before calling `runner.Run()`, the
executor resolves all `files[].path` values to absolute paths by
joining with the pipeline directory. Runners receive absolute paths
so they can read/write files directly. After the runner returns (for
process-mode stages with `files: true`), the executor strips the
pipeline directory prefix from each `files[].path`, converting back
to relative before storing in the updated `PipeValue`. This prevents
a command that echoes its input `files` array from double-resolving
absolute paths on the next stage.

**Pipeline file directory:** Each pipeline gets a scratch directory at
`~/.punt-labs/beadle/pipelines/<pipeline-id>/`. The executor creates it
before stage 0 and pre-downloads email attachments into it. Stages
write generated files here. The executor removes the directory after
pipeline completion (success or failure), with a configurable retention
period for debugging (default: 0, immediate cleanup).

**Orphan cleanup on startup:** The daemon scans `pipelines/` on
startup. The directory contains two types of entries: `<id>.json`
state files (managed by `PipelineStore`) and `<id>/` file directories
(managed by `Executor.Run`). The startup scan walks subdirectories
(not JSON files), checks each against the retention window, and
removes directories older than the window. This handles the crash case
where a pipeline directory was never cleaned up. `PipelineStore`
continues to manage only JSON state files — file directory creation
and cleanup are `Executor` responsibilities.

**Attachment fetch boundary:** The `MailHandler` already has IMAP
access and parses MIME via `ParseMIME`. A new `AttachmentFetcher`
interface is injected into `Executor`, consistent with how `Spawner`
is injected:

```go
type AttachmentFetcher interface {
    Fetch(ctx context.Context, messageID string, dir string) ([]PipeFile, error)
}
```

The `MailHandler` satisfies this interface. It fetches the full message
content, extracts MIME parts, and writes attachment bytes to the
pipeline directory. Returns `[]PipeFile` with name, relative path,
MIME type, and size. A nil `Fetcher` on the `Executor` is a hard
startup error — silent degradation hides configuration mistakes.

**Duplicate filename handling:** Attachments are prefixed with their
MIME part index: `02-report.pdf`, `05-data.csv`. This prevents
collision when an email has multiple attachments with the same name.
Stage-generated files use their original names (collision is the
command author's responsibility within their own stage).

**Command `files` declaration:** Commands declare file handling intent
via a `files` field in the command YAML:

```yaml
# Command that reads and echoes files
files: true

# Command that intentionally drops files (default)
files: false
```

- `files: true` — the command's `output_schema` must be a JSON Schema
  object (not `"text"`). `validateCommand` enforces this at load time.
  At runtime, the command is expected to include a `files` array in its
  output. If the output omits `files`, the executor warns and treats it
  as empty (files dropped).
- `files: false` (default) — the command does not handle files. For
  process-mode commands, files are dropped after this stage. The
  executor does not warn — the drop is intentional.

This makes file handling explicit at the command level, not just in
the output. A pipeline author sees `files: true/false` in the YAML
and knows whether files survive each stage.

**Process/passthrough for files:**

- `mode: process, files: true` — the command's output replaces both
  `data` and `files`.
- `mode: process, files: false` — the command's output replaces `data`.
  Files are dropped (empty array).
- `mode: passthrough` — both `data` and `files` pass through unchanged.
  `files: true` is invalid on passthrough commands — `validateCommand`
  rejects the combination. A passthrough command's output is ignored,
  so declaring file handling intent is meaningless and misleading.

**Reply stage:** The reply command has `files: true` and reads the
`files` array to attach files to the outbound email. Reply is always
the terminal stage — the executor enforces this in two ways:

1. **Strip from planner output.** Before executing the command loop,
   the executor removes any `reply` command from the planner's
   `[]CommandCall`. If the planner returns `[summarize, reply, wall]`,
   the executor runs `[summarize, wall]` then auto-appends reply.
2. **Auto-append after loop.** The executor appends a single reply
   stage after all planner stages complete. This is the only way
   reply enters the pipeline.

**How stages interact with files:**

- **Claude runner:** The pipe JSON (including `files` with resolved
  absolute paths) is passed via `inputs.pipeline_output` in the
  mission contract. The worker reads files by absolute path. To add a
  file, the worker writes it to the pipeline directory (path provided
  in the contract) and includes the relative path in its output.
- **CLI runner:** The pipe JSON is provided on stdin. CLI tools use
  `jq` to extract file paths (absolute, resolved by executor before
  passing). Generated files are written to the pipeline directory.

**Initial pipe with attachments:**

```json
{
  "data": {
    "message_id": "1829",
    "from": "jim@punt-labs.com",
    "subject": "summarize the attached report",
    "trust_level": "trusted"
  },
  "files": [
    {
      "name": "02-q3-report.pdf",
      "path": "02-q3-report.pdf",
      "mime_type": "application/pdf",
      "size": 245000,
      "origin": "attachment"
    }
  ]
}
```

**Security considerations:**

- **File path validation.** After each process-mode stage with
  `files: true`, the executor validates all `files[].path` values:
  must not contain `..`, must not start with `/`, must not be empty.
  The executor resolves relative paths against the pipeline directory
  and verifies the resolved file exists. Violations enter the else
  clause.
- **File size cap.** Individual files are capped at 25 MB (matching
  the `send_email` attachment limit). Total pipeline directory size
  capped at 100 MB. **Enforcement timing:** checked at pre-download
  (before stage 0) and after each process-mode stage that produces
  files (`files: true`). The executor sums all files in the directory.
  Exceeding either cap enters the else clause.
- **Cleanup.** Two complementary mechanisms: (1) the executor removes
  the pipeline directory immediately after pipeline completion (success
  or failure), and (2) the daemon startup scan removes orphaned
  directories older than the retention window (handling the crash case
  where mechanism 1 never ran). Files never persist beyond the
  retention window.
- **Passthrough isolation.** Passthrough-mode commands can read files
  by path but cannot modify the `files` array. A passthrough command
  that writes to the pipeline directory does not affect the pipe — the
  files it writes are orphaned and cleaned up with the directory.

**Example pipeline:**

```text
Email: "summarize the attached report" + q3-report.pdf

Initial pipe:
  data: {message_id, from, subject, trust_level}
  files: [{name: "02-q3-report.pdf", path: "02-q3-report.pdf", origin: "attachment"}]

Stage 0: summarize (claude, process, files: true)
  → reads q3-report.pdf by absolute path, produces JSON summary
  → output: {data: {title, summary}, files: [{02-q3-report.pdf}]}
  → pipe: data replaced, files echoed

Stage 1: generate-chart (cli, process, files: true)
  → reads summary data, runs charting tool, writes chart.png
  → output: {data: {title, summary}, files: [{02-q3-report.pdf}, {chart.png}]}
  → pipe: data + files both updated

Stage 2: notify (cli, passthrough)
  → reads data, broadcasts headline via biff
  → pipe unchanged (data + files preserved)

Stage 3: reply (claude, process, files: true)
  → reads data, attaches 02-q3-report.pdf + chart.png to email reply
  → sends reply with 2 attachments to jim@punt-labs.com
```

**Implementation changes:**

1. **Pipe shape.** The pipe JSON string changes from a flat value to
   `{"data": ..., "files": [...]}`. Runner interface unchanged (`pipe
   string`). Executor parses/serializes internally via a `PipeValue`
   struct.
2. **AttachmentFetcher interface.** New interface injected into
   Executor. MailHandler satisfies it. Fetches MIME parts, writes to
   pipeline directory, returns `[]PipeFile`.
3. **Pipeline directory.** `PipelineStore` creates
   `~/.punt-labs/beadle/pipelines/<id>/` at pipeline start. Cleanup
   after completion. Orphan scan on daemon startup.
4. **Command `files` field.** New boolean field on `Command` struct,
   validated in `validateCommand`. Default `false`.
5. **File path validation.** After each process-mode stage with
   `files: true`, validate relative paths (no `..`, no `/` prefix),
   verify files exist on disk.
6. **Directory size enforcement.** Sum files after pre-download and
   after each `files: true` stage. Exceeds 100 MB → else clause.
7. **Duplicate filename prefix.** Pre-download prefixes attachments
   with MIME part index: `02-report.pdf`.
8. **Reply terminal enforcement.** Executor strips `reply` from
   planner output and auto-appends it as the terminal stage.
9. **Reply wiring.** The reply command's prompt includes instructions
   to attach files from the pipe to the outbound email.

**Rejected alternatives:**

- **Files as base64 in the pipe JSON.** Bloats the pipe by 33%. A
  245KB PDF becomes 327KB of base64 in every stage's stdin. File
  paths are cheaper and CLI tools can read files directly.
- **Implicit file passthrough.** If a process-mode command doesn't
  mention files, carry them forward automatically. This hides data
  flow — a command that forgot to include files silently passes
  sensitive documents to downstream stages. Explicit is safer.
- **Separate file channel.** Files flow on a different path than data.
  Adds complexity without benefit — the pipe is already structured
  JSON. A `files` field in the same object is simpler.
- **Attachments only (no generated files).** Too narrow. A charting
  command, a PDF-to-text converter, a code compiler all produce files.
  The model must be general.
- **Runner interface change to PipeValue struct.** Breaking change to
  Runner signature, all mocks, persistence layer. Keeping `pipe string`
  with `{data, files}` embedded as JSON avoids the migration.
- **Absolute paths in the files array.** Fragile across HOME changes.
  Relative paths within the pipeline directory are simpler to validate
  and portable.
