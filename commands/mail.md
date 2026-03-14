---
description: "Mail something to me or someone else"
argument-hint: "<what to mail> [to <recipient>]"
allowed-tools: ["mcp__plugin_beadle_email__send_email", "mcp__plugin_beadle-dev_email__send_email"]
---
<!-- markdownlint-disable MD041 -->

## Input

Arguments: $ARGUMENTS

## Task

Send an email. This is the email-specific outbound verb — it always means email.

### Parse the arguments

The arguments describe what to mail and optionally to whom. Examples:

- `/mail me a summary` — email a summary of the current conversation to the owner
- `/mail this to kai@example.com` — email the current context to a specific recipient
- `/mail me the test results` — compose and send relevant content to the owner

### Default recipient

If no recipient is specified, send to the owner. The `send_email` tool sends from beadle's configured address — ask the user for the recipient if it cannot be inferred from context.

### Compose and send

1. Compose the email content based on the arguments. Use plain text.
2. Choose a clear, descriptive subject line.
3. Call `send_email` with the recipient, subject, and body.

The result is already formatted by a PostToolUse hook and displayed in the panel. Do not repeat or reformat the send confirmation.
