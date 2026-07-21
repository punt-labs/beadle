# Channels End-to-End Testing — Blocked

Status: **BLOCKED** as of 2026-04-13.
Bead: beadle-9rb
Current release: v0.14.1.

## Current state

As of v0.14.1, the plugin manifest **does not** declare a channels entry.
This was removed to stop the "channel cannot register" startup warning for
users whose organizations have channels disabled. The server-side code is
otherwise intact and ready to re-enable:

- MCP server declares `claude/channel` capability.
- Poller fires `notifications/claude/channel` on new mail.
- Re-enable is a one-line manifest addition:
  `"channels": [{"server": "email"}]` in `.claude-plugin/plugin.json`.

v0.14.0 shipped with the channels manifest enabled, but the Claude Code
client-side gate blocked activation. This doc records what was tried at
that version before the manifest was disabled in v0.14.1.

## Goal

Verify Claude Code channels support end-to-end: beadle-email's poller detects
new INBOX mail → fires `notifications/claude/channel` → Claude Code routes the
payload into the running session as an unprompted prompt.

## What works (server side, verified)

- Plugin manifest (when enabled): `"channels": [{"server": "email"}]` — array
  form, required by Claude Code schema. Currently disabled in v0.14.1.
- MCP server declares channel capability:
  `server.WithExperimental(map[string]any{"claude/channel": map[string]any{}})`.
- Poller fires both notifications on `unseen > prev`:
  - `notifications/tools/list_changed`
  - `notifications/claude/channel` with `content` + `meta.source=beadle-email`.
- File-based logging to `~/.punt-labs/beadle/logs/beadle-email.log` (tee'd with stderr).
- Per-tick INFO log: `"poller: tick" unseen=N previous=N first=true|false`.
- Binary verified via `-X main.version=` ldflags (`0.14.0-tick`).

## What's blocked (Claude Code client side)

Claude Code v2.1.104 on Linux, logged in as Claude Team, emits:

```text
--dangerously-load-development-channels ignored (plugin:beadle@punt-labs)
Channels are not currently available
```

This message means the client refuses to activate channels despite:

- Correct plugin flag format (`plugin:<name>@<marketplace>`).
- Plugin present at `~/.claude/plugins/cache/punt-labs/beadle/0.14.0/` with
  channels manifest intact.
- Claude Team org admin console reports channels enabled.
- Claude Code version 2.1.104 (≥ 2.1.80 required).
- Logged in via claude.ai (not API key).

Per the [Claude Code channels docs](https://code.claude.com/docs/en/channels):

> `channelsEnabled`: Master switch. Must be `true` for any channel to deliver
> messages. Blocks all channels including the development flag when off.

The dev flag being "ignored" matches the master-switch-off behavior, but the
master switch should be on.

## Attempts made

### Flag syntax (confirmed correct form)

| Flag | Result |
|------|--------|
| `--channels plugin:beadle@punt-labs` | "Channels are not currently available" |
| `--dangerously-load-development-channels server:plugin:beadle:email` | "ignored" (wrong syntax — `server:` prefix is for bare .mcp.json entries) |
| `--dangerously-load-development-channels plugin:beadle@punt-labs` | "ignored" |

Docs confirm `plugin:<name>@<marketplace>` is the correct format for a
channel inside an installed plugin.

### Plugin name verification

- Source repo `.claude-plugin/plugin.json`: `"name": "beadle-dev"` (legacy dev name).
- Installed marketplace copy `~/.claude/plugins/cache/punt-labs/beadle/0.14.0/.claude-plugin/plugin.json`:
  `"name": "beadle"` (marketplace overrides at publish).
- Marketplace: `punt-labs` (path at `~/.claude/plugins/cache/punt-labs/`).
- Flag `plugin:beadle@punt-labs` matches the installed plugin.

### Plugin manifest schema

- Initial release shipped with `"channels": {"email": {"server": "email"}}` (object map).
  Claude Code rejected with "Invalid input: expected array, received object".
  Fixed in v0.14.0 to `"channels": [{"server": "email"}]` (array of objects).

### Capability declaration

- Upgraded mcp-go v0.45.0 → v0.46.0 for `server.WithExperimental` support.
- Server now declares `claude/channel: {}` under experimental capabilities.

### Org policy

- User reports the [claude.ai Admin console](https://claude.ai/admin-settings/claude-code)
  has channels toggled on for the Claude Team org.
- Local override attempted at `/etc/claude-code/managed-settings.json` with
  `{"channelsEnabled": true}`. File write succeeded but malformed (missing
  closing `}`) on the heredoc. Retry pending.

### Session resume vs fresh start

- Tested with `claude -r --dangerously-load-development-channels …` — same banner.
- Tested with fresh `claude --dangerously-load-development-channels …` — same banner.
- Resume hypothesis ruled out.

## Outstanding hypotheses

1. **Malformed `/etc/claude-code/managed-settings.json` blocks policy parsing**
   — Claude Code may silently fall back to "channels off" when the file fails
   to parse. Retry with properly closed JSON.
2. **Server-managed policy not propagating** — the claude.ai admin console
   toggle exists but the client isn't receiving the policy. Logout/login may
   refresh it.
3. **Dev flag bypass of allowlist requires policy on first** — even with the
   master switch conceptually on, maybe the client needs to see it in writing
   before accepting dev flag.
4. **Per-user channel opt-in separate from org toggle** — undocumented.

## Next actions

- User to re-run the heredoc and confirm file closes properly.
- User to log out and back in to force policy refresh.
- If still blocked, file a [Claude Code issue](https://github.com/anthropics/claude-code/issues)
  with repro steps.

## Moving on

beadle-9rb server-side work is complete and released (v0.14.0). Testing
deferred until the client gate can be opened. Tracked in bead; not blocking
other work.
