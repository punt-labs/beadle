# Testing

## Test Pyramid

| Layer | What | Speed | Tag |
|-------|------|-------|-----|
| Unit | Pure functions, table-driven, no I/O | < 5s | none |
| PGP integration | Ephemeral GPG keypair, sign/verify round-trip | < 5s | none |
| MCP smoke | In-process tool registration, identity error handling | < 2s | none |
| MCP handler | Full stack via in-process IMAP/SMTP (`testserver`) | < 3s | none |
| IMAP/SMTP | `email.Client` against in-process servers | < 2s | `integration` |
| Live (manual) | Real Proton Bridge, iCloud, GPG Mail | Manual | — |

## Key Rules

- All tests must pass. If a test is failing, fix it. Do not skip, ignore, or work around it.
- GPG operations in tests use a temporary GNUPGHOME (ephemeral keyring per test).
- GPG test home directories must use short paths (`/tmp/bg-*`) to avoid the 108-byte Unix socket path limit.
- `-race` is mandatory for all test runs.

## Fastmail Test Config

Fastmail SMTP preserves `multipart/signed` envelopes (verified 2026-04-11). Proton Bridge and Resend/SES do not. For PGP signing tests, switch to Fastmail SMTP:

```bash
# Switch to Fastmail SMTP
cp ~/.punt-labs/beadle/identities/claude@punt-labs.com/email.json.fastmail-test \
   ~/.punt-labs/beadle/identities/claude@punt-labs.com/email.json
pass show beadle/fastmail-app-password | pass insert -f -e beadle/smtp-password

# Restore prod (Proton Bridge)
# email.json: smtp_host=127.0.0.1, smtp_port=1025, smtp_user=claude@punt-labs.com
pass show beadle/imap-password | pass insert -f -e beadle/smtp-password
```

Saved artifacts:

- `~/.punt-labs/beadle/identities/claude@punt-labs.com/email.json.fastmail-test` — Fastmail SMTP config (`smtp.fastmail.com:465`, user `claude_puntlabs@pobox.com`)
- `pass beadle/fastmail-app-password` — Fastmail app password
- `pass beadle/resend-api-key` — Resend API key

Note: sending as `claude@punt-labs.com` via Fastmail requires adding `punt-labs.com` as a verified sending identity in Fastmail (DNS TXT record). The test used `from_address: claude_puntlabs@pobox.com` to bypass this.
