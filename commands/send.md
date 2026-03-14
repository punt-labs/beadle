---
description: "Send something via any channel (email today, more later)"
argument-hint: "<what to send> [via <channel>]"
allowed-tools: ["mcp__plugin_beadle_email__send_email"]
---
<!-- markdownlint-disable MD041 -->

## Input

Arguments: $ARGUMENTS

## Task

Send something to the user or a specified recipient via the best available channel. Today, email is the only channel. In the future, this command will support Signal and other channels.

### Parse the arguments

The arguments describe what to send and optionally the channel. Examples:

- `/send me an email summary` — send via email (explicit)
- `/send me a summary` — send via email (default, only channel today)
- `/send this to kai@example.com` — send to a specific recipient via email

### Default behavior

- Default recipient: the owner (<jim@punt-labs.com>)
- Default channel: email (the only channel currently available)

### Compose and send

1. Compose the content based on the arguments. Use plain text.
2. Choose a clear, descriptive subject line.
3. Call `send_email` with the recipient, subject, and body.

The result is already formatted by a PostToolUse hook and displayed in the panel. Do not repeat or reformat the send confirmation.

### Future channels

When new channels are added (Signal, etc.), this command will route based on the `via` argument or infer the channel from context. For now, all sends go through email.
