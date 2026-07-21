#!/bin/bash
# PostToolUse Write|Edit — run the quality gate after an agent edits a file,
# blocking (exit 2, output on stderr) when it fails.
#
# Shared by every agent definition so the logic lives in one place. Scoped by
# the edited file's type: Go/module/Makefile/shell/YAML edits run the full
# `make check`; Markdown edits run `make docs` (markdownlint); vendored trees
# (.tmp/, .punt-labs/ethos/) and unrelated files are skipped. If jq is
# unavailable the file type cannot be read, so it runs the full gate.

REPO="${CLAUDE_PROJECT_DIR:-.}"

# Run a make target; on failure, emit the tail on stderr and block with exit 2.
gate() {
    local out rc
    out=$(cd "$REPO" && make "$1" 2>&1)
    rc=$?
    if [ "$rc" -ne 0 ]; then
        printf '%s\n' "$out" | tail -n 60 >&2
        exit 2
    fi
    exit 0
}

if ! command -v jq >/dev/null 2>&1; then
    gate check
fi

path=$(jq -r '.tool_input.file_path // empty' 2>/dev/null)
if [ -z "$path" ]; then
    gate check
fi

case "$path" in
    */.tmp/*|*/.punt-labs/ethos/*|.tmp/*|.punt-labs/ethos/*) exit 0 ;;
    *.md|*.markdown) gate docs ;;
    *.go|*go.mod|*go.sum|*go.work|*Makefile|*.sh|*.yaml|*.yml) gate check ;;
    *) exit 0 ;;
esac
