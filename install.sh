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

REPO="punt-labs/beadle"
BINARY="beadle-email"
INSTALL_DIR="$HOME/.local/bin"

# --- Step 1: Prerequisites ---

info "Checking prerequisites..."

if command -v curl >/dev/null 2>&1; then
  ok "curl found"
else
  fail "'curl' not found. Install curl first."
fi

# Optional: check for gpg (needed for PGP verification features)
if command -v gpg >/dev/null 2>&1; then
  ok "gpg found (PGP verification available)"
else
  warn "gpg not found — PGP signature verification will be unavailable"
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

DOWNLOAD_URL="https://github.com/$REPO/releases/latest/download/$ASSET"
CHECKSUMS_URL="https://github.com/$REPO/releases/latest/download/checksums.txt"

TMPDIR_DL="$(mktemp -d)"
cleanup_tmpdir() { rm -rf "$TMPDIR_DL"; }
trap cleanup_tmpdir EXIT INT TERM

curl -fsSL "$DOWNLOAD_URL" -o "$TMPDIR_DL/$ASSET" || fail "Failed to download $DOWNLOAD_URL"
curl -fsSL "$CHECKSUMS_URL" -o "$TMPDIR_DL/checksums.txt" || fail "Failed to download checksums"
ok "downloaded"

# --- Step 4: Verify checksum ---

info "Verifying checksum..."

MATCH_COUNT="$(grep -cF "  $ASSET" "$TMPDIR_DL/checksums.txt")"
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

mkdir -p "$INSTALL_DIR"
mv "$TMPDIR_DL/$ASSET" "$INSTALL_DIR/$BINARY"
chmod +x "$INSTALL_DIR/$BINARY"
ok "$INSTALL_DIR/$BINARY"

if ! command -v "$BINARY" >/dev/null 2>&1; then
  warn "$INSTALL_DIR is not on your PATH"
  warn "Add this to your shell profile: export PATH=\"\$HOME/.local/bin:\$PATH\""
fi

# --- Step 6: Verify ---

info "Verifying installation..."

if command -v "$BINARY" >/dev/null 2>&1; then
  VERSION="$("$BINARY" version 2>/dev/null || echo "unknown")"
  ok "$BINARY $VERSION ($(command -v "$BINARY"))"
elif [ -x "$INSTALL_DIR/$BINARY" ]; then
  ok "$INSTALL_DIR/$BINARY (not yet on PATH)"
else
  fail "$BINARY not found after installation"
fi

# --- Done ---

printf '\n%b%b%s is ready!%b\n\n' "$GREEN" "$BOLD" "$BINARY" "$NC"
printf 'Next steps:\n'
printf '  1. Configure credentials (see README for setup)\n'
printf '  2. Register MCP server in .mcp.json:\n'
printf '     {"mcpServers":{"beadle-email":{"command":"%s/%s","args":["serve"]}}}\n' "$INSTALL_DIR" "$BINARY"
printf '  3. Run doctor to verify: %s doctor\n\n' "$BINARY"
