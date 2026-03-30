# CLI / MCP / Slash Command Gap Analysis

Analysis of beadle-email's three tool surfaces as of v0.7.0 (2026-03-23).

## Capability Matrix

| Capability | MCP Tool | CLI Command | Slash Command | Notes |
|-----------|----------|-------------|---------------|-------|
| List messages | `list_messages` | `list` | `/inbox` | MCP: filters + unread. CLI: folder/count flags. Slash: triage workflow |
| Read message | `read_message` | `read <uid>` | `/inbox` | Slash command reads as part of triage |
| Send email | `send_email` | `send` | `/mail`, `/send` | All support to/cc/bcc/subject/body/attachments |
| List folders | `list_folders` | `folders` | — | No slash command |
| Move message | `move_message` | `move <uid>` | `/inbox` | Slash command auto-archives per permission |
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
| Health check | — | `doctor` | — | CLI only |
| Status summary | — | `status` | — | CLI only |
| Version | — | `version` | — | CLI only |
| Start MCP server | — | `serve` | — | CLI only |
| Install/uninstall | — | `install` / `uninstall` | — | CLI only |

## Intentional Gaps (by design)

| Gap | Why it's fine |
|-----|--------------|
| `doctor`, `status`, `version`, `serve` CLI-only | Admin/diagnostic — terminal tools, not conversational |
| `identity set` CLI-only | Writes persistent config — setup step, not session action |
| `/inbox` has no CLI equivalent | `/inbox` is a workflow (read → triage → archive → reply), not a single operation. CLI has the primitives |
| `/mail` and `/send` no CLI parity concern | Conversational wrappers composing MCP primitives. CLI `send` covers the raw operation |
| `install`/`uninstall` CLI-only | One-time setup, not used during normal operation |

## Questionable Gaps

### 1. `verify_signature` — MCP only

No CLI `verify <uid>` command. Low usage — MCP is the primary surface. But `doctor` already validates GPG, so a CLI verify would be consistent with the diagnostic pattern.

**Verdict:** Low priority. Add if CLI users request it.

### 2. `show_mime` — MCP only

No CLI MIME inspection. This is a debugging/diagnostic tool. MCP-only is fine — CLI users can pipe `read` output to external tools.

**Verdict:** Acceptable gap.

### 3. `download_attachment` — MCP only

CLI users can `list` and `read` messages but cannot save attachments. This is a real gap — downloading attachments is a common workflow.

**Verdict:** Worth adding as `beadle-email download <uid> --part <index>`.

### 4. `check_trust` — no CLI

`/inbox` uses it internally, MCP exposes it. CLI users can't classify trust from terminal. Useful for scripting and debugging trust issues.

**Verdict:** Low priority. `doctor` covers the setup side; per-message trust is visible in `list` output.

### 5. `whoami` vs `identity show` — different output

`whoami` (MCP) shows: email, source, handle, name, contacts path + count, override status, session participants. `identity show` (CLI) shows: email, handle, source, contacts path. Missing: contacts count, override indicator, session participants.

**Verdict:** `identity show` should converge with `whoami` output. Same data, different surface.

### 6. `switch_identity` — MCP only, no slash command

No CLI switch (by design — switch is session-scoped in-memory). No slash command either. A `/identity` or `/whoami` command wrapping `whoami` + `switch_identity` would be the natural user-facing entry point.

**Verdict:** Add `/identity` slash command. Users currently must know the MCP tool name to switch.

### 7. `list_folders` — no slash command

No `/folders`. Minor — folders rarely change and aren't part of typical workflows.

**Verdict:** Acceptable gap.

## Overlap Concerns

### `/mail` vs `/send`

`/send` is described as "any channel (email today, more later)" but today only does email — identical to `/mail`. Two options:

- **Keep both:** `/mail` is explicit ("I'm emailing"), `/send` is intent-based ("send this, you pick the channel"). When Signal/SMS ships, `/send` gains value.
- **Deprecate `/mail`:** `/send` subsumes it. One command, less confusion.

**Verdict:** Keep both for now. `/send` is the forward-looking command; `/mail` is the explicit one. Revisit when a second channel ships.

### CLI `list` vs MCP `list_messages` parameter names

CLI uses `--unread`, MCP uses `unread_only`. CLI uses `--count`, MCP uses `count`. Minor inconsistency — different surfaces have different conventions (flags vs params).

**Verdict:** Acceptable. CLI flags are short (`--unread`); MCP params are descriptive (`unread_only`).

## Missing Slash Commands Worth Adding

| Command | What it would do | Priority |
|---------|-----------------|----------|
| `/identity` | Show identity (`whoami`) + switch (`switch_identity`) | P2 — natural entry point for identity switching |
| `/attachments` | Download attachment from a message | P3 — common workflow |

## Recommendations

1. **Converge `identity show` with `whoami`** — add contacts count and session participants to CLI output
2. **Add `/identity` slash command** — wraps `whoami` + `switch_identity` for user-facing identity management
3. **Add CLI `download` subcommand** — `beadle-email download <uid> --part <index>` for attachment extraction
4. **Keep `/mail` and `/send` separate** — revisit when second channel ships
5. **Leave `verify_signature`, `show_mime`, `check_trust` as MCP-only** — diagnostic tools that don't need CLI/slash wrappers
