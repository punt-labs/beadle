# CLI / MCP / Slash Command Gap Analysis

Analysis of beadle-email's three tool surfaces as of v0.17.0 (2026-11-07).
Re-baselined from the v0.7.0 (2026-03-23) snapshot: the message `mark`,
`reply`, and `search` operations, the two `batch_*` MCP tools, the poll-status
pair, and the `enable` MCP tool all postdate the old baseline, and this pass
adds a noun-verb naming assessment against
[punt-kit/standards/cli.md](https://github.com/punt-labs/punt-kit/blob/main/standards/cli.md)
§ Subcommand naming.

## Capability Matrix

| Capability | MCP Tool | CLI Command | Slash Command | Notes |
|-----------|----------|-------------|---------------|-------|
| List messages | `list_messages` | `list` | `/inbox` | MCP: filters + unread. CLI: folder/count flags. Slash: triage workflow |
| Read message | `read_message` | `read <uid>` | `/inbox` | Slash reads as part of triage |
| Send email | `send_email` | `send` | `/mail`, `/send` | All support to/cc/bcc/subject/body/attachments |
| Reply to message | `reply_message` | `reply <uid>` | — | Added since v0.7.0 |
| Search messages | `search_messages` | `search` | — | Added since v0.7.0 |
| Mark read/unread | `mark_message` | `mark` | — | Added since v0.7.0 |
| Mark many read/unread | `batch_mark_messages` | — | — | MCP only; batch variant |
| Move message | `move_message` | `move <uid>` | `/inbox` | Slash auto-archives per permission |
| Move many messages | `batch_move_messages` | — | — | MCP only; batch variant |
| List folders | `list_folders` | `folders` | — | No slash command |
| Verify signature | `verify_signature` | — | — | MCP only |
| Show MIME | `show_mime` | — | — | MCP only |
| Check trust | `check_trust` | — | `/inbox` | Slash uses internally for permission enforcement |
| Download attachment | `download_attachment` | — | — | MCP only |
| List contacts | `list_contacts` | `contact list` | `/contacts` | |
| Find contact | `find_contact` | `contact find` | `/contacts` | |
| Add contact | `add_contact` | `contact add` | `/contacts` | |
| Remove contact | `remove_contact` | `contact remove` | `/contacts` | |
| Show identity | `whoami` | `identity show` | — | Different output (see gap #5) |
| Switch identity | `switch_identity` | — | — | MCP only, session-scoped |
| Set repo identity | — | `identity set` | — | CLI only, persistent config |
| Poll status | `get_poll_status` | — | — | MCP only; present only when polling is configured |
| Set poll interval | `set_poll_interval` | — | — | MCP only; present only when polling is configured |
| Enable in repo | `enable` | `enable` | — | Deposits guide + marker; MCP tool mirrors the CLI verb |
| Disable in repo | — | `disable` | — | CLI only |
| Health check | — | `doctor` | — | CLI only |
| WebSocket health | — | `health` | — | CLI only |
| Status summary | — | `status` | — | CLI only |
| Version | — | `version` | — | CLI only |
| Start MCP server | — | `serve` | — | CLI only |
| Install/uninstall | — | `install` / `uninstall` | — | CLI only |

## Noun-verb naming assessment

[punt-kit/standards/cli.md](https://github.com/punt-labs/punt-kit/blob/main/standards/cli.md)
§ Subcommand naming: a tool that acts on **more than one kind of noun** uses
`noun verb` (`lux scene show`, `bd issue list`); only a genuinely single-noun
tool uses flat verbs (`quarry search`, `biff who`). beadle-email acts on
several nouns — **message, folder, contact, identity** — so the noun-first form
is the one the standard prescribes.

The surface is **split**: the contact and identity nouns already group
correctly, but the message and folder operations are flat top-level verbs.

| Area | Current | Standard-conformant | Status |
|---|---|---|---|
| Contacts | `contact add\|find\|list\|remove` | same | ✓ conformant |
| Identity | `identity show\|set` | same | ✓ conformant |
| Messages | `list`, `read`, `send`, `reply`, `search`, `mark`, `move` | `message list\|read\|send\|reply\|search\|mark\|move` | ✗ flat; should be noun-first |
| Folders | `folders` | `folder list` | ✗ flat, and plural-noun-as-verb |

The message verbs are the bulk of the product surface and the clearest
deviation: seven flat verbs that all act on the `message` noun. Under the
standard they belong under a `message` group, which also future-proofs the
surface — a new message operation becomes a new verb under `message`, not a new
top-level command competing with `doctor`/`serve`/`install`.

This is a **breaking CLI change** (every `beadle-email list` becomes
`beadle-email message list`), so it is a deliberate, versioned decision, not a
drive-by rename — see recommendation #1. The admin/diagnostic verbs
(`doctor`, `status`, `health`, `version`, `serve`, `install`, `uninstall`,
`enable`, `disable`) are correctly flat: cli.md § Layer 2 exempts the admin
tier from noun-first, as they name a machine-level action, not a domain noun.

## Intentional gaps (by design)

| Gap | Why it's fine |
|-----|--------------|
| `doctor`, `status`, `health`, `version`, `serve` CLI-only | Admin/diagnostic — terminal tools, not conversational |
| `identity set` CLI-only | Writes persistent config — setup step, not session action |
| `/inbox` has no single CLI equivalent | `/inbox` is a workflow (read → triage → archive → reply); CLI has the primitives |
| `/mail` and `/send` no CLI parity concern | Conversational wrappers over the `send` primitive |
| `install`/`uninstall`/`enable`/`disable` CLI-only (enable also MCP) | Setup operations, not normal-operation actions |
| `batch_move_messages`, `batch_mark_messages` MCP only | Batch variants for the conversational surface; CLI scripts loop the singular verb |
| `get_poll_status`, `set_poll_interval` MCP only, conditional | Appear only when polling is configured for the identity |

## Questionable gaps

### 1. `download_attachment` — MCP only

CLI users can `list` and `read` but cannot save attachments. A real gap —
attachment download is a common workflow.
**Verdict:** worth adding, as `beadle-email message download <uid> --part <index>`
(noun-first, per the assessment above).

### 2. `whoami` vs `identity show` — different output

`whoami` (MCP) shows email, source, handle, name, contacts path + count,
override status, session participants. `identity show` (CLI) shows email,
handle, source, contacts path. Missing on the CLI side: contacts count,
override indicator, session participants.
**Verdict:** `identity show` should converge with `whoami` — same data, two surfaces.

### 3. `switch_identity` — MCP only, no slash command

No CLI switch (session-scoped, in-memory, by design) and no slash command. A
`/identity` slash wrapping `whoami` + `switch_identity` is the natural
user-facing entry point.
**Verdict:** add `/identity`. Users currently must know the MCP tool name to switch.

### 4. `verify_signature`, `show_mime`, `check_trust` — MCP only

Diagnostic/inspection tools. `doctor` covers the GPG setup side; per-message
trust is visible in `list` output.
**Verdict:** acceptable as MCP-only; add a CLI verb only on request.

### 5. `list_folders` — no slash command

Folders rarely change and aren't part of typical workflows.
**Verdict:** acceptable gap.

## Overlap concerns

### `/mail` vs `/send`

`/send` is described as "any channel (email today, more later)" but today only
emails — identical to `/mail`. Keep both: `/mail` is explicit, `/send` is the
forward-looking intent command that gains value when a second channel ships.
**Verdict:** keep both; revisit when a non-email channel lands.

### CLI flags vs MCP param names

CLI `--unread` / `--count` vs MCP `unread_only` / `count`. Different surface
conventions (short flags vs descriptive params).
**Verdict:** acceptable.

## Recommendations

1. **Decide the noun-verb migration for the message and folder surface.** The
   standard prescribes `message list`/`message read`/…/`folder list`; the CLI
   ships flat verbs. This is a breaking change and the single largest
   conformance gap — it needs an operator ruling (migrate with a deprecation
   window, or document a standing exception), not a silent rename.
2. **Converge `identity show` with `whoami`** — add contacts count and session
   participants to the CLI output.
3. **Add a `/identity` slash command** — wraps `whoami` + `switch_identity`.
4. **Add attachment download to the CLI** — `message download <uid> --part <index>`.
5. **Keep `/mail` and `/send` separate** — revisit when a second channel ships.
6. **Leave `verify_signature`, `show_mime`, `check_trust`, the `batch_*` tools,
   and the poll pair as MCP-only** — diagnostic or batch/conversational tools
   that do not need CLI or slash wrappers.
