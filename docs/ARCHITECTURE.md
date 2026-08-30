# Architecture

## Repository Layout

Everything the Claude Code plugin ships lives under `plugin/`, and nothing
else does:

| Path | Contents |
|------|----------|
| `plugin/.claude-plugin/plugin.json` | Plugin manifest. `mcpServers` names the `beadle-email` binary on `PATH`, so no compiled code ships. |
| `plugin/.claude-plugin/hooks/hooks.json` | Hook registrations. This path, not `hooks/hooks.json`, is where Claude Code discovers them for this plugin (v0.6.1). |
| `plugin/commands/` | Slash commands (`/beadle`, `/contacts`, `/inbox`, `/mail`, `/send`). |
| `plugin/hooks/` | The two hook scripts: `session-start.sh` and `suppress-output.sh`. |

The marketplace installs this directory with Claude Code's `git-subdir`
source (`"source": "git-subdir"`, `"path": "plugin"`), which is a blobless
partial clone plus `git sparse-checkout set --cone plugin` — so an install
never fetches `cmd/`, `internal/`, `docs/`, `scripts/`, `.github/`, or this
repo's own `.claude/` and `.punt-labs/` working state.

Two rules follow from that, and both are load-bearing:

- **The plugin surface must not reach outside itself at runtime.** A hook
  script may use `$HOME`, the `beadle-email` binary, and paths under the
  *consumer's* home or repo; it may not reference a file elsewhere in this
  repo, because that file will not exist on an installed plugin.
  `${CLAUDE_PLUGIN_ROOT}` is `plugin/`, and `session-start.sh` falls back to
  its own parent directory — never the git toplevel, which is no longer the
  plugin root.
- **Cone mode does not materialize the repo root.** Verified directly against
  `~/.claude/plugins/cache/punt-labs/<plugin>/<version>/` for every
  punt-labs git-subdir plugin, including a fresh beadle install (`beadle/install.sh`):
  the installed cache contains only the `path` directory's contents
  (`plugin/hooks/`, `plugin/commands/`, `plugin/.claude-plugin/`) — no `.git`,
  no root-level files, no other top-level directories. A tracked root file
  such as `.envrc` does not reach a plugin consumer's install. This corrects
  an earlier (2026-08-23 and prior) claim in this document that cone mode
  "always materializes the files in the repo root" — that claim was never
  checked against the actual installer output and does not hold. The root
  document set is still worth keeping small for repo hygiene, but not because
  of a distribution leak.

## Package Map

| Package | Responsibility |
|---------|---------------|
| `cmd/beadle-email/` | CLI entry point: product commands (`list`, `read`, `send`, `move`, `folders`, `contact`) plus admin (`serve`, `install`, `uninstall`, `doctor`, `status`, `identity`, `health`, `version`) |
| `cmd/beadle-daemon/` | Daemon entry point: the mail-triggered mission pipeline runner |
| `internal/channel/` | Channel interface — shared contract for Beadle communication channels (`Message`, `TrustLevel`, shared types) |
| `internal/email/` | IMAP client (Proton Bridge), MIME parser, trust classifier, SMTP/Resend senders |
| `internal/pgp/` | GPG signature verification and signing via `gpg` CLI. Verification runs in an isolated GNUPGHOME; signing and decryption use the owner's system keyring |
| `internal/mcp/` | MCP tool definitions and handlers (21 always-on tools; 2 poll tools gated on config) |
| `internal/daemon/` | Mail-triggered mission pipeline: planner, spawner, runner, mission/command model, persistence |
| `internal/contacts/` | Address book storage and lookup |
| `internal/identity/` | Resolves which identity beadle operates as |
| `internal/session/` | Reads the ethos session roster to enumerate participants |
| `internal/secret/` | Credential resolution: OS keychain → file → env var |
| `internal/paths/` | Single root directory for all beadle data |

## Trust Model

Four levels based on sender identity and encryption:

| Level | Sender | Signature | Detection |
|-------|--------|-----------|-----------|
| `trusted` | Proton→Proton | E2E (Proton) | `X-Pm-Content-Encryption: end-to-end` + `X-Pm-Origin: internal` |
| `verified` | External | Valid PGP | `gpg --verify` returns 0 |
| `untrusted` | External | Invalid PGP | `gpg --verify` returns non-zero |
| `unverified` | External | None | No `multipart/signed` |

## Credentials

Resolved at runtime by name through a priority chain:

1. **macOS Keychain** (`security` CLI) — v0.1.0
2. **Linux** (`pass` primary, `secret-tool`/libsecret fallback; if both are installed, `pass` wins) — v0.1.1
3. **Secret file** (`~/.punt-labs/beadle/secrets/<name>`, mode 600)
4. **Environment variable** (`BEADLE_IMAP_PASSWORD`, `BEADLE_RESEND_API_KEY`)

The config file stores only connection parameters, never secrets. It is resolved per identity: `~/.punt-labs/beadle/identities/<email>/email.json` is preferred, with the root `~/.punt-labs/beadle/email.json` kept as a legacy fallback (and migrated into the identity dir on demand).

## Design Invariants

- **Zero agent authority (target state, not yet enforced).** Every action is meant to require a GPG-signed instruction from the owner, with the daemon holding no independent decision-making. `internal/daemon/command.go`'s `VerifySignature` is currently a hard-coded no-op, so nothing yet rejects an unsigned or tampered command file — see `prfaq.tex`'s Feature Appendix ("Must Do") for the gap.
- **Preflight before execute.** All permissions are validated before any command runs. No partial execution.
- **Isolated verification.** Inbound signature verification (`internal/pgp/verify.go`) runs in a temporary GNUPGHOME so an untrusted sender's key never touches the system keyring. Signing and decryption (`internal/pgp/sign.go`, `decrypt.go`) use the owner's own key from the system keyring, since that's where the owner's private key already lives — there is no isolation to provide there.
- **Non-expiring keys rejected.** All command-signing keys must have an expiration date. This is a security invariant.
- **Audit log is tamperproof.** Append-only, GPG-signed entries. Only the owner can clear the log.
