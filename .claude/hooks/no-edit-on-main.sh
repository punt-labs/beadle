#!/bin/bash
# PreToolUse Edit|Write — block file edits on main/master branch.

INPUT=$(cat)
FILE_PATH=$(echo "$INPUT" | jq -r '.tool_input.file_path // empty' 2>/dev/null)

if [[ -n "$FILE_PATH" && -d "$(dirname "$FILE_PATH")" ]]; then
    CHECK_DIR="$(dirname "$FILE_PATH")"
else
    CHECK_DIR="${CLAUDE_PROJECT_DIR:-.}"
fi

cd "$CHECK_DIR" 2>/dev/null || cd "${CLAUDE_PROJECT_DIR:-.}" 2>/dev/null || exit 0

BRANCH=$(git branch --show-current 2>/dev/null)

if [[ "$BRANCH" == "main" || "$BRANCH" == "master" ]]; then
    jq -n '{
        hookSpecificOutput: {
            hookEventName: "PreToolUse",
            permissionDecision: "deny",
            permissionDecisionReason: "BLOCKED: You are on the main branch. Create a feature branch first: git checkout -b feat/<description> main"
        }
    }'
    exit 0
fi

exit 0
