# Design Decisions

Log of architectural decisions, alternatives considered, and outcomes.
Consult before proposing changes to settled architecture.

## DES-001: Trust model — four levels, not binary

**Decision:** Trust classification uses four discrete levels (trusted, verified,
untrusted, unverified) rather than a binary trusted/untrusted split.

**Why:** Proton-to-Proton messages have infrastructure-level E2E encryption
that is qualitatively different from PGP-signed external messages. Collapsing
them into a single "trusted" category loses information. The four-level model
lets consumers make graduated decisions.

**Detection:**

| Level | Method |
|-------|--------|
| trusted | `X-Pm-Content-Encryption: end-to-end` + `X-Pm-Origin: internal` |
| verified | `gpg --verify` exit 0 |
| untrusted | `gpp --verify` exit non-zero |
| unverified | No `multipart/signed` in Content-Type |

## DES-002: Inline PGP verification in list_messages

**Decision:** `list_messages` runs `gpg --verify` on every message with a PGP
signature during listing, not as a separate step.

**Alternative:** Return `unverified` for signed messages in listings and require
a separate `verify_signature` call. This was the original design and led to user
confusion — the listing showed `unverified` for a message that was actually
verifiable.

**Trade-off:** Adds ~1-2s per signed message to listing time. Acceptable because
most messages are unsigned, and trust accuracy matters more than latency.

## DES-003: Isolated GNUPGHOME per PGP operation

**Decision:** Every `Verify()` and `Sign()` call creates a temporary GNUPGHOME
under `/tmp` (short path for Unix socket limit), imports needed keys, runs gpg,
and deletes the temp dir.

**Why:** Prevents keyring pollution. A malicious attached key can't persist in
the user's keyring. Each operation is hermetic.

**Socket path:** GPG agent communicates via Unix domain socket with a 108-byte
path limit. macOS `os.MkdirTemp("")` yields `/var/folders/...` paths that exceed
this. Using `/tmp/bg-*` keeps it short.

## DES-004: System keyring bridge for verification

**Decision:** When a message has a PGP signature but no attached public key,
`exportAll()` copies all public keys from `~/.gnupg/` into the isolated
GNUPGHOME before running `gpg --verify`.

**Why:** Many senders sign messages without attaching their public key (e.g.,
GPG Mail on macOS). Without bridging, these messages always fail verification
with "No public key." The bridge is read-only — it exports from the system
keyring but never writes to it.

## DES-005: Proton Bridge strips multipart/signed

**Decision:** Do not PGP-sign outbound messages through Proton Bridge SMTP.

**Evidence:** Tested 2026-03-13. Sent a `multipart/signed` RFC 3156 message
through Proton Bridge SMTP. The message arrived with the signature as a detached
`signature.asc` attachment — the `multipart/signed` envelope was stripped. GPG
Mail did not auto-verify because the MIME structure was wrong.

**Root cause:** Proton Bridge re-processes all outbound messages through its own
encryption pipeline, disassembling and reassembling MIME structure.

**Workaround:** Amazon SES `SendRawEmail` preserves raw MIME. Tracked in
beadle-atz. Requires SPF/DKIM DNS changes for punt-labs.com.

## DES-006: Resend does not support raw MIME

**Decision:** Resend API cannot be used for PGP-signed outbound mail.

**Evidence:** Tested 2026-03-13. Resend's POST /emails endpoint only accepts
structured fields (from, to, subject, text, html). There is no `raw` field.
The API docs confirm no raw MIME support.

**Impact:** Resend is fallback-only for unsigned plain text delivery.

## DES-007: Sender chain — SMTP primary, Resend fallback

**Decision:** Outbound email tries Proton Bridge SMTP first, falls back to
Resend API.

**Why:** Proton Bridge passes SPF/DKIM/DMARC for punt-labs.com. Resend has no
DKIM records configured for punt-labs.com (DMARC policy: quarantine). Messages
sent through Resend may land in spam.

**Future:** SES will become primary for external recipients (with PGP signing).
Proton Bridge for Proton-to-Proton. Resend becomes last resort.

## DES-008: Credential resolution chain

**Decision:** Secrets resolved at runtime through: OS keychain → file → env var.
Config files store only connection parameters.

**Why:** Prevents accidental secret commits. The config file
(`~/.config/beadle/email.json`) can be shared or version-controlled safely.
Secrets live in the OS keychain (macOS Keychain, Linux libsecret in v0.1.1)
or mode-600 files.

**Path traversal:** `secret.Get()` rejects names containing `/` or `\`.
`fileGet()` rejects files with group/world-readable permissions.

## DES-009: Install via curl | sh with SHA256 verification

**Decision:** Distribution via GitHub Releases with a curled install script,
matching the mcp-proxy standard.

**Structure:** 4 static binaries (darwin/arm64, darwin/amd64, linux/arm64,
linux/amd64) + checksums.txt. Install script downloads, verifies SHA256,
installs to `~/.local/bin`, registers marketplace, and registers the MCP
server via `claude mcp add -s user`.

**No manual steps after install.** The installer completes all registration.
User just restarts Claude Code.
