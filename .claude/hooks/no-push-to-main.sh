#!/bin/bash
# PreToolUse Bash — block git push when on main/master.

COMMAND=$(jq -r '.tool_input.command // empty' < /dev/stdin)

# Only check git push commands
echo "$COMMAND" | grep -qE '^git push' || exit 0

cd "${CLAUDE_PROJECT_DIR:-.}" 2>/dev/null || exit 0

BRANCH=$(git branch --show-current 2>/dev/null)

if [[ "$BRANCH" == "main" || "$BRANCH" == "master" ]]; then
    jq -n '{
        hookSpecificOutput: {
            hookEventName: "PreToolUse",
            permissionDecision: "deny",
            permissionDecisionReason: "BLOCKED: Cannot push directly to main. All changes go through PRs. Create a feature branch, push it, and open a PR."
        }
    }'
    exit 0
fi

exit 0
