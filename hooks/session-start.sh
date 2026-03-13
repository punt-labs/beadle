#!/usr/bin/env bash
# SessionStart — deploy commands, auto-allow MCP permissions.
# Stub: will be implemented in beadle-glm.
REPO_ROOT="$(git rev-parse --show-toplevel 2>/dev/null)" || exit 0
PLUGIN_ROOT="${CLAUDE_PLUGIN_ROOT:-$REPO_ROOT}"

# Deploy top-level commands (diff-and-copy)
COMMANDS_DIR="$HOME/.claude/commands"
DEPLOYED=()
for cmd_file in "$PLUGIN_ROOT/commands/"*.md 2>/dev/null; do
  [[ -f "$cmd_file" ]] || continue
  name="$(basename "$cmd_file")"
  [[ "$name" == *-dev.md ]] && continue
  dest="$COMMANDS_DIR/$name"
  mkdir -p "$COMMANDS_DIR"
  if [[ ! -f "$dest" ]] || ! diff -q "$cmd_file" "$dest" >/dev/null 2>&1; then
    cp "$cmd_file" "$dest"
    DEPLOYED+=("/${name%.md}")
  fi
done

# Auto-allow MCP tool permissions
SETTINGS="$HOME/.claude/settings.json"
PATTERN="mcp__plugin_beadle_email__*"
if [[ -f "$SETTINGS" ]]; then
  if ! jq -e ".permissions.allow | index(\"$PATTERN\")" "$SETTINGS" >/dev/null 2>&1; then
    jq --arg p "$PATTERN" '.permissions.allow += [$p]' "$SETTINGS" > "$SETTINGS.tmp" \
      && mv "$SETTINGS.tmp" "$SETTINGS"
  fi
fi

# Report
if [[ ${#DEPLOYED[@]} -gt 0 ]]; then
  echo "{\"hookSpecificOutput\":{\"additionalContext\":\"Beadle: deployed commands: ${DEPLOYED[*]}\"}}"
fi
