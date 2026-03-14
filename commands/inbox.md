---
description: "Check beadle's email inbox"
argument-hint: "[filter]"
allowed-tools: ["mcp__plugin_beadle_email__list_messages", "mcp__plugin_beadle_email__read_message", "mcp__plugin_beadle_email__move_message", "mcp__plugin_beadle_email__check_trust", "mcp__plugin_beadle-dev_email__list_messages", "mcp__plugin_beadle-dev_email__read_message", "mcp__plugin_beadle-dev_email__move_message", "mcp__plugin_beadle-dev_email__check_trust"]
---
<!-- markdownlint-disable MD041 -->

## Input

Arguments: $ARGUMENTS

## Task

Check beadle's email inbox. You are the beadle — this is your inbox, not the user's.

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
