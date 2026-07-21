#!/bin/bash
# PreToolUse Bash — block git push to main/master.
#
# Catches compound commands (cd … && git push, git commit && git push) by
# matching `git push` anywhere in the command, and inspects the pushed ref so
# `git push origin main` is blocked even from a feature branch. Enforcement
# does not depend on jq: the deny payload is emitted with printf, and the
# command is read via jq when present or the raw stdin payload otherwise, so a
# missing jq degrades to a broader check, never a silent no-op.

INPUT=$(cat)

if command -v jq >/dev/null 2>&1; then
    COMMAND=$(printf '%s' "$INPUT" | jq -r '.tool_input.command // empty')
else
    COMMAND=$INPUT
fi

# Only proceed when `git push` appears in command position — at the start or
# after a shell separator (&&, ||, ;, |, `(`). This catches compound commands
# (cd … && git push, git commit && git push) without false-matching the phrase
# inside quoted text (echo "git push"), which is not an invocation.
printf '%s' "$COMMAND" | grep -qE '(^[[:space:]]*|[&|;(][[:space:]]*)git[[:space:]]+push' || exit 0

deny() {
    printf '%s\n' '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"BLOCKED: Cannot push to main/master. All changes go through PRs. Create a feature branch, push it, and open a PR."}}'
    exit 0
}

cd "${CLAUDE_PROJECT_DIR:-.}" 2>/dev/null || true
BRANCH=$(git branch --show-current 2>/dev/null)

if [[ "$BRANCH" == "main" || "$BRANCH" == "master" ]]; then
    deny
fi

# Scope the ref check to each `git push …` invocation's own arguments (up to
# the next shell separator), so main/master mentioned elsewhere in a compound
# command — echo text, an unrelated branch (`git push origin feat && echo
# see main`) — does not false-trigger. Within those args, deny main/master as a
# destination ref: bare (`origin main`), a refspec RHS (`HEAD:main`), or a full
# ref (`refs/heads/main`). A source-only refspec (`main:feature`, main followed
# by `:`) is not a push to the protected branch and is left alone.
PUSH_ARGS=$(printf '%s' "$COMMAND" | grep -oE 'git[[:space:]]+push[^;&|]*')
if printf '%s' "$PUSH_ARGS" | grep -qE '([[:space:]:/])(main|master)([^[:alnum:]_/:-]|$)'; then
    deny
fi

exit 0
