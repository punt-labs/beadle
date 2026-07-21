#!/bin/bash
# PreToolUse Bash — block gh run watch and gh pr checks --watch.
# These poll every 3s and burn through the 5000/hr GitHub API rate limit.
#
# jq-optional: the command is read via jq when present or the raw stdin
# payload otherwise, and the deny payload is emitted with printf, so a missing
# jq degrades to a broader check rather than a silent no-op.

INPUT=$(cat)

if command -v jq >/dev/null 2>&1; then
    COMMAND=$(printf '%s' "$INPUT" | jq -r '.tool_input.command // empty')
else
    COMMAND=$INPUT
fi

if printf '%s' "$COMMAND" | grep -qE 'gh (run|pr checks).*--watch|gh run watch'; then
    printf '%s\n' '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"BLOCKED: --watch waits until everything finishes, so a fast lint failure stays invisible until slow Bugbot completes. Use /loop 2m with gh run view or gh pr checks (without --watch) — polling surfaces partial results so you can address fast-failing checks while slow ones run."}}'
    exit 0
fi

exit 0
