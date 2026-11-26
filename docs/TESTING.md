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
  **Outstanding gap, tracked as beadle-hi4n:** further
  `t.Skip("gpg not installed")` sites remain in `internal/pgp`
  (`verify_test.go`, `decrypt_test.go`, `encrypt_test.go`, `expiry_test.go`,
  `probe_test.go`, `sign_test.go`) and `internal/email` (`send_test.go`,
  `reply_test.go`) — load-bearing for the *existing* PGP integration tier
  this table already lists as `< 5s, tag: none`, i.e. already claimed to
  run unconditionally, just not yet enforced there. Their count is not
  stated here on purpose: an earlier draft of this bullet said "eleven,"
  which was wrong by more than 3x and had already been copied verbatim
  into two other places before anyone ran the count. Reproduce it instead
  of trusting a number:
  `grep -rn 'gpg not installed' internal/ --include='*_test.go' | grep -v internal/daemon | wc -l`.
- **One entry point.** The daemon pipeline tier runs under the plain `test`
  target — no build tag, no `-tags=integration`, no CI-only invocation. A
  developer's `make test`/`make check` and CI's `go test -race -count=1
  ./...` step run identically.
- **`ethos`, `beadle-email`, and `biff` are required tools**, each
  made available automatically by a `test`-dependency target.
  `internal/daemon/templates_test.go`'s
  `TestDefaultMCPRegistry_StdioCommandsAreReal` is the consumer: it runs
  every stdio MCP registry entry's configured command with `--help` and
  fails loudly, per the no-`t.Skip` rule above, if the binary is missing.
  `ethos` is pinned via `Makefile`'s `ETHOS_VERSION` and installed by
  `tools-ethos` (`go install` with `GOBIN=$(CURDIR)/.tmp/bin`, leaving the
  user's installed ethos untouched). `tools-biff` reuses any executable
  `biff` already on `PATH`, without calling uv. If none is on `PATH`, it
  reuses the pinned fallback in uv's tool bin or installs it with
  `uv tool install` if needed. The test recipe adds the uv tool bin
  directory to `PATH` if needed. `beadle-email` has no
  version pin and is **not** installed into either shared directory —
  `tools-beadle-email` self-builds this branch's own `./cmd/beadle-email`
  with `go build -o` into `$(CURDIR)/.tmp/bin`, a directory scoped to
  *this checkout*, so the binary under test is always the code under
  review in the current branch or worktree, never a prior release and
  never another checkout's in-flight build. The "runs identically" claim
  above holds through `make test` (or `make check`) only: a bare `go test
  ./...` has no `.tmp/bin` on `PATH` and resolves whatever `beadle-email`
  is first on the shell's own `PATH` instead — a prior release, a stale
  build from another branch, or nothing at all. CI runs the same
  provisioning targets before its `Test` step and adds the
  per-checkout `.tmp/bin` (an absolute path built from
  `$GITHUB_WORKSPACE`, containing both test ethos and beadle-email),
  `$(go env GOPATH)/bin`, and the uv tool bin directory (where `uv tool install`
  places the fallback `biff`) to `$GITHUB_PATH` so `exec.LookPath` finds all three. Because
  the fallback install still writes to a host-global directory, a
  version-pin bump racing against a concurrent `make test` on the same
  host (e.g. two worktrees provisioning at once) can still interleave;
  ethos and beadle-email's per-checkout directory is immune to this by
  construction, since nothing else on the host ever writes there.

## Ethos handoff snapshot

With a compatible Ethos release, the Linux amd64 profile runs the blocking
`make check` in a snapshot. It supplies Go, analyzers, test Ethos, biff,
and Node tools from a pinned archive. It still builds the checkout's own
`beadle-email` and runs the full race-enabled suite. Ethos verifies the host
tools declared in the profile, including Git, make, GCC, GnuPG, ShellCheck,
`pass`, and `secret-tool` (Ubuntu package `libsecret-tools`). Only loopback
is available to the tests.

Use an Ethos CLI with verified worktree snapshots and `handoff refresh-data`
support. This is separate from the older test binary installed by
`make tools-ethos`. Install the release archive before running the handoff gate.
Copy the URL and SHA-256 from the
[snapshot profile](../.punt-labs/ethos/checks/snapshots/beadle-go.yaml).

```bash
url='<profile URL>'
sha='<profile SHA-256>'
archive=".tmp/$sha.tar.gz"
store="$HOME/.punt-labs/ethos/runtimes"
mkdir -p .tmp "$store" &&
  curl --fail --location --proto '=https' --proto-redir '=https' "$url" -o "$archive" &&
  printf '%s  %s\n' "$sha" "$archive" | sha256sum --check &&
  install -m 0644 "$archive" "$store/$sha.tar.gz" &&
  ethos handoff refresh-data
```

Ethos mounts a separate, read-only Go vulnerability database refreshed
within 24 hours. Missing or stale data defers the check rather than passing
it; run `ethos handoff refresh-data` or the explicit live `make check`.
The live gate and CI still query the upstream database.

To package a new runtime, use `make handoff-runtime-bundle` on Linux amd64
with the official Node distribution and uv versions pinned by the
[Handoff runtime workflow](../.github/workflows/handoff-runtime.yml), or
dispatch that workflow on `main` with the next `handoff-runtime-vN` tag.
Publish the archive and verify its digest before updating the profile's URL
and digest together. The archive's `cache/go-download` contains the Go tool
and dependency sources and their license notices. A snapshot rejects a bundle
made for different Go modules or Makefile tool pins. A rebuild can resolve
different transitive npm and Python packages; the published archive's digest
pins the exact result.
Runtime prereleases have no changelog; they are not product updates.

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
