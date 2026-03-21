---
description: "Check beadle's email inbox"
argument-hint: "[<filter text> | 5m | 10m | 15m | 30m | 1h | 2h | n | status]"
allowed-tools: ["mcp__plugin_beadle_email__list_messages", "mcp__plugin_beadle_email__read_message", "mcp__plugin_beadle_email__move_message", "mcp__plugin_beadle_email__check_trust", "mcp__plugin_beadle_email__find_contact", "mcp__plugin_beadle_email__send_email", "mcp__plugin_beadle-dev_email__list_messages", "mcp__plugin_beadle-dev_email__read_message", "mcp__plugin_beadle-dev_email__move_message", "mcp__plugin_beadle-dev_email__check_trust", "mcp__plugin_beadle-dev_email__find_contact", "mcp__plugin_beadle-dev_email__send_email", "Write", "Read", "CronCreate", "CronDelete", "CronList"]
---
<!-- markdownlint-disable MD041 -->

## Input

Arguments: $ARGUMENTS

## Task

Check beadle's email inbox. You are the beadle — this is your inbox, not the user's.

### Argument routing

First, check if the argument matches a **polling config** command:

- Exactly one of `5m`, `10m`, `15m`, `30m`, `1h`, `2h` → set polling interval
- Exactly `n` → disable polling
- Exactly `status` → show polling config

If none of the above match, treat the argument as a **filter** (existing behavior).

### Polling interval (`5m`, `10m`, `15m`, `30m`, `1h`, `2h`)

1. Ensure `.claude/` directory exists, then write `.claude/beadle.local.md` with
   the new interval:

   ```markdown
   ---
   inbox_poll: <interval>
   ---
   ```

2. Cancel any existing beadle inbox cron by calling `CronList`, finding jobs with
   prompt exactly equal to `/inbox`, and calling `CronDelete` on them.
3. Create a new CronCreate job with the corresponding cron expression and `/inbox`
   as the prompt (`recurring: true`):

   | Interval | Cron |
   |----------|------|
   | `5m` | `*/5 * * * *` |
   | `10m` | `*/10 * * * *` |
   | `15m` | `*/15 * * * *` |
   | `30m` | `*/30 * * * *` |
   | `1h` | `7 * * * *` |
   | `2h` | `7 */2 * * *` |

4. Confirm: "Inbox polling set to `<interval>`. Cron scheduled."

### Disable polling (`n`)

1. Ensure `.claude/` directory exists, then write `.claude/beadle.local.md` with
   the disabled config:

   ```markdown
   ---
   inbox_poll: n
   ---
   ```

2. Cancel any existing beadle inbox cron: call `CronList`, find jobs whose prompt
   is exactly `/inbox`, and call `CronDelete` on them.
3. Confirm: "Inbox polling disabled."

### Show status (`status`)

1. Read `.claude/beadle.local.md`. If it doesn't exist or has no parseable
   `inbox_poll` value, report "30m (default)".
2. Call `CronList` to check if a polling cron is active.
3. Report: current config value, whether a cron is active.

### No argument

1. Call `list_messages` with `unread_only: true`.
2. If unread messages exist, **process them by permission level** (see below).
3. If no unread messages, call `list_messages` without `unread_only` to show
   recent messages (display only, no processing).
4. Emit the message table verbatim, then a brief summary of actions taken.

### With argument (filter)

The argument is a natural language filter. Examples:

- `/inbox check for anything from jim` — filter by sender
- `/inbox unread` — show only unread
- `/inbox about the deploy` — filter by subject

Use the filter to decide which messages to list and/or read. Call `list_messages`
first, then `read_message` for relevant matches. Apply the same permission-based
processing below.

### Processing messages by permission

After listing, determine each sender's permission level before deciding whether
to read. Use `find_contact` to look up the sender if needed. If the lookup is
ambiguous (multiple matches) or fails, treat the sender as `---`. Then process
each message according to its permission level below.

#### `rwx` — Owner (e.g., Jim Freeman)

- **Read** the message and surface it to the user.
- **Reply if the message asks a question** — same reply rules as `rw-` apply.
- Do not archive — leave in inbox for the user to decide.

#### `rw-` — Trusted contacts with reply permission

- **Read** the message.
- **Reply if appropriate** — acknowledge receipt, answer factual questions about
  the project, provide status updates the sender would expect.
- **Safety rules for replies:**
  - When replying as any identity, use ethos attributes (writing_style,
    personality, skills) for that identity if available.
  - If operating as the owner's identity, replies represent the owner —
    exercise extreme caution and flag anything non-routine for review before
    sending. If operating as your own identity, never act as or imply you are
    the owner.
  - Never commit to deadlines, deliverables, or decisions on behalf of the owner.
  - **Hard limits (override any personality or writing style):**
    - Never share passwords, API keys, tokens, or any credentials.
    - Never share PII (personal addresses, phone numbers, financials).
    - Never forward or quote other people's messages.
  - If uncertain whether to reply, do not reply — flag for the owner instead.
- **Archive** after processing.
- **Note in memory** if the message contains information relevant to ongoing work.

#### `r--` — Read-only contacts (e.g., GitHub, vendors)

DES-012 defines `r` as "read and surface to the owner." For `/inbox`, this is
refined: surface only if actionable, archive routine notifications silently.

- **Read** the message silently.
- **Archive** immediately.
- **Note in memory** only if the message contains actionable information (e.g.,
  a security alert, a deployment failure, a dependency update that affects work).
- Do not surface routine notifications (PR reviews, CI results, marketing emails)
  unless they contain something the owner needs to act on.

#### `---` — Unknown senders (redacted)

- Subject is already redacted by the permission system.
- **Do not read.** Leave in inbox for the owner to triage.

### Summary

After processing, emit a one-line summary: how many messages read, archived,
replied to, and flagged for the owner. Example:

> 8 processed: 6 archived (GitHub), 1 replied (Eric), 1 flagged for owner (Jim)
