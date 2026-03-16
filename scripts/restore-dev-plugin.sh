#!/usr/bin/env bash
set -euo pipefail

# Restore dev plugin state on main after a release tag.

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
PLUGIN_JSON="${REPO_ROOT}/.claude-plugin/plugin.json"

# Restore plugin.json from the commit before release prep (repo-relative path)
git -C "$REPO_ROOT" checkout HEAD~1 -- .claude-plugin/plugin.json
git -C "$REPO_ROOT" add .claude-plugin/plugin.json
git -C "$REPO_ROOT" commit --no-verify -m "chore: restore dev plugin state [skip ci]"
