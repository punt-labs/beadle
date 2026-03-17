#!/usr/bin/env bash
set -euo pipefail
# SessionStart — deploy commands, auto-allow MCP permissions, first-run setup.

PLUGIN_ROOT="${CLAUDE_PLUGIN_ROOT:-$(git rev-parse --show-toplevel 2>/dev/null || true)}"
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

# ── Auto-allow MCP tools and skills ───────────────────────────────────
# Every MCP tool and every skill must be auto-approved so users never see
# a permission prompt after enabling the plugin. Uses the PLUGIN_RULES
# array pattern from punt-kit/standards/permissions.md § 6.
#
# Skill names must match deployed commands: inbox.md, mail.md, send.md.
# If a command is added/renamed, update this list — stale entries cause
# unexplained permission prompts (see punt-kit/standards/plugins.md § 2).
if ! command -v jq >/dev/null 2>&1; then
  ACTIONS+=("jq not found, skipping permission setup")
else
  # Build PLUGIN_RULES via jq to avoid JSON injection from $TOOL_GLOB
  PLUGIN_RULES=$(jq -n --arg glob "$TOOL_GLOB" \
    '[$glob, "Skill(inbox)", "Skill(mail)", "Skill(send)"]')

  if [[ ! -f "$SETTINGS" ]]; then
    if mkdir -p "$(dirname "$SETTINGS")" && printf '{}' > "$SETTINGS"; then
      ACTIONS+=("Created ~/.claude/settings.json")
    else
      ACTIONS+=("Failed to create ~/.claude/settings.json — skipping permission setup")
    fi
  fi

  if [[ -f "$SETTINGS" ]]; then
    ADDED=$(jq -r --argjson new "$PLUGIN_RULES" '
      (.permissions.allow // []) as $orig
      | [$new[] | select(. as $r | $orig | index($r) | not)] | length
    ' "$SETTINGS" 2>/dev/null) || ADDED=""

    if [[ -z "$ADDED" ]]; then
      ACTIONS+=("Failed to read permissions from settings.json (file may be corrupt)")
    elif [[ "$ADDED" -gt 0 ]]; then
      TMP=$(mktemp "$SETTINGS.XXXXXX")
      if jq --argjson new "$PLUGIN_RULES" '
        (.permissions.allow // []) as $orig
        | .permissions.allow = $orig + [$new[] | select(. as $r | $orig | index($r) | not)]
      ' "$SETTINGS" > "$TMP"; then
        mv "$TMP" "$SETTINGS"
        ACTIONS+=("Auto-allowed $ADDED permission rule(s) in settings.json")
      else
        rm -f "$TMP"
        ACTIONS+=("Failed to update permissions in settings.json")
      fi
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
