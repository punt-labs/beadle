#!/bin/sh
# Install beadle-email — MCP server for email communication via Proton Bridge.
# Usage: curl -fsSL https://raw.githubusercontent.com/punt-labs/beadle/main/install.sh | sh
set -eu

# --- Colors (disabled when not a terminal) ---
if [ -t 1 ]; then
  BOLD='\033[1m' GREEN='\033[32m' YELLOW='\033[33m' NC='\033[0m'
else
  BOLD='' GREEN='' YELLOW='' NC=''
fi

info() { printf '%b▶%b %s\n' "$BOLD" "$NC" "$1"; }
ok()   { printf '  %b✓%b %s\n' "$GREEN" "$NC" "$1"; }
warn() { printf '  %b!%b %s\n' "$YELLOW" "$NC" "$1"; }
fail() { printf '  %b✗%b %s\n' "$YELLOW" "$NC" "$1"; exit 1; }

VERSION="0.9.0"
REPO="punt-labs/beadle"
BINARY="beadle-email"
INSTALL_DIR="$HOME/.local/bin"
MARKETPLACE_REPO="punt-labs/claude-plugins"
MARKETPLACE_NAME="punt-labs"

# --- Step 1: Prerequisites ---

info "Checking prerequisites..."

if command -v claude >/dev/null 2>&1; then
  ok "claude CLI found"
else
  fail "'claude' CLI not found. Install Claude Code first: https://docs.anthropic.com/en/docs/claude-code"
fi

if command -v git >/dev/null 2>&1; then
  ok "git found"
else
  fail "'git' not found. Install git first: https://git-scm.com/downloads"
fi

if command -v curl >/dev/null 2>&1; then
  ok "curl found"
else
  fail "'curl' not found. Install curl first."
fi

# --- Step 2: Detect platform ---

info "Detecting platform..."

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"

case "$OS" in
  darwin) ;;
  linux)  ;;
  *)      fail "Unsupported OS: $OS (only darwin and linux are supported)" ;;
esac

case "$ARCH" in
  x86_64)  ARCH="amd64" ;;
  aarch64) ARCH="arm64" ;;
  arm64)   ;;
  *)       fail "Unsupported architecture: $ARCH (only amd64 and arm64 are supported)" ;;
esac

ASSET="${BINARY}-${OS}-${ARCH}"
ok "$OS/$ARCH"

# --- Step 3: Download binary ---

info "Downloading $BINARY..."

DOWNLOAD_URL="https://github.com/$REPO/releases/download/v${VERSION}/$ASSET"
CHECKSUMS_URL="https://github.com/$REPO/releases/download/v${VERSION}/checksums.txt"

TMPDIR_DL="$(mktemp -d)"
cleanup() { rm -rf "$TMPDIR_DL"; }
trap cleanup EXIT INT TERM

curl -fsSL "$DOWNLOAD_URL" -o "$TMPDIR_DL/$ASSET" || fail "Failed to download $DOWNLOAD_URL"
curl -fsSL "$CHECKSUMS_URL" -o "$TMPDIR_DL/checksums.txt" || fail "Failed to download checksums"
ok "downloaded"

# --- Step 4: Verify checksum ---

info "Verifying checksum..."

MATCH_COUNT="$(grep -cF "  $ASSET" "$TMPDIR_DL/checksums.txt" || true)"
if [ "$MATCH_COUNT" -ne 1 ]; then
  fail "Expected exactly 1 checksum for $ASSET, found $MATCH_COUNT"
fi
EXPECTED="$(grep -F "  $ASSET" "$TMPDIR_DL/checksums.txt" | awk '{print $1}')"

if command -v sha256sum >/dev/null 2>&1; then
  ACTUAL="$(sha256sum "$TMPDIR_DL/$ASSET" | awk '{print $1}')"
elif command -v shasum >/dev/null 2>&1; then
  ACTUAL="$(shasum -a 256 "$TMPDIR_DL/$ASSET" | awk '{print $1}')"
else
  fail "Neither sha256sum nor shasum found — cannot verify download integrity"
fi

if [ "$ACTUAL" != "$EXPECTED" ]; then
  fail "Checksum mismatch: expected $EXPECTED, got $ACTUAL"
fi
ok "SHA256 verified"

# --- Step 5: Install binary ---

info "Installing to $INSTALL_DIR..."

mkdir -p "$INSTALL_DIR" || fail "Failed to create $INSTALL_DIR"
mv "$TMPDIR_DL/$ASSET" "$INSTALL_DIR/$BINARY" || fail "Failed to move binary to $INSTALL_DIR"
chmod +x "$INSTALL_DIR/$BINARY" || fail "Failed to make $BINARY executable"
ok "$INSTALL_DIR/$BINARY"

if ! command -v "$BINARY" >/dev/null 2>&1; then
  warn "$INSTALL_DIR is not on your PATH"
  warn "Add this to your shell profile: export PATH=\"\$HOME/.local/bin:\$PATH\""
fi

# --- Step 6: Register marketplace ---

info "Registering Punt Labs marketplace..."

if claude plugin marketplace list < /dev/null 2>/dev/null | grep -q "$MARKETPLACE_NAME"; then
  ok "marketplace already registered"
  claude plugin marketplace update "$MARKETPLACE_NAME" < /dev/null 2>/dev/null || true
