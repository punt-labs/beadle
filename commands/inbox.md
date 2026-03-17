---
description: "Check beadle's email inbox"
argument-hint: "[filter | 5m | 10m | 15m | 30m | 1h | 2h | n | status]"
allowed-tools: ["mcp__plugin_beadle_email__list_messages", "mcp__plugin_beadle_email__read_message", "mcp__plugin_beadle_email__move_message", "mcp__plugin_beadle_email__check_trust", "mcp__plugin_beadle-dev_email__list_messages", "mcp__plugin_beadle-dev_email__read_message", "mcp__plugin_beadle-dev_email__move_message", "mcp__plugin_beadle-dev_email__check_trust", "Write", "Read", "CronCreate", "CronDelete", "CronList"]
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

1. Write `.claude/beadle.local.md` with the new interval:

   ```markdown
   ---
   inbox_poll: <interval>
   ---
   ```

2. Cancel any existing beadle inbox cron by calling `CronList`, finding jobs with
   prompt containing `/inbox`, and calling `CronDelete` on them.
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

1. Write `.claude/beadle.local.md` with the disabled config:

   ```markdown
   ---
   inbox_poll: n
   ---
   ```

2. Cancel any existing beadle inbox cron (CronList + CronDelete).
3. Confirm: "Inbox polling disabled."

### Show status (`status`)

1. Read `.claude/beadle.local.md`. If it doesn't exist, report "30m (default)".
2. Call `CronList` to check if a polling cron is active.
3. Report: current config value, whether a cron is active.

### No argument

1. Call `list_messages` with `unread_only: true`.
2. If there are unread messages, summarize them in a compact table: sender, subject, trust level.
3. If no unread messages, call `list_messages` without `unread_only` to show recent messages.
4. For any messages from the owner, offer to read them.

### With argument (filter)

The argument is a natural language filter. Examples:

- `/inbox check for anything from jim` — filter by sender
- `/inbox unread` — show only unread
- `/inbox about the deploy` — filter by subject

Use the filter to decide which messages to list and/or read. Call `list_messages` first, then `read_message` for relevant matches.

### After reading

If the user asks to archive messages after reading, use `move_message` to move them to Archive.
