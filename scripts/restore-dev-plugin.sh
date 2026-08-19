#!/usr/bin/env bash
set -euo pipefail

# Restore dev plugin state on main after a release tag.

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"

# Restore plugin.json from the commit before release prep
git -C "$REPO_ROOT" checkout HEAD~1 -- plugin/.claude-plugin/plugin.json
git -C "$REPO_ROOT" add plugin/.claude-plugin/plugin.json
git -C "$REPO_ROOT" commit --no-verify -m "chore: restore dev plugin state [skip ci]"