else
  claude plugin marketplace add "$MARKETPLACE_REPO" < /dev/null || fail "Failed to register marketplace"
  ok "marketplace registered"
fi

# --- Step 7: Install plugin ---

info "Installing beadle plugin..."

HTTPS_ENV=""
if ! ssh -n -o StrictHostKeyChecking=yes -o BatchMode=yes -o ConnectTimeout=5 -T git@github.com 2>&1 | grep -q "successfully authenticated"; then
  warn "SSH auth to GitHub unavailable, using HTTPS fallback"
  HTTPS_ENV="GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=url.https://github.com/.insteadOf GIT_CONFIG_VALUE_0=git@github.com:"
fi

PLUGIN_INSTALLED=0
# Uninstall first so re-running install.sh actually upgrades. `claude plugin
# install` is a no-op when the plugin is already cached at any version, so
# without this, users stay on whatever version they first installed forever.
# Pattern: biff/install.sh, quarry/install.sh, vox/install.sh.
claude plugin uninstall "beadle@$MARKETPLACE_NAME" < /dev/null 2>/dev/null || true
if env $HTTPS_ENV claude plugin install "beadle@$MARKETPLACE_NAME" --scope user < /dev/null 2>/dev/null; then
  ok "beadle plugin installed"
  PLUGIN_INSTALLED=1
else
  warn "Failed to install plugin (install manually: claude plugin install beadle@$MARKETPLACE_NAME)"
fi

# --- Step 8: Register MCP server ---
# Only register standalone MCP server if plugin install failed (plugin.json declares mcpServers)

if [ "$PLUGIN_INSTALLED" = "0" ]; then
  info "Registering MCP server (fallback)..."
  if claude mcp get "$BINARY" < /dev/null 2>/dev/null | grep -q "$BINARY"; then
    ok "MCP server already registered"
  else
    claude mcp add -s user "$BINARY" -- "$INSTALL_DIR/$BINARY" serve < /dev/null || fail "Failed to register MCP server"
    ok "MCP server registered (user scope)"
  fi
fi

# --- Step 9: Verify ---

info "Verifying installation..."

if command -v "$BINARY" >/dev/null 2>&1; then
  ok "$BINARY $(command -v "$BINARY")"
elif [ -x "$INSTALL_DIR/$BINARY" ]; then
  ok "$INSTALL_DIR/$BINARY (not yet on PATH)"
else
  fail "$BINARY not found after installation"
fi

# --- Step 10: Health check ---

info "Running doctor..."

doctor_rc=0
if command -v "$BINARY" >/dev/null 2>&1; then
  "$BINARY" doctor || doctor_rc=$?
elif [ -x "$INSTALL_DIR/$BINARY" ]; then
  "$INSTALL_DIR/$BINARY" doctor || doctor_rc=$?
fi

# --- Done ---

if [ "$doctor_rc" -eq 0 ]; then
  printf '\n%b%b%s is ready!%b\n\n' "$GREEN" "$BOLD" "$BINARY" "$NC"
else
  printf '\n%b%b%s installed but doctor reported issues.%b\n\n' "$YELLOW" "$BOLD" "$BINARY" "$NC"
  info "Next steps:"
  printf '\n'
  if [ "$OS" = "darwin" ]; then
    printf '  Store your Proton Bridge password:\n'
    printf "    security add-generic-password -s beadle -a imap-password -w '%bYOUR_BRIDGE_PASSWORD%b'\n\n" "$BOLD" "$NC"
    printf '  Store your Resend API key (for external email):\n'
    printf "    security add-generic-password -s beadle -a resend-api-key -w '%bYOUR_RESEND_KEY%b'\n\n" "$BOLD" "$NC"
    printf '  Store your GPG passphrase:\n'
    printf "    security add-generic-password -s beadle -a gpg-passphrase -w '%bYOUR_GPG_PASSPHRASE%b'\n\n" "$BOLD" "$NC"
  else
    printf '  Store your Proton Bridge password:\n'
    printf '    mkdir -p ~/.punt-labs/beadle/secrets\n'
    printf "    printf '%%s' '%bYOUR_BRIDGE_PASSWORD%b' > ~/.punt-labs/beadle/secrets/imap-password\n" "$BOLD" "$NC"
    printf '    chmod 600 ~/.punt-labs/beadle/secrets/imap-password\n\n'
    printf '  Store your Resend API key (for external email):\n'
    printf "    printf '%%s' '%bYOUR_RESEND_KEY%b' > ~/.punt-labs/beadle/secrets/resend-api-key\n" "$BOLD" "$NC"
    printf '    chmod 600 ~/.punt-labs/beadle/secrets/resend-api-key\n\n'
    printf '  Store your GPG passphrase:\n'
    printf "    printf '%%s' '%bYOUR_GPG_PASSPHRASE%b' > ~/.punt-labs/beadle/secrets/gpg-passphrase\n" "$BOLD" "$NC"
    printf '    chmod 600 ~/.punt-labs/beadle/secrets/gpg-passphrase\n\n'
  fi
  printf '  Then verify: %s doctor\n\n' "$BINARY"
fi
printf 'Restart Claude Code to activate.\n\n'
