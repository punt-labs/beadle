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

**Workaround:** Amazon SES `SendRawEmail` preserves raw MIME. Tracked in
beadle-atz. Requires SPF/DKIM DNS changes for punt-labs.com.

## DES-006: Resend does not support raw MIME

**Decision:** Resend API cannot be used for PGP-signed outbound mail.

**Evidence:** Tested 2026-03-13. Resend's POST /emails endpoint only accepts
structured fields (from, to, subject, text, html). There is no `raw` field.
The API docs confirm no raw MIME support.

**Impact:** Resend is fallback-only for unsigned plain text delivery.

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
  `jim@punt-labs.com`, `builds@punt-labs.com`, etc.

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
| Jim Freeman | `rwx` | Full authority — read, reply, execute tasks |
| Eric | `rw-` | Read and reply, but not execute instructions |
| Vendor X | `r--` | Read only, surface to owner for action |
| Unknown sender | `---` | Default: no permissions (whitelist) |

**Orthogonal to transport trust:** Transport trust (trusted/verified/untrusted/
unverified from DES-001) answers "is this message authentic?" Identity trust
answers "given it's authentic, what should beadle do?" Both must pass: an
unverified message from an `rwx` contact should NOT be executed (identity claim
not verified). An authenticated message from an `r--` contact should NOT trigger
autonomous action (sender lacks authority).

**No inheritance between identities.** Jim may grant Eric `rwx` on
`jim@punt-labs.com` but only `rw-` on `claude@punt-labs.com`. Each cell in the
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
    jim@punt-labs.com/
      email.json
      contacts.json
      attachments/
  default-identity        # file: default email address
```

**Filesystem layout (both systems):**

```text
~/.punt-labs/
├── ethos/                              ← ethos owns this tree
│   ├── active                          ← global active handle ("jfreeman")
│   ├── sessions/                       ← session roster data
│   └── identities/
│       ├── jfreeman.yaml               ← Jim's persona (kind: human)
│       ├── jfreeman.ext/               ← extensions for Jim
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

**Why repo-local config exists:** The global `active` file may say `jfreeman`
(Jim is the active human). But in the beadle repo, Claude is the agent that
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
in ethos. Ownership and authority relationships (e.g., "Jim owns Claude") are
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

## DES-015: Server-side inbox poller, not CronCreate

**Status:** SETTLED (PR #84, 2026-04-01)

**Decision:** Inbox polling runs as a background goroutine inside the beadle-email
MCP server, not as a CronCreate job managed by the model.

**Problem:** CronCreate jobs are session-scoped — they die when the session ends.
The original design used a SessionStart hook to emit "Execute: CronCreate..." in
`additionalContext`, expecting the model to re-register the cron. Tested
2026-04-01: the model ignores these instructions. CLAUDE.md instructions (like
biff's `/loop 2m /biff:read`) are also unreliable across sessions. There is no
reliable mechanism to make the model call a tool at session start.

**Design:** The MCP server owns the full polling lifecycle:

1. `email.json` stores `poll_interval` (valid: 5m, 10m, 15m, 30m, 1h, 2h, n)
2. On startup, the server reads the config and starts a background goroutine
3. The goroutine calls IMAP STATUS on the configured interval
4. When unread count increases, it fires `tools/list_changed` (MCP notification)
5. Claude Code sees the notification and re-lists tools, surfacing new mail
6. MCP tools `set_poll_interval` and `get_poll_status` manage the config

**Pattern:** Same as biff's notification system (`biff/docs/notification.tex`).
Background poller detects changes, fires `tools/list_changed` from the server's
own goroutine context. Two notification paths: "belt" (inside tool handler) and
"suspenders" (background poller with captured session reference).

**Key properties:**

- Polling survives session restarts — server reads config on startup
- No model cooperation needed — server pushes notifications autonomously
- First poll runs immediately, subsequent polls on the configured interval
- First poll suppresses notification (avoids false positive on existing unread)
- Failure tracking: consecutive failure count and last error in status output
- Atomic config writes (temp + rename) prevent corruption
- Config fallback: identity-scoped path, then default path, only on `ErrNotExist`

**Rejected alternatives:**

- **SessionStart hook with "Execute:" instruction** — Model ignores
  `additionalContext` instructions. Tested and verified broken.
- **CLAUDE.md instruction** — Model doesn't reliably act on CLAUDE.md
  instructions at session start. Biff's `/loop` instruction fails regularly.
- **SessionStart hook with `type: "prompt"`** — SessionStart hooks only support
  `type: "command"`. No prompt injection at session start.
- **CronCreate with poll-reminder fallback** — The UserPromptSubmit fallback
  adds overhead to every user prompt and still depends on the model acting.

**Future:** When Anthropic's channels feature ships, upgrade from
`tools/list_changed` to `notifications/claude/channel` for direct conversation
injection. See `channels-architecture.tex`.

## DES-016: Contact matching by email domain pattern

**Status:** PROPOSED (beadle-a7v)

**Decision:** Add glob/domain-pattern matching to the contact system so a single
contact entry can cover all messages from a domain (e.g., `*@mail.anthropic.com`).

**Problem:** Anthropic uses per-message randomized sender addresses
(`no-reply-<random>@mail.anthropic.com`). The current contact system matches by
exact email address, so each new Anthropic message arrives from an unknown address
and gets blocked as `---`. Adding individual addresses doesn't scale — new ones
arrive with every message.

**Not yet implemented.** Tracked in beadle-a7v.
