#!/usr/bin/env bash
set -euo pipefail
# PostToolUse — two-channel display for beadle-email MCP tools.
# See punt-kit/patterns/two-channel-display.md for the pattern.

# Require jq — without it, let raw output through.
command -v jq >/dev/null 2>&1 || exit 0

INPUT=$(cat)
TOOL=$(echo "$INPUT" | jq -r '.tool_name')
TOOL_NAME="${TOOL##*__}"

# Check if the tool result is an error. MCP tool errors from mcp-go arrive
# as isError:true with the error message as plain text in .tool_response[0].text.
IS_ERROR=$(echo "$INPUT" | jq -r '
  if (.tool_response | type) == "array" then
    (.tool_response[0].isError // false)
  else
    false
  end
')

RESULT=$(echo "$INPUT" | jq -r '
  if (.tool_response | type) == "array" then
    (.tool_response[0].text // "")
  else
    (.tool_response // "")
  end
')

# Bail on empty result (but not "null" — that's a valid empty-list response).
[[ -z "$RESULT" ]] && exit 0

emit() {
  local summary="$1" ctx="${2:-}"
  if [[ -n "$ctx" ]]; then
    jq -n --arg s "$summary" --arg c "$ctx" '{
      hookSpecificOutput: {
        hookEventName: "PostToolUse",
        updatedMCPToolOutput: $s,
        additionalContext: $c
      }
    }'
  else
    jq -n --arg s "$summary" '{
      hookSpecificOutput: {
        hookEventName: "PostToolUse",
        updatedMCPToolOutput: $s
      }
    }'
  fi
}

# Surface errors directly — MCP tool errors are plain strings, not JSON objects.
if [[ "$IS_ERROR" == "true" ]]; then
  emit "error: ${RESULT}"
  exit 0
fi

# Tools return pre-formatted text (not JSON). The first line is the
# panel summary; the full text goes to additionalContext.
# Extract the first line as summary, pass full text as context.
FIRST_LINE=$(echo "$RESULT" | head -1)

case "$TOOL_NAME" in
  # Silent tools: panel summary only, no context spill
  send_email|move_message|add_contact|remove_contact)
    emit "$RESULT"
    ;;

  # Data tools: first line as summary, full text in context
  list_messages|read_message|list_folders|show_mime|verify_signature|\
  check_trust|download_attachment|list_contacts|find_contact)
    emit "$FIRST_LINE" "$RESULT"
    ;;

  *)
    emit "done" "$RESULT"
    ;;
esac
