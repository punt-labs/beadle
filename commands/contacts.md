---
description: "Manage your address book"
argument-hint: "[list | add <name> <email> [permissions] | remove <name> | find <query>]"
allowed-tools: ["mcp__plugin_beadle_email__list_contacts", "mcp__plugin_beadle_email__find_contact", "mcp__plugin_beadle_email__add_contact", "mcp__plugin_beadle_email__remove_contact", "mcp__plugin_beadle-dev_email__list_contacts", "mcp__plugin_beadle-dev_email__find_contact", "mcp__plugin_beadle-dev_email__add_contact", "mcp__plugin_beadle-dev_email__remove_contact"]
---
<!-- markdownlint-disable MD041 -->

## Input

Arguments: $ARGUMENTS

## Task

Manage the beadle address book. Contacts enable name-based addressing —
`/mail alice` resolves to the stored email without an extra lookup.

### Argument routing

- `list` or no argument — list all contacts
- `add` — add a new contact (prompt for name and email)
- `remove <name>` — remove a contact by name
- `find <query>` — search by name, alias, or email

### List (default)

Call `list_contacts`. Display results as a compact table: name, email, aliases.
If empty, say "No contacts. Use `/contacts add` to add one."

### Add

If arguments include name, email, and optionally permissions after `add`
(e.g., `/contacts add Alice alice@example.com rw-`), use them directly.
Otherwise prompt for:

1. Name (required, unique)
2. Email (required — may be a glob pattern like `*@github.com` for domain-wide `r--` or `---` only)
3. Permissions (optional, default `---`; format: `rwx`, `rw-`, `r--`, `---`)
4. Aliases (optional, comma-separated)
5. GPG key ID (optional)

Call `add_contact` with the collected fields.

### Remove

Extract the name from arguments after `remove`.
Call `remove_contact` with the name. Confirm removal.

### Find

Extract the query from arguments after `find`.
Call `find_contact` with the query. Display matches as a compact table.
If no matches, say "No contacts matching `<query>`."
