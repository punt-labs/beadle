#!/usr/bin/env bash
set -euo pipefail
# SessionStart — deploy commands, first-run setup. Grants no permissions.

# Claude Code sets CLAUDE_PLUGIN_ROOT for an installed plugin. When it is
# absent — the script run by hand, or from a dev checkout — resolve the plugin
# root from the script's own location: this file lives at
# <plugin-root>/hooks/session-start.sh. The git toplevel is no longer a usable
# fallback, because the shippable surface moved under plugin/ and the repo root
# has no .claude-plugin/ or commands/ of its own.
PLUGIN_ROOT="${CLAUDE_PLUGIN_ROOT:-}"
if [[ -z "$PLUGIN_ROOT" ]]; then
  if ! PLUGIN_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"; then
    echo '{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":"Beadle SessionStart: skipped (cannot resolve plugin root)"}}'
    exit 0
  fi
fi

COMMANDS_DIR="$HOME/.claude/commands"
PLUGIN_JSON="${PLUGIN_ROOT}/.claude-plugin/plugin.json"

# Assert the resolved root really is a plugin root before trusting anything
# derived from it. Without this, a wrong PLUGIN_ROOT degrades silently rather
# than loudly: the dev-mode probe below reads false because grep cannot open
# the file, and the deploy loop iterates an empty glob under nullglob — so a
# future relayout or a misconfigured CLAUDE_PLUGIN_ROOT deploys nothing and
# says so nowhere. Fail visibly instead.
if [[ ! -f "$PLUGIN_JSON" ]]; then
  if command -v jq >/dev/null 2>&1; then
    jq -n --arg root "$PLUGIN_ROOT" '{
      hookSpecificOutput: {
        hookEventName: "SessionStart",
        additionalContext: ("Beadle SessionStart: skipped (no .claude-plugin/plugin.json under resolved plugin root " + $root + ")")
      }
    }'
  else
    echo "{\"hookSpecificOutput\":{\"hookEventName\":\"SessionStart\",\"additionalContext\":\"Beadle SessionStart: skipped (no .claude-plugin/plugin.json under resolved plugin root ${PLUGIN_ROOT//\"/\\\"})\"}}"
  fi
  exit 0
fi

# Detect dev mode: plugin.json name contains "beadle-dev"
DEV_MODE=false
if grep -q '"beadle-dev"' "$PLUGIN_JSON" 2>/dev/null; then
  DEV_MODE=true
fi

ACTIONS=()

# ── Deploy top-level commands (diff-and-copy) ─────────────────────────
# In dev mode, the prod plugin normally handles top-level commands.
# But if no prod plugin is installed, the dev plugin must deploy them.
SKIP_DEPLOY=false
if [[ "$DEV_MODE" == "true" ]]; then
  INSTALLED="$HOME/.claude/plugins/installed_plugins.json"
  if command -v jq >/dev/null 2>&1 && [[ -f "$INSTALLED" ]]; then
    if jq -e '.plugins["beadle@punt-labs"]' "$INSTALLED" >/dev/null 2>&1; then
      SKIP_DEPLOY=true
    fi
  fi
fi

DEPLOYED=()
shopt -s nullglob
if [[ "$SKIP_DEPLOY" == "true" ]]; then
  : # prod plugin handles deployment
elif ! mkdir -p "$COMMANDS_DIR"; then
  ACTIONS+=("Failed to create $COMMANDS_DIR — skipping command deployment")
else
  for cmd_file in "$PLUGIN_ROOT/commands/"*.md; do
    name="$(basename "$cmd_file")"
    [[ "$name" == *-dev.md ]] && continue
    dest="$COMMANDS_DIR/$name"
    if [[ ! -f "$dest" ]] || ! diff -q "$cmd_file" "$dest" >/dev/null 2>&1; then
      if cp "$cmd_file" "$dest"; then
        DEPLOYED+=("/${name%.md}")
      else
        ACTIONS+=("Failed to deploy /${name%.md}")
      fi
    fi
  done
  if [[ ${#DEPLOYED[@]} -gt 0 ]]; then
    ACTIONS+=("Deployed commands: ${DEPLOYED[*]}")
  fi
fi

# Permission rules are not set up here: SessionStart never edits a settings
# file. `beadle-email enable` adds beadle's allow rules to the repo's
# .claude/settings.json (punt-kit permissions.md §6).

# ── First-run check: verify beadle-email binary is available ──────────
if ! command -v beadle-email >/dev/null 2>&1; then
  ACTIONS+=("beadle-email binary not found on PATH")
fi

# ── Notify Claude if anything was set up ──────────────────────────────
if [[ ${#ACTIONS[@]} -gt 0 ]]; then
  MSG="Beadle plugin setup:"
  for action in "${ACTIONS[@]}"; do
    MSG="$MSG $action."
  done
  # Use jq for safe JSON output when available, fall back to echo.
  if command -v jq >/dev/null 2>&1; then
    jq -n --arg ctx "$MSG" '{
      hookSpecificOutput: {
        hookEventName: "SessionStart",
        additionalContext: $ctx
      }
    }'
  else
    echo "{\"hookSpecificOutput\":{\"hookEventName\":\"SessionStart\",\"additionalContext\":\"${MSG//\"/\\\"}\"}}"
  fi
fi

exit 0
