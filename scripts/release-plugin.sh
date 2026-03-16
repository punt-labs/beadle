#!/usr/bin/env bash
set -euo pipefail

# Prepare plugin for release: swap name to prod.
# The tagged commit has only prod artifacts; the marketplace cache clones from it.
#
# Unlike punt-kit's version, beadle has no -dev command files to remove —
# only the plugin name swap is needed.

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
PLUGIN_JSON="${REPO_ROOT}/.claude-plugin/plugin.json"

# Swap plugin name from *-dev to prod
current_name="$(PLUGIN_JSON="$PLUGIN_JSON" python3 -c "import json, os; print(json.load(open(os.environ['PLUGIN_JSON']))['name'])")"
prod_name="${current_name%-dev}"

if [[ "$current_name" == "$prod_name" ]]; then
  echo "Plugin name is already '${prod_name}' (no -dev suffix)" >&2
  exit 1
fi

echo "Swapping plugin name: ${current_name} → ${prod_name}"
PLUGIN_JSON="$PLUGIN_JSON" PROD_NAME="$prod_name" python3 -c "
import json, pathlib, os
p = pathlib.Path(os.environ['PLUGIN_JSON'])
d = json.loads(p.read_text())
d['name'] = os.environ['PROD_NAME']
p.write_text(json.dumps(d, indent=2) + '\n')
"

git -C "$REPO_ROOT" add .claude-plugin/plugin.json
git -C "$REPO_ROOT" commit --no-verify -m "chore: prepare plugin for release"
