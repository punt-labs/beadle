---
description: "Check beadle's email inbox"
argument-hint: "[<filter text> | 5m | 10m | 15m | 30m | 1h | 2h | n | status]"
allowed-tools: ["mcp__plugin_beadle_email__list_messages", "mcp__plugin_beadle_email__read_message", "mcp__plugin_beadle_email__move_message", "mcp__plugin_beadle_email__check_trust", "mcp__plugin_beadle_email__find_contact", "mcp__plugin_beadle_email__send_email", "mcp__plugin_beadle_email__set_poll_interval", "mcp__plugin_beadle_email__get_poll_status", "mcp__plugin_beadle-dev_email__list_messages", "mcp__plugin_beadle-dev_email__read_message", "mcp__plugin_beadle-dev_email__move_message", "mcp__plugin_beadle-dev_email__check_trust", "mcp__plugin_beadle-dev_email__find_contact", "mcp__plugin_beadle-dev_email__send_email", "mcp__plugin_beadle-dev_email__set_poll_interval", "mcp__plugin_beadle-dev_email__get_poll_status", "Read"]
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

1. Call `set_poll_interval` with the interval value.
2. Confirm with the tool's response text.

### Disable polling (`n`)

1. Call `set_poll_interval` with `interval: "n"`.
2. Confirm with the tool's response text.

### Show status (`status`)

1. Call `get_poll_status`.
2. Report the returned values: interval, active, last check time, unseen count.

### No argument

1. Initialize an empty set of **processed message IDs**.
2. Call `list_messages` with `unread_only: true` and `count: 50`. **Emit the
   returned table immediately** — always show it to the user before any
   processing begins.
3. If no unread messages, call `list_messages` without `unread_only` to show
   recent messages and emit that table. Stop — nothing to process.
4. From the returned batch, identify messages whose IDs are **not** in the
   processed set. If every ID in the batch is already processed, stop — there
   are no new messages to handle.
5. **Process only the new messages** by permission level (see below). After
   processing each message (read, archive, reply, etc.), add its ID to the
   processed set.
6. Call `list_messages` again with `unread_only: true` and `count: 50`. Repeat
   from step 4. Messages that were moved/archived will no longer appear;
   messages that remain unread (e.g., unknown-sender messages left in inbox)
   will be skipped because their IDs are already in the processed set.
7. Emit a single summary at the end covering all batches.

### With argument (filter)

The argument is a natural language filter. Examples:

- `/inbox check for anything from alice` — filter by sender
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

#### `rwx` — Owner (e.g., the repo owner)

- **Read** the message and surface it to the user.
- **Reply if the message asks a question.** Same reply rules as `rw-` apply.
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

> 8 processed: 6 archived (notification senders), 1 replied (trusted contact), 1 flagged for owner
