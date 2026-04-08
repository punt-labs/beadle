# New Machine Setup Guide

Set up `beadle-email` on a fresh machine. This guide covers the
complete path from zero to a working MCP server — not just the binary,
but also Proton Bridge, credentials, and identity.

**Time:** 15-20 minutes (mostly waiting for Proton Bridge to sync).

**Audience:** Punt Labs team members running `beadle-email` as a Claude
Code plugin. If you're using it standalone, the steps are the same but
skip the plugin registration in Step 5.

## Prerequisites

| Dependency | Check | Install |
|------------|-------|---------|
| GPG | `gpg --version` | `sudo apt install gnupg` (Linux) or `brew install gnupg` (macOS) |
| Claude Code | `claude --version` | [docs.anthropic.com](https://docs.anthropic.com/en/docs/claude-code) |
| Git | `git --version` | `sudo apt install git` (Linux) or `brew install git` (macOS) |
| curl | `curl --version` | `sudo apt install curl` (Linux) or pre-installed (macOS) |

## Step 1: Install Proton Bridge

Beadle reads and sends email through [Proton
Bridge](https://proton.me/mail/bridge), which exposes your Proton
mailbox as a local IMAP/SMTP server. This is a separate application from
the Proton Mail desktop app.

### Linux (DEB)

```bash
# Download from https://proton.me/mail/bridge#download
# Or install the .deb directly:
sudo dpkg -i protonmail-bridge_*.deb
sudo apt-get install -f   # resolve dependencies if needed
```

### macOS

```bash
brew install --cask protonmail-bridge
```

### First launch

1. Open Proton Bridge (`protonmail-bridge` or from Applications).
2. Sign in with your Proton account.
3. Wait for the initial mailbox sync to complete (can take several
   minutes for large mailboxes).
4. Open **Settings** (gear icon) > note the **IMAP port** (default
   1143) and **SMTP port** (default 1025).
5. Click your account name > **Mailbox details** > copy the **Bridge
   password**. This is *not* your Proton account password — Bridge
   generates a separate password for IMAP/SMTP access.

> **Keep Bridge running.** Beadle connects to Bridge on
> `127.0.0.1:1143` (IMAP) and `127.0.0.1:1025` (SMTP). If Bridge
> isn't running, all email operations fail.

## Step 2: Install beadle-email

### Option A: Install script (recommended)

```bash
curl -fsSL https://raw.githubusercontent.com/punt-labs/beadle/main/install.sh | sh
```

This downloads the binary, verifies the SHA256 checksum, registers the
Claude Code plugin, and runs `doctor`. If doctor reports issues,
continue to Step 3.

### Option B: Build from source

```bash
cd /path/to/beadle
make build
make install   # copies binary to ~/.local/bin/
```

### Verify

```bash
beadle-email version
# Expected: beadle-email 0.9.0
```

If `beadle-email: command not found`, add `~/.local/bin` to your PATH:

```bash
echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.bashrc
source ~/.bashrc
```

## Step 3: Configure connection

Create the config directory and connection file:

```bash
mkdir -p ~/.punt-labs/beadle/secrets
```

Write `~/.punt-labs/beadle/email.json` with your connection parameters.
Replace `you@example.com` with your Proton email address:

```json
{
  "imap_user": "you@example.com",
  "from_address": "you@example.com"
}
```

The defaults for omitted fields are:

| Field | Default | Override when |
|-------|---------|---------------|
| `imap_host` | `127.0.0.1` | Bridge runs on a different host |
| `imap_port` | `1143` | Bridge uses a non-default IMAP port |
| `smtp_port` | `1025` | Bridge uses a non-default SMTP port |
| `gpg_binary` | `gpg` | GPG is at a non-standard path |
| `gpg_signer` | same as `from_address` | Signing key differs from from address |

Alternatively, `beadle-email install` runs an interactive wizard that
prompts for each field and writes the config file.

## Step 4: Store credentials

Beadle resolves credentials at runtime through a priority chain.
Choose one method per credential.

### Required

**IMAP password** — the Bridge password from Step 1.5 (not your Proton
account password):

<!-- markdownlint-disable MD013 -->

| Method | Command |
|--------|---------|
| pass (Linux, recommended) | `pass insert beadle/imap-password` |
| secret-tool (Linux, fallback) | `secret-tool store --label='beadle imap-password' service beadle account imap-password` |
| macOS Keychain | `security add-generic-password -s beadle -a imap-password -w 'BRIDGE_PASSWORD'` |
| Secret file | `printf '%s' 'BRIDGE_PASSWORD' > ~/.punt-labs/beadle/secrets/imap-password && chmod 600 ~/.punt-labs/beadle/secrets/imap-password` |
| Environment variable | `export BEADLE_IMAP_PASSWORD='BRIDGE_PASSWORD'` |

<!-- markdownlint-enable MD013 -->

### Optional

**Resend API key** — enables sending email to non-Proton addresses when
SMTP is unavailable. Get a key at [resend.com](https://resend.com):

<!-- markdownlint-disable MD013 -->

| Method | Command |
|--------|---------|
| pass (Linux, recommended) | `pass insert beadle/resend-api-key` |
| secret-tool (Linux, fallback) | `secret-tool store --label='beadle resend-api-key' service beadle account resend-api-key` |
| macOS Keychain | `security add-generic-password -s beadle -a resend-api-key -w 'KEY'` |
| Secret file | `printf '%s' 'KEY' > ~/.punt-labs/beadle/secrets/resend-api-key && chmod 600 ~/.punt-labs/beadle/secrets/resend-api-key` |
| Environment variable | `export BEADLE_RESEND_API_KEY='KEY'` |

<!-- markdownlint-enable MD013 -->

**GPG passphrase** — required only if your GPG signing key has a
passphrase and you want beadle to sign outgoing messages:

<!-- markdownlint-disable MD013 -->

| Method | Command |
|--------|---------|
| pass (Linux, recommended) | `pass insert beadle/gpg-passphrase` |
| secret-tool (Linux, fallback) | `secret-tool store --label='beadle gpg-passphrase' service beadle account gpg-passphrase` |
| macOS Keychain | `security add-generic-password -s beadle -a gpg-passphrase -w 'PASSPHRASE'` |
| Secret file | `printf '%s' 'PASSPHRASE' > ~/.punt-labs/beadle/secrets/gpg-passphrase && chmod 600 ~/.punt-labs/beadle/secrets/gpg-passphrase` |
| Environment variable | `export BEADLE_GPG_PASSPHRASE='PASSPHRASE'` |

<!-- markdownlint-enable MD013 -->

### Credential priority

Beadle checks in this order and uses the first one found:

1. **OS keychain**
   - macOS: Keychain via `security` CLI
   - Linux: `pass` first (GPG-encrypted at rest with your own key,
     matches Proton Bridge's own vault backend), then `secret-tool`
     (libsecret / GNOME Keyring) as fallback
2. **Secret file** — `~/.punt-labs/beadle/secrets/<name>`, must be mode
   600 (no group/world read).
3. **Environment variable** — `BEADLE_IMAP_PASSWORD`,
   `BEADLE_RESEND_API_KEY`, `BEADLE_GPG_PASSPHRASE`.

## Step 5: Register with Claude Code

If you used the install script (Step 2, Option A), the plugin is
already registered. Verify with:

```bash
claude plugin list 2>/dev/null | grep beadle
```

If not registered, install the plugin:

```bash
claude plugin marketplace add punt-labs/claude-plugins
claude plugin install beadle@punt-labs --scope user
```

Or register the standalone MCP server (no slash commands or hooks):

```bash
claude mcp add -s user beadle-email -- beadle-email serve
```

Restart Claude Code after registration.

## Step 6: Import GPG keys

If you sign email or verify signatures from known contacts, import the
relevant keys:

```bash
# Import your own key (if not already on this machine)
gpg --import /path/to/your-private-key.asc

# Import contact public keys
gpg --import /path/to/contact-public-key.asc

# Verify your key is present
gpg --list-keys your@email.com
```

## Step 7: Set up contacts (optional)

Beadle's address book controls who can send you email and what the
agent can do with it. Without contacts, messages from unknown senders
appear redacted.

```bash
# Via CLI
beadle-email contact add --name "Alice" --email alice@example.com

# Via MCP tool (from Claude Code)
# Use the add_contact tool with name, email, and permissions
```

## Step 8: Verify

Run the health check:

```bash
beadle-email doctor
```

All items should show `[+]`. Common issues:

<!-- markdownlint-disable MD013 -->

| Doctor output | Fix |
|---------------|-----|
| `[!] imap_password` | Re-check Step 4. Verify the credential exists (in pass, secret-tool, or the secret file) and contains the Bridge password — not your Proton account password. |
| `[!] resend_api_key` | Optional — only needed for Resend fallback sending. |
| `[!] gpg_passphrase` | Optional — only needed if your GPG signing key has a passphrase. |
| `[+] smtp ... not reachable` | Bridge isn't running or uses a different SMTP port. Check Step 1. |
| `[+] identity ... no identity` | See Troubleshooting > Identity below. |

<!-- markdownlint-enable MD013 -->

Then test from Claude Code:

```text
/inbox
```

## Step 9: Identity setup (Punt Labs agents)

This step is only for Punt Labs agents whose identity is managed by
ethos.

Beadle resolves identity from the ethos sidecar. On a new machine, the
team submodule may not be initialized:

```bash
# In any punt-labs repo with the team submodule:
cd /path/to/your-repo
git submodule init
git submodule update
```

This populates `.punt-labs/ethos/` with the team registry (identities,
personalities, writing styles, etc.).

For the global ethos directory (`~/.punt-labs/ethos/`), which is used
outside of git repos, the ethos session hooks create it automatically
on first Claude Code launch. If identity resolution fails, check:

```bash
cat ~/.punt-labs/ethos/active
# Should contain your handle (e.g., "jfreeman" or "claude")

ls ~/.punt-labs/ethos/identities/
# Should contain <handle>.yaml
```

## Troubleshooting

### "command not found" after install

`~/.local/bin` is not on your PATH. Add it:

```bash
# bash
echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.bashrc

# zsh
echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.zshrc
```

### Bridge password vs. Proton password

The IMAP password is the **Bridge password**, not your Proton account
password. Open Proton Bridge > click your account > **Mailbox details**
to find it. It looks like a random string (e.g.,
`a1b2c3d4e5f6g7h8i9j0k1l2`).

### Bridge not reachable

```bash
# Check Bridge is running
ps aux | grep -i bridge

# Test IMAP connectivity
nc -zv 127.0.0.1 1143

# Test SMTP connectivity
nc -zv 127.0.0.1 1025
```

If ports differ from 1143/1025, update `imap_port` and `smtp_port` in
`~/.punt-labs/beadle/email.json`.

### Secret file permission denied

Secret files must be mode 600 (owner read/write only):

```bash
chmod 600 ~/.punt-labs/beadle/secrets/*
ls -la ~/.punt-labs/beadle/secrets/
# Should show -rw------- for each file
```

### Identity resolution fails

If doctor shows `no identity`, beadle can still function — it falls
back to the `from_address` in `email.json`. Identity resolution is
needed for multi-identity features and contact permissions scoped by
identity.

To fix: ensure the ethos active file and identity YAML exist:

```bash
# Check active identity
cat ~/.punt-labs/ethos/active

# Check identity file exists
ls ~/.punt-labs/ethos/identities/$(cat ~/.punt-labs/ethos/active).yaml
```

## Quick reference

| What | Where |
|------|-------|
| Binary | `~/.local/bin/beadle-email` |
| Config | `~/.punt-labs/beadle/email.json` |
| Secrets | `~/.punt-labs/beadle/secrets/` |
| Contacts | `~/.punt-labs/beadle/contacts.json` |
| Identities | `~/.punt-labs/beadle/identities/<email>/` |
| Attachments | `~/.punt-labs/beadle/identities/<email>/attachments/` |
| Ethos | `~/.punt-labs/ethos/` |
| Logs | stderr (when running as MCP server) |
