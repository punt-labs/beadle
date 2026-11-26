#!/usr/bin/env bash
set -euo pipefail
exec 3>&1 >&2

if [[ $# -lt 4 ]]; then
    printf 'Run this builder via make handoff-runtime-bundle.\n' >&2
    exit 2
fi
output=$(cd -- "$1" && pwd -P)
markdownlint=$2
biff=$3
shift 3

if [[ $(uname -sm) != 'Linux x86_64' ]]; then
    printf 'This builder requires Linux x86_64.\n' >&2
    exit 2
fi

work=$(mktemp -d "$output/beadle-handoff.XXXXXX")
trap 'chmod -R u+w "$work"; rm -rf -- "$work"' EXIT
runtime=$work/runtime
mkdir -p "$runtime"/{bin,cache,go,go-tools,node/bin,node/lib/node_modules,python/bin}
cat > "$runtime/inputs"
test -s "$runtime/inputs"

go_root=$(go env GOROOT)
cp -R -L "$go_root/." "$runtime/go"
(
    export GOTOOLCHAIN=local GOPATH=$work/gopath GOMODCACHE=$work/mod GOCACHE=$work/go-build GOBIN=$work/tools
    "$runtime/go/bin/go" mod download
    mv "$GOMODCACHE/cache/download" "$runtime/cache/go-download"
    for tool in "$@"; do
        "$runtime/go/bin/go" install -trimpath "$tool"
        versioned=${tool##*/}
        mv "$GOBIN/${versioned%@*}" "$runtime/go-tools/$versioned"
    done
    mkdir -p "$work/offline-mod/cache"
    cp -R "$runtime/cache/go-download" "$work/offline-mod/cache/download"
    GOPROXY=off GOMODCACHE="$work/offline-mod" "$runtime/go/bin/go" list -deps -test ./... > /dev/null
)

node=$(node -p 'process.execPath')
node_root=$(dirname -- "$(dirname -- "$node")")
install -m 0755 "$node" "$runtime/node/bin/node"
install -m 0644 "$node_root/LICENSE" "$runtime/node/LICENSE"
cp -R -L "$node_root/lib/node_modules/npm" "$runtime/node/lib/node_modules"
cat > "$runtime/bin/npx" <<'EOF'
#!/bin/sh
runtime=$(CDPATH='' cd -- "${0%/*}/.." && pwd -P) || exit
exec "$runtime/node/bin/node" "$runtime/node/lib/node_modules/npm/bin/npx-cli.js" "$@"
EOF
chmod +x "$runtime/bin/npx"
(
    cd "$work"
    export PATH="$runtime/bin:$runtime/node/bin:$PATH"
    export npm_config_cache=$runtime/cache/npm npm_config_ignore_scripts=true
    printf '# Runtime smoke test\n' > smoke.md
    npx --yes "markdownlint-cli2@$markdownlint" smoke.md
    npm_config_offline=true npx --yes "markdownlint-cli2@$markdownlint" smoke.md
)
rm -rf -- "$runtime/cache/npm/_logs"
rm -f -- "$runtime/cache/npm/_update-notifier-last-checked"

uv python install 3.13.18 --no-bin
python=$(uv python find --managed-python --no-project --system 3.13.18)
python_root=$("$python" -I -c 'import sys; print(sys.base_prefix)')
install -m 0755 "$python" "$runtime/python/bin/python3.13"
cp -R -L "$python_root/lib" "$runtime/python/lib"
# nats-py needs nkeys, which only publishes a source distribution.
uv pip install --only-binary :all: --no-binary nkeys --python "$python" --target "$runtime/biff" "punt-biff==$biff"
cat > "$runtime/bin/biff" <<'EOF'
#!/bin/sh
runtime=$(CDPATH='' cd -- "${0%/*}/.." && pwd -P) || exit
PYTHONPATH="$runtime/biff" exec "$runtime/python/bin/python3.13" -m biff "$@"
EOF
chmod +x "$runtime/bin/biff"
PYTHONDONTWRITEBYTECODE=1 "$runtime/bin/biff" --help > /dev/null

LC_ALL=C tar --sort=name --format=gnu --mtime=@0 --numeric-owner \
    --owner=0 --group=0 --hard-dereference -C "$runtime" -cf - bin biff cache go go-tools inputs node python \
    | gzip -n > "$work/archive.tar.gz"
read -r digest _ < <(sha256sum "$work/archive.tar.gz")
archive=$output/$digest.tar.gz
mv -- "$work/archive.tar.gz" "$archive"
printf '%s\n' "$archive" >&3
