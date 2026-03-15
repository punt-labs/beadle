#!/usr/bin/env bash
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
  local summary="$1" ctx="$2"
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

# If RESULT is not valid JSON, treat it as an opaque string.
if ! echo "$RESULT" | jq -e 'type' >/dev/null 2>&1; then
  emit "$RESULT"
  exit 0
fi

case "$TOOL_NAME" in
  send_email)
    TO=$(echo "$RESULT" | jq -r '.to // "unknown"')
    METHOD=$(echo "$RESULT" | jq -r '.method // "unknown"')
    # Strip method prefix for brevity: proton-bridge-smtp → smtp
    METHOD="${METHOD##*-}"
    emit "sent to ${TO} via ${METHOD}"
    ;;

  list_messages)
    TOTAL=$(echo "$RESULT" | jq 'if type == "array" then length elif . == null then 0 else 0 end')
    UNREAD=$(echo "$RESULT" | jq 'if type == "array" then [.[] | select(.unread == true)] | length else 0 end')
    if [[ "$TOTAL" -eq 0 ]]; then
      emit "no messages"
    elif [[ "$UNREAD" -gt 0 ]]; then
      emit "${TOTAL} messages (${UNREAD} unread)" "$RESULT"
    else
      emit "${TOTAL} messages" "$RESULT"
    fi
    ;;

  read_message)
    FROM=$(echo "$RESULT" | jq -r '.from // "unknown"')
    TRUST=$(echo "$RESULT" | jq -r '.trust_level // "unknown"')
    emit "from: ${FROM} · ${TRUST}" "$RESULT"
    ;;

  list_folders)
    COUNT=$(echo "$RESULT" | jq 'if type == "array" then length else 0 end')
    emit "${COUNT} folders" "$RESULT"
    ;;

  show_mime)
    emit "MIME structure" "$RESULT"
    ;;

  verify_signature)
    VALID=$(echo "$RESULT" | jq -r '.valid // false')
    KEY_ID=$(echo "$RESULT" | jq -r '.key_id // empty')
    if [[ "$VALID" == "true" ]]; then
      SUMMARY="verified"
      [[ -n "$KEY_ID" ]] && SUMMARY="verified · key ${KEY_ID}"
    else
      SUMMARY="invalid signature"
    fi
    emit "$SUMMARY" "$RESULT"
    ;;

  move_message)
    DEST=$(echo "$RESULT" | jq -r '.destination // "Archive"')
    MSG_ID=$(echo "$RESULT" | jq -r '.message_id // "?"')
    emit "moved #${MSG_ID} → ${DEST}"
    ;;

  check_trust)
    # TrustResult serializes trust level as "level", not "trust_level".
    TRUST=$(echo "$RESULT" | jq -r '.level // "unknown"')
    ENCRYPTION=$(echo "$RESULT" | jq -r '.encryption // empty')
    SUMMARY="$TRUST"
    [[ -n "$ENCRYPTION" ]] && SUMMARY="${TRUST} · ${ENCRYPTION}"
    emit "$SUMMARY" "$RESULT"
    ;;

  download_attachment)
    STATUS=$(echo "$RESULT" | jq -r '.status // "saved"')
    FILENAME=$(echo "$RESULT" | jq -r '.filename // "unknown"')
    SIZE=$(echo "$RESULT" | jq -r '.size // 0')
    emit "${STATUS}: ${FILENAME} (${SIZE} bytes)" "$RESULT"
    ;;

  *)
    emit "done" "$RESULT"
    ;;
esac
