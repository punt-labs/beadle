# beadle

> Email for your AI engineering assistant.

[![License](https://img.shields.io/github/license/punt-labs/beadle)](LICENSE)
[![CI](https://img.shields.io/github/actions/workflow/status/punt-labs/beadle/test.yml?label=CI)](https://github.com/punt-labs/beadle/actions/workflows/test.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/punt-labs/beadle.svg)](https://pkg.go.dev/github.com/punt-labs/beadle)
[![Working Backwards](https://img.shields.io/badge/Working_Backwards-hypothesis-lightgrey)](./prfaq.pdf)

beadle-email is an MCP (Model Context Protocol) server that connects Claude
Code, or any other MCP client, to an existing mailbox over IMAP and SMTP, so an
AI coding assistant can read and send mail from that address. It labels every
message with one of four [trust levels](#trust-model), from Proton-to-Proton
end-to-end encryption down to no signature, and per-contact
[permissions](#contact-permissions) decide which senders the assistant may read
and which recipients it may write to. Releases also publish `beadle-daemon`
binaries, a separate background service; the installer does not install them
and this README does not cover them.

**Platforms:** macOS, Linux

## Diagnostic Logging

The filesystem namespace for `beadle-email` and `beadle-daemon` is `beadle`:
both use `~/.punt-labs/beadle/` for their machine-local state.

All beadle processes share `~/.punt-labs/beadle/logs/beadle.log`, using the
private rotating handler from `punt-observe-go`. The directory is mode `0700`;
log files are mode `0600`. Rotation keeps five backups, each bounded to 5 MiB.
Logs contain operation metadata and byte counts, not email content, addresses,
credentials, or worker stdout. Provider errors and bounded subprocess stderr
are escaped; the shared handler redacts recognizable sensitive metadata.
Failures that can quote email-derived content retain safe summaries instead.

The durable level defaults to Info. `-v`/`--verbose` or `BEADLE_DEBUG=1` enables
Debug; `-q` controls command output, not durable logging. Diagnostics are not
teed to stderr. Logging initialization failures are reported on stderr and stop
the command. MCP stdout remains JSON-RPC only, including when stderr is discarded.

## Usage Telemetry

Beadle records private, local usage spans in
`~/.punt-labs/beadle/telemetry.db` (mode `0600`, 90-day retention).
Records contain only schema version, timestamps, elapsed milliseconds, a
closed-set command/tool name, and an outcome. No arguments, email content,
addresses, paths, identifiers, errors, or results are recorded. Telemetry is
independent of diagnostic logs; Beadle does not enable network export.

Set either `PUNT_TELEMETRY=off` or `BEADLE_TELEMETRY=off` to disable capture.
Alternatively, add `"telemetry_disabled": true` to either tool-wide
`~/.punt-labs/beadle/email.json` or `~/.punt-labs/beadle/daemon.json`.
Either file vetoes capture for all Beadle processes; identity-scoped files and
explicit mailbox `--config` files do not control this machine-wide setting.
Missing files are allowed; unreadable or malformed files disable capture.
Disabled capture creates no telemetry database. Recording is non-blocking,
and exit allows at most one second for a best-effort flush.

## Quick Start

```bash
curl -fsSL https://raw.githubusercontent.com/punt-labs/beadle/355710a/install.sh | sh
```

The installer downloads the `beadle-email` binary to `~/.local/bin`, checks
its SHA256 checksum, installs the beadle Claude Code plugin, and runs
`beadle-email doctor`. The plugin provides the MCP tools, the slash commands
(`/inbox`, `/mail`, `/send`, `/contacts`, `/beadle`), and hooks. If the plugin
install fails, the installer registers `beadle-email` as a standalone MCP
server instead, which provides the MCP tools only. Restart Claude Code
afterwards.

If you registered `beadle-email` yourself earlier with `claude mcp add`, remove
that entry so the tools are not registered twice:

```bash
claude mcp remove beadle-email
```

<details>
<summary>Inspect before running</summary>

```bash
curl -fsSL https://raw.githubusercontent.com/punt-labs/beadle/355710a/install.sh -o install.sh
```

```bash
cat install.sh
```

```bash
sh install.sh
```

</details>

<details>
<summary>Manual install (other MCP clients)</summary>

```bash
mkdir -p ~/.local/bin
```

```bash
curl -fsSL https://github.com/punt-labs/beadle/releases/latest/download/beadle-email-darwin-arm64 -o ~/.local/bin/beadle-email
```

```bash
chmod +x ~/.local/bin/beadle-email
```

Replace `darwin-arm64` with your platform: `darwin-amd64`, `linux-arm64`, `linux-amd64`.
Make sure `~/.local/bin` is on your `PATH`, then configure your MCP client to run `beadle-email serve`.

</details>

<details>
<summary>Prerequisites</summary>

- An IMAP server for reading (Proton Bridge on localhost, Fastmail, Gmail IMAP, and others)
- An SMTP server for sending, using STARTTLS or implicit TLS on port 465 (Proton Bridge or any other SMTP server)
- [GPG](https://gnupg.org/) for signature verification
- (Optional) a [Resend](https://resend.com) API key for fallback sending

</details>

## Features

- **Email tools for the assistant** --- list, search, read, reply, send, mark read or unread, move, list folders, and download attachments; see [MCP Tools](#mcp-tools)
- **Trust levels** --- every message is labelled `trusted` (Proton-to-Proton end-to-end encryption), `verified` (valid PGP signature), `untrusted` (invalid PGP signature), or `unverified` (no signature); `list_messages` runs `gpg --verify` on signed messages
- **Per-contact permissions** --- the address book is an allow list: the MCP tools read only mail from contacts granted `r` and send only to contacts granted `w`
- **Multiple identities** --- one default address, or several identities through [ethos](https://github.com/punt-labs/ethos) with a fixed identity per repo and the `switch_identity` tool
- **Slash commands** (plugin only) --- `/inbox`, `/mail`, `/send`, `/contacts`, `/beadle`
- **Short tool output** (plugin only) --- a hook shows each tool result as a one-line summary in the Claude Code panel; the full result stays available to the assistant
- **Proton Bridge or any IMAP/SMTP server** --- IMAP STARTTLS for reading, SMTP for sending, with an optional Resend API fallback for unsigned mail
- **Credentials kept out of config files** --- passwords and keys are read at run time from the OS keychain, `pass`, `secret-tool`, a secret file, or an environment variable
- **Health checks** --- `doctor` checks dependencies and configuration; `status` shows the active identity and configuration

## What It Looks Like

Output from the installer's final step (version numbers vary):

```text
$ curl -fsSL https://raw.githubusercontent.com/punt-labs/beadle/355710a/install.sh | sh
...
▶ Running doctor...
[+] version          X.Y.Z
[+] identity         you@example.com (source: ethos)
[+] secret_backends  pass, secret-tool, file (~/.punt-labs/beadle/secrets/), environment variable
[+] config           ~/.punt-labs/beadle/identities/you@example.com/email.json
[+] imap_password
[+] resend_api_key
[+] gpg              /usr/bin/gpg
[+] gpg_signing_key  you@example.com
[+] gpg_passphrase   not required (you@example.com has no passphrase — filesystem access grants signing authority)
[+] smtp             127.0.0.1:1025
[+] contacts         2 contacts at ~/.punt-labs/beadle/identities/you@example.com/contacts.json
[+] mcp_registration plugin provides the MCP server (no standalone duplicate)

beadle-email vX.Y.Z is ready!
```

## MCP Tools

| Tool | Purpose |
|------|--------|
| `list_messages` | List messages with trust levels. PGP signatures verified inline. Repo-scoped by default; `all_repos` widens. Paginate with `offset`. |
| `search_messages` | Search the mailbox by from / subject / since / text (server-side IMAP SEARCH). Repo-scoped by default; `all_repos` widens. |
| `read_message` | Read full message body, headers, attachments, and trust classification. Does not mark the message read. |
| `reply_message` | Reply to a message: threads via In-Reply-To/References, preserves the `Re:` + repo tag, quotes the original. Write-gated like `send_email`. |
| `send_email` | Send via SMTP, falling back to the Resend API if SMTP fails and signing is off. Resolves contact names to addresses. Write-gated. |
| `mark_message` / `batch_mark_messages` | Mark one or many messages read or unread. Reports the number actually changed. |
| `move_message` | Move a message to another folder. Defaults to Archive. |
| `batch_move_messages` | Move multiple messages to another folder in one call. Returns the number actually moved. |
| `list_folders` | List all IMAP mailbox folders. |
| `show_mime` | Inspect multipart MIME structure, PGP parts, and attachments. |
| `verify_signature` | Verify PGP signature on a message. Returns signer info and key ID. |
| `check_trust` | Detailed trust classification with encryption type, origin analysis, and identity permission for sender. |
| `download_attachment` | Extract an attachment by MIME part index (from `show_mime`). Saves to identity-scoped directory. |
| `list_contacts` | List all contacts with effective permissions for the active identity. |
| `find_contact` | Look up a contact by name, email, or alias. Shows effective permissions. |
| `add_contact` | Add a contact (name, email, aliases, GPG key ID, permissions). |
| `remove_contact` | Remove a contact by name. |
| `whoami` | Return the active identity (email, display name, ethos handle). |
| `switch_identity` | Switch the active identity for this session. Pass an ethos handle or empty to reset. |
| `enable` | Turn beadle's repo guidance on or off in this repo (`action`: `enable` or `disable`). Writes or removes the same files as the CLI `enable`/`disable`. |
| `set_poll_interval` | Set automatic inbox polling interval (1m, 2m, 3m, 5m, 10m, 15m, 30m, 1h, 2h) or disable (`n`). |
| `get_poll_status` | Return current polling configuration: interval, active state, last check time, unseen count. |

## Commands

### Slash Commands

Available when installed as a Claude Code plugin.

| Command | What it does |
|---------|-------------|
| `/inbox` | Check the inbox. Takes an optional plain-English filter. |
| `/inbox 3m` | Set the inbox polling interval (1m, 2m, 3m, 5m, 10m, 15m, 30m, 1h, 2h). |
| `/inbox n` | Turn off automatic inbox polling. |
| `/inbox status` | Show the current polling configuration. |
| `/mail` | Mail something to you or to a named recipient. |
| `/send` | Send something by email. |
| `/contacts` | Manage the address book (list, add with permissions, remove, find). |
| `/beadle enable` / `/beadle disable` | Turn beadle's repo guidance on or off in this repo. Same as the CLI `enable` and `disable` below. |

### CLI

The CLI does not check contact permissions; it acts with your authority.

| Command | What it does |
|---------|-------------|
| `beadle-email list` | List messages for the current repo. Flags: `--folder`, `--count`, `--offset`, `--unread`, `--all-repos`. |
| `beadle-email search` | Search the mailbox. Flags: `--from`, `--subject`, `--since`, `--text`, `--folder`, `--count`, `--offset`, `--unread`, `--all-repos`. |
| `beadle-email read <uid>` | Read a message without marking it read. Flag: `--folder`. |
| `beadle-email mark [uid]` | Mark one message, or several with `--ids a,b,c`, as read; `--unread` reverses it. Flag: `--folder`. |
| `beadle-email send` | Send an email. Flags: `--to`, `--subject`, `--body` (required), `--cc`, `--bcc`. |
| `beadle-email reply <uid>` | Reply in the same thread, quoting the original. Flags: `--body` (required), `--cc`, `--bcc`, `--folder`. |
| `beadle-email move <uid>` | Move a message (default destination `Archive`). Flags: `--folder`, `--to`. |
| `beadle-email folders` | List IMAP folders. |
| `beadle-email contact list` | List contacts. |
| `beadle-email contact add` | Add a contact. Flags: `--name`, `--email` (required), `--alias`, `--gpg-key-id`, `--notes`. |
| `beadle-email contact remove <name>` | Remove a contact. |
| `beadle-email contact find <query>` | Find contacts by name, email, or alias. |
| `beadle-email identity` | Show the active identity (same as `identity show`). |
| `beadle-email identity set <handle>` | Set the identity for the current repo. |
| `beadle-email install` | Set up beadle-email once per machine. `--standalone` registers the MCP server without the plugin. |
| `beadle-email uninstall` | Remove beadle-email. |
| `beadle-email enable` | Turn on beadle's repo guidance in the current repo. |
| `beadle-email disable` | Turn it off. `--purge` also deletes `.punt-labs/beadle/`. |
| `beadle-email serve` | Start the MCP server on stdio. `--transport ws --port N` serves over WebSocket instead (default port 8420). |
| `beadle-email health` | Check that a WebSocket server answers on `--port` (default 8420). |
| `beadle-email doctor` | Check installation health. |
| `beadle-email status` | Show the active identity and configuration. |
| `beadle-email version` | Print the version. |

Most commands that read or send mail accept `--config PATH` (`-c`) to use a
different `email.json`. Global flags `--json` (`-j`), `--verbose` (`-v`), and
`--quiet` (`-q`) work with every command except `install`, `uninstall`, and
`version`.

**Repo guidance.** Run `enable` inside a repo to write beadle's agent guide to
`.punt-labs/beadle/CLAUDE.md`, write an `enabled` marker, add the line
`@.punt-labs/beadle/CLAUDE.md` to the repo's `CLAUDE.md`, and add beadle's
permission rules to `.claude/settings.json`. An agent working in that repo then
reads the guide. `disable` removes the line, the marker, and the permission
rules, and leaves the guide in place. Commit the result through a pull request.
Keep `.punt-labs/local/` gitignored; enable and disable use it for a private
operation lock, not for anything you should commit.
This follows the
[Tool Enable/Disable standard](https://github.com/punt-labs/punt-kit/blob/main/standards/tool-enable-disable.md).

**Repo-scoped mail.** When several repos share one mailbox, `list` and
`search` show only the current repo's mail by default. A message belongs to a
repo when its `X-Beadle-Repo` header or its `[owner/repo]` subject tag matches
the repo's git `origin` remote. `--all-repos` shows the whole mailbox. Outside
a git repo, all mail is shown. The new-mail poller counts mail the same way.

## Setup

### Credentials

beadle-email reads passwords and keys at run time, in this order: the OS
keychain, a secret file, then an environment variable. On macOS the keychain
is the system Keychain. On Linux it is `pass`, then `secret-tool` (libsecret /
GNOME Keyring). Store only the secrets you use:

| Name | Needed for |
|------|-----------|
| `imap-password` | Reading mail (also used for SMTP unless `smtp-password` is set) |
| `smtp-password` | Sending mail, when it differs from the IMAP password |
| `resend-api-key` | The optional Resend fallback |
| `gpg-passphrase` | Signing outbound mail with a passphrase-protected key |

<details>
<summary>Storing a secret with each backend</summary>

**macOS Keychain:**

```bash
security add-generic-password -s beadle -a imap-password -w 'your-bridge-password'
```

```bash
security add-generic-password -s beadle -a resend-api-key -w 'your-resend-key'
```

```bash
security add-generic-password -s beadle -a gpg-passphrase -w 'your-gpg-passphrase'
```

**pass** (Linux, recommended --- encrypted at rest with your own GPG key):

```bash
pass insert beadle/imap-password
```

```bash
pass insert beadle/resend-api-key
```

```bash
pass insert beadle/gpg-passphrase
```

**secret-tool** (Linux, fallback --- GNOME Keyring via libsecret):

```bash
secret-tool store --label='beadle imap-password' service beadle account imap-password
```

```bash
secret-tool store --label='beadle resend-api-key' service beadle account resend-api-key
```

```bash
secret-tool store --label='beadle gpg-passphrase' service beadle account gpg-passphrase
```

**Secret files** (`~/.punt-labs/beadle/secrets/<name>`, mode 600):

```bash
mkdir -p ~/.punt-labs/beadle/secrets
```

```bash
printf '%s' 'your-bridge-password' > ~/.punt-labs/beadle/secrets/imap-password
```

```bash
chmod 600 ~/.punt-labs/beadle/secrets/imap-password
```

**Environment variables** (`BEADLE_` plus the name in upper case, with `_` for `-`):

```bash
export BEADLE_IMAP_PASSWORD='your-bridge-password'
```

```bash
export BEADLE_RESEND_API_KEY='your-resend-key'
```

</details>

### Connection Settings

Connection settings live in `email.json`. Create the directory:

```bash
mkdir -p ~/.punt-labs/beadle
```

Then save one of the examples below as `~/.punt-labs/beadle/email.json`,
replacing the addresses with your own. On first run beadle copies this file
into the identity directory (see [Identity](#identity)); after that, edit the
copy there.

**Proton Bridge (localhost):**

```json
{
  "imap_host": "127.0.0.1",
  "imap_port": 1143,
  "imap_user": "you@example.com",
  "smtp_host": "127.0.0.1",
  "smtp_port": 1025,
  "smtp_user": "you@example.com",
  "from_address": "you@example.com",
  "poll_interval": "3m"
}
```

**External IMAP + SMTP (Fastmail, Gmail, and others):**

```json
{
  "imap_host": "imap.fastmail.com",
  "imap_port": 143,
  "imap_user": "you@fastmail.com",
  "smtp_host": "smtp.fastmail.com",
  "smtp_port": 587,
  "smtp_user": "you@fastmail.com",
  "from_address": "you@fastmail.com",
  "poll_interval": "10m"
}
```

`poll_interval` sets how often beadle checks for new mail; leave it out, or
set it to `"n"`, to turn polling off. `/inbox` can change it later. Add `"gpg_signer":
"<key ID or email>"` to PGP-sign all outbound mail; signed mail is never sent
through Resend, because Resend cannot sign it. See the
[setup guide](docs/setup-guide.md) for a full walkthrough, including Proton
Bridge and GPG keys.

## Trust Model

| Level | Sender | Detection | What It Means |
|-------|--------|-----------|---------------|
| `trusted` | Proton → Proton | `X-Pm-Content-Encryption: end-to-end` + `X-Pm-Origin: internal` | End-to-end encrypted by Proton |
| `verified` | External | `gpg --verify` exits 0 | Valid PGP signature from a known key |
| `untrusted` | External | `gpg --verify` exits non-zero | PGP signature present but invalid or from an unknown key |
| `unverified` | External | No `multipart/signed` part | No PGP signature |

beadle-email shows the trust level on each message so the assistant and you
can judge it. It does not refuse to read or reply because of a low trust level;
what it enforces is [contact permissions](#contact-permissions).

## Identity

The simplest setup needs one file: `~/.punt-labs/beadle/default-identity`,
containing a single email address. beadle uses that identity for everything.

For more than one identity --- a fixed identity per repo, or switching
mid-session with `switch_identity` --- install
[ethos](https://github.com/punt-labs/ethos). With ethos present, beadle looks
for an identity handle in this order:

1. **Repo config** --- `.punt-labs/ethos.yaml` with `agent: <handle>` (set it with `beadle-email identity set <handle>`)
2. **Active ethos identity** --- `~/.punt-labs/ethos/active`

It then reads the name and email for that handle from
`~/.punt-labs/ethos/identities/<handle>.yaml`. An optional
`~/.punt-labs/ethos/identities/<handle>.ext/beadle.yaml` supplies a GPG key ID
that `beadle-email identity` reports; signing still uses `gpg_signer` in
`email.json`.

If no handle is found --- ethos is not installed, or nothing sets one ---
beadle uses `default-identity`. If a handle is found but its identity file is
missing or unreadable, beadle stops with an error instead of falling back, so
it never sends mail as the wrong identity.

Each identity has its own directory, `~/.punt-labs/beadle/identities/<email>/`,
holding its `email.json`, `contacts.json`, and downloaded `attachments/`.

## Contact Permissions

Each contact has an optional `permissions` map keyed by identity email, using
Unix-style `rwx` letters:

| Permission | Meaning |
|------------|--------|
| `r` (read) | The MCP tools show and read messages from this contact. |
| `w` (write) | The MCP tools may send mail and replies to this contact. |
| `x` (execute) | Not used by beadle-email; only `beadle-daemon` checks it. |

A contact without permissions has `---` (none). The address book is an allow
list: messages from unknown senders, or from contacts without `r`, appear
redacted in listings and cannot be read. A contact's email may be a glob
pattern such as `*@mail.example.com`; pattern contacts may hold only `r--` or
`---`. Permissions are set per identity, so the same contact can have
different access for each of your addresses. The CLI does not check
permissions.

## Documentation

[Setup Guide](docs/setup-guide.md) |
[Design Log](DESIGN.md) |
[Contributing](CONTRIBUTING.md) |
[Changelog](CHANGELOG.md)

## License

[MIT](LICENSE)
