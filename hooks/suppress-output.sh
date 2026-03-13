#!/usr/bin/env bash
# PostToolUse — two-channel display for beadle-email MCP tools.
# See punt-kit/patterns/two-channel-display.md for the pattern.
INPUT=$(cat)
TOOL=$(echo "$INPUT" | jq -r '.tool_name')
TOOL_NAME="${TOOL##*__}"

RESULT=$(echo "$INPUT" | jq -r '
  if (.tool_response | type) == "array" then
    (.tool_response[0].text // "")
  else
    (.tool_response // "")
  end
')

# Bail on empty result (but not "null" — that's a valid empty-list response).
[[ -z "$RESULT" ]] && exit 0

# If RESULT is not valid JSON, treat it as an opaque string.
if ! echo "$RESULT" | jq -e 'type' >/dev/null 2>&1; then
  emit "$RESULT"
  exit 0
fi

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

# Error handling — surface errors in the panel.
ERROR_MSG=$(echo "$RESULT" | jq -r 'if type == "string" then empty else .error // empty end' 2>/dev/null)
if [[ -n "$ERROR_MSG" ]]; then
  emit "error: ${ERROR_MSG}"
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
    TRUST=$(echo "$RESULT" | jq -r '.trust_level // "unknown"')
    ENCRYPTION=$(echo "$RESULT" | jq -r '.encryption // empty')
    SUMMARY="$TRUST"
    [[ -n "$ENCRYPTION" ]] && SUMMARY="${TRUST} · ${ENCRYPTION}"
    emit "$SUMMARY" "$RESULT"
    ;;

  *)
    emit "done" "$RESULT"
    ;;
esac
