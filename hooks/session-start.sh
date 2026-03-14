#!/usr/bin/env bash
# SessionStart — deploy commands, auto-allow MCP permissions, first-run setup.

PLUGIN_ROOT="${CLAUDE_PLUGIN_ROOT:-$(git rev-parse --show-toplevel 2>/dev/null)}"
if [[ -z "$PLUGIN_ROOT" ]]; then
  echo '{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":"Beadle SessionStart: skipped (not in a git repo)"}}'
  exit 0
fi

SETTINGS="$HOME/.claude/settings.json"
COMMANDS_DIR="$HOME/.claude/commands"
PLUGIN_JSON="${PLUGIN_ROOT}/.claude-plugin/plugin.json"

# Detect dev mode: plugin.json name contains "beadle-dev"
DEV_MODE=false
if grep -q '"beadle-dev"' "$PLUGIN_JSON" 2>/dev/null; then
  DEV_MODE=true
fi

if [[ "$DEV_MODE" == "true" ]]; then
  TOOL_GLOB="mcp__plugin_beadle-dev_email__*"
else
  TOOL_GLOB="mcp__plugin_beadle_email__*"
fi

ACTIONS=()

# ── Deploy top-level commands (diff-and-copy) ─────────────────────────
# In dev mode, skip — prod plugin handles top-level commands.
if [[ "$DEV_MODE" == "false" ]]; then
  DEPLOYED=()
  shopt -s nullglob
  for cmd_file in "$PLUGIN_ROOT/commands/"*.md; do
    name="$(basename "$cmd_file")"
    [[ "$name" == *-dev.md ]] && continue
    dest="$COMMANDS_DIR/$name"
    mkdir -p "$COMMANDS_DIR"
    if [[ ! -f "$dest" ]] || ! diff -q "$cmd_file" "$dest" >/dev/null 2>&1; then
      if cp "$cmd_file" "$dest"; then
        DEPLOYED+=("/${name%.md}")
      fi
    fi
  done
  if [[ ${#DEPLOYED[@]} -gt 0 ]]; then
    ACTIONS+=("Deployed commands: ${DEPLOYED[*]}")
  fi
fi

# ── Auto-allow MCP tool permissions ───────────────────────────────────
if ! command -v jq >/dev/null 2>&1; then
  ACTIONS+=("jq not found, skipping permission setup")
elif [[ ! -f "$SETTINGS" ]]; then
  ACTIONS+=("~/.claude/settings.json not found, skipping permission setup")
else
  if ! jq -e --arg glob "$TOOL_GLOB" '.permissions.allow // [] | index($glob)' "$SETTINGS" >/dev/null 2>&1; then
    TMP=$(mktemp "$SETTINGS.XXXXXX")
    if jq --arg glob "$TOOL_GLOB" '
      .permissions //= {} |
      .permissions.allow //= [] |
      .permissions.allow += [$glob]
    ' "$SETTINGS" > "$TMP"; then
      mv "$TMP" "$SETTINGS"
      ACTIONS+=("Auto-allowed beadle MCP tools in permissions")
    else
      rm -f "$TMP"
    fi
  fi
fi

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
