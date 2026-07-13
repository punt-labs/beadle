# Architecture

## Package Map

| Package | Responsibility |
|---------|---------------|
| `cmd/beadle-email/` | CLI entry point: `serve`, `version`, `doctor`, `status` |
| `internal/channel/` | Channel interface — `Message`, `TrustLevel`, shared types |
| `internal/email/` | IMAP client (Proton Bridge), MIME parser, trust classifier, SMTP/Resend senders |
| `internal/pgp/` | GPG signature verification and signing via `gpg` CLI in isolated GNUPGHOME |
| `internal/mcp/` | MCP tool definitions and handlers (8 tools) |
| `internal/secret/` | Credential resolution: OS keychain → file → env var |

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
2. **Linux libsecret** (`secret-tool` CLI) — v0.1.1
3. **Secret file** (`~/.punt-labs/beadle/secrets/<name>`, mode 600)
4. **Environment variable** (`BEADLE_IMAP_PASSWORD`, `BEADLE_RESEND_API_KEY`)

Config file (`~/.punt-labs/beadle/email.json`) stores only connection parameters, never secrets.

## Design Invariants

- **Zero agent authority.** Every action requires a GPG-signed instruction from the owner. The daemon has no independent decision-making.
- **Preflight before execute.** All permissions are validated before any command runs. No partial execution.
- **Isolated keychain.** PGP operations use temporary GNUPGHOME directories, never touching the user's system GPG keyring.
- **Non-expiring keys rejected.** All command-signing keys must have an expiration date. This is a security invariant.
- **Audit log is tamperproof.** Append-only, GPG-signed entries. Only the owner can clear the log.
