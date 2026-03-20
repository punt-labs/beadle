#!/usr/bin/env bash
[[ -f "$HOME/.punt-hooks-kill" ]] && exit 0
# UserPromptSubmit — remind model to set up inbox polling if overdue.
#
# Reads the poll config (.claude/beadle.local.md) and last poll timestamp
# (.claude/beadle.poll.ts). If the last poll was more than 2x the configured
# interval ago (or never happened), emit a CronCreate reminder.
#
# No set -euo pipefail — hooks must degrade gracefully.

PROJECT_ROOT="$(git rev-parse --show-toplevel 2>/dev/null)"
[[ -z "$PROJECT_ROOT" ]] && exit 0

CONFIG="$PROJECT_ROOT/.claude/beadle.local.md"
POLL_TS="$PROJECT_ROOT/.claude/beadle.poll.ts"

# Read configured interval. Default 30m if no config.
INTERVAL="30m"
if [[ -f "$CONFIG" ]]; then
  VAL=$(sed -n '/^---$/,/^---$/{ /^---$/d; p; }' "$CONFIG" \
    | grep '^inbox_poll:' | head -1 | sed 's/inbox_poll: *//' | sed 's/^"\(.*\)"$/\1/' | sed 's/[[:space:]]*$//')
  [[ -n "$VAL" ]] && INTERVAL="$VAL"
fi

# Polling disabled — no reminder needed.
[[ "$INTERVAL" == "n" ]] && exit 0

# Convert interval to seconds and compute 2x threshold.
case "$INTERVAL" in
  5m)  THRESHOLD=600 ;  CRON_EXPR="*/5 * * * *" ;;
  10m) THRESHOLD=1200 ; CRON_EXPR="*/10 * * * *" ;;
  15m) THRESHOLD=1800 ; CRON_EXPR="*/15 * * * *" ;;
  30m) THRESHOLD=3600 ; CRON_EXPR="*/30 * * * *" ;;
  1h)  THRESHOLD=7200 ; CRON_EXPR="7 * * * *" ;;
  2h)  THRESHOLD=14400; CRON_EXPR="7 */2 * * *" ;;
  *)   THRESHOLD=3600 ; CRON_EXPR="*/30 * * * *" ;;
esac

# Check last poll timestamp.
NOW=$(date +%s)
if [[ -f "$POLL_TS" ]]; then
  LAST=$(cat "$POLL_TS" 2>/dev/null)
  # Validate it's a number
  if [[ "$LAST" =~ ^[0-9]+$ ]]; then
    ELAPSED=$((NOW - LAST))
    [[ $ELAPSED -lt $THRESHOLD ]] && exit 0
  fi
fi

# Overdue or never polled — emit reminder.
command -v jq >/dev/null 2>&1 || exit 0
jq -n --arg ctx "Execute: CronCreate cron='$CRON_EXPR' prompt='/inbox' recurring=true" '{
  hookSpecificOutput: {
    hookEventName: "UserPromptSubmit",
    additionalContext: $ctx
  }
}'
