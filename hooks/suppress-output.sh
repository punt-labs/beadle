#!/usr/bin/env bash
[[ -f "$HOME/.punt-hooks-kill" ]] && exit 0
# Format beadle-email MCP tool output for the UI panel.
#
# Two-channel display (see punt-kit/patterns/two-channel-display.md):
#   updatedMCPToolOutput  -> compact panel line for the UI
#   additionalContext     -> full data for the model to reference
#
# No `set -euo pipefail` — hooks must degrade gracefully on
# malformed input rather than failing the tool call.

# Require jq — without it, let raw output through.
command -v jq >/dev/null 2>&1 || exit 0

INPUT=$(cat)
TOOL=$(printf '%s' "$INPUT" | jq -r '.tool_name // empty' 2>/dev/null)
TOOL_NAME="${TOOL##*__}"

# Single-pass unpack: handles string-encoded, array, or object responses.
RESULT=$(printf '%s' "$INPUT" | jq -r '
  def unpack: if type == "string" then (fromjson? // .) else . end;
  if (.tool_response | type) == "array" then
    (.tool_response[0].text // "" | unpack)
  else
    (.tool_response | unpack)
  end
  | if type == "object" and has("result") then (.result | unpack) else . end
' 2>/dev/null)

# Fallback: if unpack failed or yielded nothing, use raw tool_response.
if [[ -z "$RESULT" ]]; then
  RESULT=$(printf '%s' "$INPUT" | jq -r '.tool_response // empty' 2>/dev/null)
  [[ -z "$RESULT" ]] && RESULT="(no output)"
fi

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

# ── Error guard: surface tool errors directly ────────────────────────
ERROR_MSG=$(printf '%s' "$RESULT" | jq -r '.error // empty' 2>/dev/null)
if [[ -n "$ERROR_MSG" ]]; then
  emit "error: ${ERROR_MSG}"
  exit 0
fi

# Check for MCP-level isError (mcp-go sends errors as plain text with isError flag)
IS_ERROR=$(printf '%s' "$INPUT" | jq -r '
  if (.tool_response | type) == "array" then
    (.tool_response[0].isError // false)
  else
    false
  end
' 2>/dev/null)
if [[ "$IS_ERROR" == "true" ]]; then
  emit "error: ${RESULT}"
  exit 0
fi

# ── list_messages ──────────────────────────────────────────────────────
if [[ "$TOOL_NAME" == "list_messages" ]]; then
  if [[ "$RESULT" == "No messages." ]]; then
    emit "$RESULT"
  else
    SUMMARY=$(printf '%s' "$RESULT" | head -1)
    emit "${SUMMARY} messages" "$RESULT"
  fi
  exit 0
fi

# ── read_message ───────────────────────────────────────────────────────
if [[ "$TOOL_NAME" == "read_message" ]]; then
  SUBJ=$(printf '%s' "$RESULT" | grep 'Subject:' | head -1 | sed 's/.*Subject: *//')
  if [[ -z "$SUBJ" ]]; then
    SUBJ="(no subject)"
  fi
  emit "${SUBJ}" "$RESULT"
  exit 0
fi

# ── list_folders ───────────────────────────────────────────────────────
if [[ "$TOOL_NAME" == "list_folders" ]]; then
  if [[ "$RESULT" == "No folders." ]]; then
    emit "$RESULT"
  else
    COUNT=$(printf '%s' "$RESULT" | grep -c '[^ ]')
    emit "${COUNT} folders" "$RESULT"
  fi
  exit 0
fi

# ── send_email ─────────────────────────────────────────────────────────
if [[ "$TOOL_NAME" == "send_email" ]]; then
  emit "$RESULT"
  exit 0
fi

# ── verify_signature ───────────────────────────────────────────────────
if [[ "$TOOL_NAME" == "verify_signature" ]]; then
  FIRST=$(printf '%s' "$RESULT" | head -1)
  emit "${FIRST}" "$RESULT"
  exit 0
fi

# ── show_mime ──────────────────────────────────────────────────────────
if [[ "$TOOL_NAME" == "show_mime" ]]; then
  if [[ "$RESULT" == "No MIME parts." ]]; then
    emit "$RESULT"
  else
    COUNT=$(printf '%s' "$RESULT" | grep -c '[^ ]')
    emit "${COUNT} parts" "$RESULT"
  fi
  exit 0
fi

# ── check_trust ────────────────────────────────────────────────────────
if [[ "$TOOL_NAME" == "check_trust" ]]; then
  FIRST=$(printf '%s' "$RESULT" | head -1)
  emit "${FIRST}" "$RESULT"
  exit 0
fi

# ── move_message ───────────────────────────────────────────────────────
if [[ "$TOOL_NAME" == "move_message" ]]; then
  emit "$RESULT"
  exit 0
fi

# ── download_attachment ────────────────────────────────────────────────
if [[ "$TOOL_NAME" == "download_attachment" ]]; then
  FIRST=$(printf '%s' "$RESULT" | head -1)
  emit "${FIRST}" "$RESULT"
  exit 0
fi

# ── list_contacts ──────────────────────────────────────────────────────
if [[ "$TOOL_NAME" == "list_contacts" ]]; then
  if [[ "$RESULT" == "No contacts." ]]; then
    emit "$RESULT"
  else
    COUNT=$(printf '%s' "$RESULT" | grep -c '[^ ]')
    emit "${COUNT} contacts" "$RESULT"
  fi
  exit 0
fi

# ── find_contact ───────────────────────────────────────────────────────
if [[ "$TOOL_NAME" == "find_contact" ]]; then
  if [[ "$RESULT" == "No contacts." ]]; then
    emit "no matches"
  else
    COUNT=$(printf '%s' "$RESULT" | grep -c '[^ ]')
    emit "${COUNT} matches" "$RESULT"
  fi
  exit 0
fi

# ── add_contact ────────────────────────────────────────────────────────
if [[ "$TOOL_NAME" == "add_contact" ]]; then
  emit "$RESULT"
  exit 0
fi

# ── remove_contact ─────────────────────────────────────────────────────
if [[ "$TOOL_NAME" == "remove_contact" ]]; then
  emit "$RESULT"
  exit 0
fi

# ── Fallback: full output in panel ─────────────────────────────────────
emit "$RESULT"
