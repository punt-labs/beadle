#!/bin/bash
# PreToolUse Agent — block Agent calls without run_in_background=true.
#
# jq-optional and fail-closed: run_in_background is read via jq when present,
# else detected by scanning the raw stdin payload. Anything that is not an
# explicit `true` is denied, so a missing jq enforces the rule rather than
# silently skipping it. The deny payload is emitted with printf.

INPUT=$(cat)

if command -v jq >/dev/null 2>&1; then
    RUN_IN_BG=$(printf '%s' "$INPUT" | jq -r '.tool_input.run_in_background // false' 2>/dev/null)
elif printf '%s' "$INPUT" | grep -qE '"run_in_background"[[:space:]]*:[[:space:]]*true'; then
    RUN_IN_BG=true
else
    RUN_IN_BG=false
fi

if [[ "$RUN_IN_BG" != "true" ]]; then
    printf '%s\n' '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"BLOCKED: Agent calls must use run_in_background=true. Sub-agents run in the background so the leader can continue working. Add run_in_background=true to the Agent call."}}'
    exit 0
fi

exit 0
