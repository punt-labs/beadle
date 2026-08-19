---
description: "Enable or disable beadle guidance in this repo"
argument-hint: "enable | disable"
allowed-tools: ["mcp__plugin_beadle_email__enable", "mcp__plugin_beadle-dev_email__enable"]
---
<!-- markdownlint-disable MD041 -->

## Input

Arguments: $ARGUMENTS

## Task

Turn beadle's CLAUDE.md guidance composition on or off in the current repo. This
is the Claude Code door to the same `enable` / `disable` verbs the `beadle-email`
CLI exposes; both write the identical `.punt-labs/beadle/enabled` marker.

### Parse the argument

The argument is exactly one verb:

- `enable` — deposit the beadle user guide into `.punt-labs/beadle/`, mark the
  repo enabled, and add the `@.punt-labs/beadle/CLAUDE.md` import to the repo
  `CLAUDE.md`. Idempotent — re-running is the upgrade path.
- `disable` — remove that import and the enabled marker, leaving
  `.punt-labs/beadle/` dormant.

If the argument is anything else (or missing), do not call the tool. Report that
the command takes exactly `enable` or `disable`.

### Run it

Call the `enable` tool with `action` set to the verb (`enable` or `disable`).
The tool writes working-tree files only — it does not run git.

### After

Report the tool's result verbatim. Remind the user to commit the change
(`.punt-labs/beadle/` and the `CLAUDE.md` import line) through a PR — enablement
is per-repo policy that lands through review, not a tool side effect.
