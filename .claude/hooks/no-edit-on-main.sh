#!/bin/bash
# PreToolUse Edit|Write — block file edits on main/master branch.
#
# jq-optional: file_path (used only to locate the repo whose branch is
# checked) is read via jq when present, else via a raw-payload scan, falling
# back to CLAUDE_PROJECT_DIR. The deny payload is emitted with printf, so a
# missing jq never silently drops enforcement.

INPUT=$(cat)

if command -v jq >/dev/null 2>&1; then
    FILE_PATH=$(printf '%s' "$INPUT" | jq -r '.tool_input.file_path // empty' 2>/dev/null)
else
    FILE_PATH=$(printf '%s' "$INPUT" | sed -n 's/.*"file_path"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')
fi

if [[ -n "$FILE_PATH" && -d "$(dirname "$FILE_PATH")" ]]; then
    CHECK_DIR="$(dirname "$FILE_PATH")"
else
    CHECK_DIR="${CLAUDE_PROJECT_DIR:-.}"
fi

cd "$CHECK_DIR" 2>/dev/null || cd "${CLAUDE_PROJECT_DIR:-.}" 2>/dev/null || exit 0

BRANCH=$(git branch --show-current 2>/dev/null)

if [[ "$BRANCH" == "main" || "$BRANCH" == "master" ]]; then
    printf '%s\n' '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"BLOCKED: You are on the main branch. Create a feature branch first: git checkout -b feat/<description> main"}}'
    exit 0
fi

exit 0
