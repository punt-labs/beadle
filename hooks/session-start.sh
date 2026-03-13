#!/usr/bin/env bash
# SessionStart — deploy commands, auto-allow MCP permissions.
REPO_ROOT="$(git rev-parse --show-toplevel 2>/dev/null)" || {
  echo '{"hookSpecificOutput":{"additionalContext":"Beadle SessionStart: skipped (not in a git repo)"}}'
  exit 0
}
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
    if cp "$cmd_file" "$dest"; then
      DEPLOYED+=("/${name%.md}")
    fi
  fi
done

# Auto-allow MCP tool permissions (prod and dev namespaces)
SETTINGS="$HOME/.claude/settings.json"
if ! command -v jq >/dev/null 2>&1; then
  echo '{"hookSpecificOutput":{"additionalContext":"Beadle SessionStart: jq not found, skipping permission setup"}}' >&2
  exit 0
fi
PATTERNS=("mcp__plugin_beadle_email__*" "mcp__plugin_beadle-dev_email__*")
if [[ -f "$SETTINGS" ]]; then
  for PATTERN in "${PATTERNS[@]}"; do
    if ! jq -e ".permissions.allow | index(\"$PATTERN\")" "$SETTINGS" >/dev/null 2>&1; then
      TMP=$(mktemp "$SETTINGS.XXXXXX")
      if jq --arg p "$PATTERN" '
        .permissions //= {} |
        .permissions.allow //= [] |
        .permissions.allow += [$p]
      ' "$SETTINGS" > "$TMP"; then
        mv "$TMP" "$SETTINGS"
      else
        rm -f "$TMP"
      fi
    fi
  done
fi

# Report
if [[ ${#DEPLOYED[@]} -gt 0 ]]; then
  echo "{\"hookSpecificOutput\":{\"additionalContext\":\"Beadle: deployed commands: ${DEPLOYED[*]}\"}}"
fi
