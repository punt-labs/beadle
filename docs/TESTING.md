# Testing

## Test Pyramid

| Layer | What | Speed | Tag |
|-------|------|-------|-----|
| Unit | Pure functions, table-driven, no I/O | < 5s | none |
| PGP integration | Ephemeral GPG keypair, sign/verify round-trip | < 5s | none |
| MCP smoke | In-process tool registration, identity error handling | < 2s | none |
| MCP handler | Full stack via in-process IMAP/SMTP (`testserver`) | < 3s | none |
| Daemon pipeline | Mail-triggered pipeline end to end, real ethos CLI, faked LLM subprocess | < 2s | none |
| IMAP/SMTP | `email.Client` against in-process servers | < 2s | `integration` |
| Live (manual) | Real Proton Bridge, iCloud, GPG Mail | Manual | — |

## The daemon pipeline tier

`internal/daemon`'s mail-triggered pipeline (email → trust classification →
per-contact permission → command-file signature → planner → executor →
real `ethos mission create` → worker spawn) had never actually run until a
manual live test surfaced four defects at once, including a mission-contract
shape ethos's real schema rejects outright (beadle-8gt). This tier exists to
catch that class of defect automatically. See
[`daemon-test-harness.md`](daemon-test-harness.md) for the full design.

It covers four gates, each with the negative case that guards it, driven
through the daemon's real `OnNewMail` entry point (never the `Poller` —
`Poller`'s first-poll suppression and 1-minute minimum interval make it
unusable for a pre-commit-gate test, and it carries no security-relevant
behavior of its own):

1. **Trust classification** — a PGP-signed or Proton-trusted message creates
   a pipeline; an unsigned message from the same sender creates nothing.
2. **Per-contact `x` permission** — an `rwx`/`r-x` sender's verified message
   creates a pipeline; an `rw-` sender's does not.
3. **Command-file signature** — a file signed by the configured owner key
   loads; unsigned, wrong-key, and expired-key files are rejected
   (`internal/daemon/signature_test.go`'s `TestVerifySignature`).
4. **Contract validity against the real ethos CLI** — a generated stage
   contract is accepted by the real `ethos mission create` binary; a
   contract missing a required field (e.g. `evaluator.handle`) is rejected.
   The rejection case is a deliberate negative control: without it, a
   `PATH` misconfiguration that silently no-ops the CLI call, or a Go-side
   error that gets swallowed, could make every `ethos mission create` call
   spuriously succeed and this gate would never notice. It does **not**
   detect broken `$HOME`/`$ETHOS_REPO_ROOT` isolation — an isolation
   failure makes the CLI validate against the *wrong* identity data, a
   different failure mode a required-field check never touches (it doesn't
   consult the identity graph at all).

`internal/daemon/harness_test.go`'s `TestOnNewMail_EndToEnd` exercises gates
1 and 2 (positive and negative) together with gates 3 and 4's positive
cases in one pass; gate 3's negative cases live in `TestVerifySignature`
and gate 4's negative control lives in
`TestBuildStageContract_RealEthosCLIRejectsMalformedContract`
(`internal/daemon/pipeline_test.go`). `TestOnNewMail_EndToEnd` is an
external test package (`package daemon_test`), not the white-box
convention every other file in `internal/daemon` uses, because its
fixtures (`internal/daemontest`) import `internal/daemon` — a package-daemon
test file importing `internal/daemontest` closes an import cycle.

**What a new gate test costs.** One fixture builder if the gate needs a new
kind of induced state (see `testserver.AddRawMessage`, `testenv.AddContact`,
`daemontest.SignCommand`); one `NewMailHandler(...)` call — every argument
already has a working default; one `handler.OnNewMail(n)` +
`handler.Stop()` pair (`Stop` blocks on the handler's own `sync.WaitGroup`,
so there is no sleep or polling loop to write); and an assertion against
either a mock or `daemontest.FakeSpawner`'s recorded calls.

Only `daemon.Spawner` (the real Claude Code worker subprocess) is faked,
via `daemontest.FakeSpawner` — the one component whose real form is
unbounded in time, cost, and non-determinism. Everything upstream,
including the real `ethos` CLI, runs for real inside a
`testenv.IsolateEthos`-isolated `$HOME`/`$ETHOS_REPO_ROOT`.

## Key Rules

- All tests must pass. If a test is failing, fix it. Do not skip, ignore, or work around it.
- GPG operations in tests use a temporary GNUPGHOME (ephemeral keyring per test).
- GPG test home directories must use short paths (`/tmp/bg-*`) to avoid the 108-byte Unix socket path limit.
- `-race` is mandatory for all test runs.
- **No `t.Skip` on a missing external dependency, ever — the standard for
  every test in this repo, not only `internal/daemon`.** A test that
  silently skips locally is the same failure as one that skips in CI —
  exactly how beadle-8gt's regression coverage went uncaught for as long
  as it did. `internal/daemon` has zero `t.Skip`/`t.Skipf` sites today:
  every `gpg`- or `ethos`-dependent test fails with the install remedy
  (`t.Fatalf`), and `runner_test.go`'s `setupWhitelist` fails the same way
  for a missing base system utility (`echo`, `cat`, `false`, `sleep`,
  `dd`, `env`, `tr` — all POSIX utilities guaranteed present on every
  supported platform and this repo's own CI runner, not an optional tool
  to install).
  **Outstanding gap, tracked as beadle-hi4n:** eleven further
  `t.Skip("gpg not installed")` sites remain in `internal/pgp` and
  `internal/email` (`verify_test.go`, `decrypt_test.go`, `encrypt_test.go`,
  `expiry_test.go`, `probe_test.go`, `sign_test.go`, `send_test.go`,
  `reply_test.go`) — load-bearing for the *existing* PGP integration tier
  this table already lists as `< 5s, tag: none`, i.e. already claimed to
  run unconditionally, just not yet enforced there.
- **One entry point.** The daemon pipeline tier runs under the plain `test`
  target — no build tag, no `-tags=integration`, no CI-only invocation. A
  developer's `make test`/`make check` and CI's `go test -race -count=1
  ./...` step run identically.
- **`ethos` is a required tool**, pinned via `Makefile`'s `ETHOS_VERSION`
  and installed automatically by the `tools-ethos` target, which `test`
  depends on — a developer never runs `make tools-ethos` by hand except
  implicitly, the first time `make test`/`make check` provisions it. CI
  installs the same pinned binary via the same target before its `Test`
  step and adds `$(go env GOPATH)/bin` to `$GITHUB_PATH` so
  `exec.LookPath("ethos")` finds it.

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
