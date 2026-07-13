#!/bin/bash
# PreToolUse Agent — block Agent calls without run_in_background=true.

INPUT=$(cat)

RUN_IN_BG=$(echo "$INPUT" | jq -r '.tool_input.run_in_background // false' 2>/dev/null)

if [[ "$RUN_IN_BG" != "true" ]]; then
    jq -n '{
        hookSpecificOutput: {
            hookEventName: "PreToolUse",
            permissionDecision: "deny",
            permissionDecisionReason: "BLOCKED: Agent calls must use run_in_background=true. Sub-agents run in the background so the leader can continue working. Add run_in_background=true to the Agent call."
        }
    }'
    exit 0
fi

exit 0
