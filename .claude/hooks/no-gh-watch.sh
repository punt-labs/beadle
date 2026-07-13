#!/bin/bash
# PreToolUse Bash — block gh run watch and gh pr checks --watch.
# These poll every 3s and burn through the 5000/hr GitHub API rate limit.

COMMAND=$(jq -r '.tool_input.command // empty' < /dev/stdin)

if echo "$COMMAND" | grep -qE 'gh (run|pr checks).*--watch|gh run watch'; then
    jq -n '{
        hookSpecificOutput: {
            hookEventName: "PreToolUse",
            permissionDecision: "deny",
            permissionDecisionReason: "BLOCKED: --watch waits until everything finishes, so a fast lint failure stays invisible until slow Bugbot completes. Use /loop 2m with gh run view or gh pr checks (without --watch) — polling surfaces partial results so you can address fast-failing checks while slow ones run."
        }
    }'
    exit 0
fi

exit 0
