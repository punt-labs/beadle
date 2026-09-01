# Daemon Test Harness Design

Status: implemented. Design mission `m-2026-08-30-053` (`beadle-axj`)
closed pass; implementation mission `m-2026-08-31-002` closed pass and
shipped the harness, fixtures, and CI wiring in PR #261.

## Why this exists

The daemon path — email arrives, gets trust-classified, checked against a
per-contact `x` permission, planned, executed, and turned into a real ethos
mission that spawns a worker — had never actually run end to end until a
manual live test surfaced four defects at once, including a mission-contract
shape ethos's real schema rejects outright (`beadle-8gt`): every
daemon-triggered mission would have failed. `internal/daemon/pipeline_test.go`
already carries a regression test for that exact defect,
`TestBuildStageContract_ValidatesAgainstRealEthosCLI` — but it `t.Skip`s
(`"ethos not on PATH; skipping real-CLI mission-contract validation"`) when
`ethos` is not on `PATH`, and `.github/workflows/test.yml` never installs
`ethos`. So the one test that
would have caught `beadle-8gt` has been skipping on every CI run since it was
written. That is precisely the failure mode this design closes: a test that
silently skips locally is the same failure as one that skips in CI, because
in both cases the operator believes the path is covered and it is not.

This document designs a test tier that (1) exercises all four
security-relevant gates on the daemon's mail-triggered pipeline, including
each gate's negative case, (2) runs identically under a developer's
`make check` and in CI with no skip path, build tag, or CI-only invocation,
and (3) is fast enough to sit in the pre-commit gate.

## Survey of existing material

### `internal/testserver.Fixture` — in-process IMAP+SMTP

`internal/testserver/fixture.go:28-144`. `NewFixture(t)` starts real
`go-imap`/`go-smtp` servers bound to `127.0.0.1:0` and returns a `*Fixture`
with a pre-populated `email.Config`. `AddRawMessage(folder string, raw
[]byte) uint32` (`fixture.go:124-127`) accepts arbitrary RFC822 bytes, so any
trust level can be induced directly by constructing the right headers/body —
no live mail system needed:

- **`trusted`**: `X-Pm-Content-Encryption: end-to-end` + `X-Pm-Origin:
  internal` headers, no PGP (`internal/daemon/handler_test.go:242-249`,
  `buildTrustedRFC822`).
- **`verified`/`untrusted`**: a real `multipart/signed` RFC 3156 body,
  detached-signed with an ephemeral GPG key
  (`internal/daemon/handler_test.go:266-338`, `buildPGPSignedMessage`) —
  `verified` when the contact's registered key matches or none is
  registered, `untrusted` when it doesn't
  (`internal/daemon/handler.go:262-268`).
- **`unverified`**: no signature, no Proton headers — `fix.AddMessage(...)`
  (`internal/daemon/handler_test.go:149`, the `default` case).

`testserver.TestDialer{Password: ...}` (`internal/testserver/fixture.go:13-26`)
injects the fixture's test password past OS-keychain resolution, which is
process-global and would otherwise return a real Proton Bridge password.

### `internal/testenv` — identity/contacts/config scaffolding

`internal/testenv/env.go:35-83`. `testenv.New(t, emailAddr)` builds a fake
`$HOME` (via `t.Setenv("HOME", ...)`, `env.go:44`), writes an ethos identity
YAML, a repo-local `.punt-labs/ethos/ethos.yaml`, and an empty beadle
`contacts.json`, then returns an `*identity.Resolver` pointed at all of it.
`AddContact(name, addr, permissions)` (`env.go:86-105`) adds a
`contacts.Contact` with a parsed `rwx` string. Because `t.Setenv` mutates the
real process environment for the test's duration (not just `Env` on one
`exec.Cmd`), **any** `exec.Command` call in the code under test that does not
explicitly override `.Env` — which includes `createMissionFromContract`'s
`exec.Command("ethos", ...)` (`internal/daemon/mission.go`) and
`ClaudeRunner.Run`'s `ethos mission close` call (`internal/daemon/runner.go`)
— inherits the fake `$HOME` too. `testenv.New` does **not** set
`ETHOS_REPO_ROOT`, which matters
for §"What stays real, what gets faked" below.

### `internal/daemon.Spawner` — the one seam that stays faked

`internal/daemon/pipeline.go`'s `Spawner` interface:

```go
type Spawner interface {
    Run(ctx context.Context, missionID, mcpConfigPath, systemPromptPath string, envOverrides map[string]string) (WorkerResult, error)
}
```

`WorkerSpawner` (`internal/daemon/spawner.go:34-183` — a separate file from
the `createMissionFromContract`/`BuildContract` helpers, which live in
`internal/daemon/mission.go`) is the only production implementation, and it
`exec.LookPath("claude")`s and runs a real Claude Code subprocess
(`spawner.go:99-183`) with a 30-minute default timeout (`spawner.go:69-70`).
That is the one thing this tier must not do — a
pre-commit-gate test cannot spawn a real LLM session. `ClaudeRunner` already
takes `Spawner` as an injected struct field, so faking it is a one-line
substitution, already proven safe by `internal/daemon/pipeline_test.go`'s
`mockClaudeRunner` (which fakes one layer up, the whole `Runner`, for the
`Executor`-only unit tests). This design fakes one layer lower — `Spawner`,
not `Runner` — so that `ClaudeRunner.Run`'s own logic (build the stage
contract, create the real ethos mission, close it) stays real and gate 4 is
exercised through the actual `OnNewMail` path, not just in isolation.

**A note on citation style for the rest of this document.** From here on,
line-number citations into `internal/daemon/mission.go`,
`internal/daemon/pipeline.go`, `internal/daemon/pipeline_test.go`,
`internal/daemon/runner.go`, `internal/daemon/runner_test.go`, and
`cmd/beadle-daemon/main.go` are deliberately dropped in favor of
function/symbol names. Those six files are under active concurrent revision
in this exact repo — three merges landed in the last day alone (PRs #257, #259,
and #260) touching between them every file in that list — and this
design's own earlier draft already cited two of them (`isolatedEthosEnv`'s
doc comment and its environment-filter predicate, both in
`pipeline_test.go`) at line numbers that drifted within the same review
cycle, once before this correction and once again after it. A symbol name
greps in one step and outlives a line range a sibling PR reshuffles out from
under it. Files this design has no evidence of recent churn in — `handler.go`,
`signature.go`, `command.go`, `spawner.go`, `planner.go`,
`cmd/beadle-daemon/main_test.go`, and every package outside
`internal/daemon`/`cmd/beadle-daemon` — keep their line citations as written
below, since nothing observed here suggests they are moving.

### `isolatedEthosEnv` — the established real-ethos-CLI isolation pattern

`internal/daemon/pipeline_test.go`'s `isolatedEthosEnv`. Its doc comment
records a real corruption incident: without it,
`ethos mission create`/`ethos mission abandon` mutate whatever mission a real
concurrent session in this shared, multi-agent environment has bound, and
burn real per-date mission-ID counter slots. It works by redirecting **both**
roots ethos resolves from — `$HOME` (global session bindings, mission-ID
counters, delegations, locks) and `$ETHOS_REPO_ROOT` (repo-local mission
storage, identity/personality/role/team resolution) — to fresh scratch
directories, then symlinking only the read-only subtrees (`archetypes`
globally; `identities`, `personalities`, `roles`, `teams`, `talents`,
`writing-styles` under the repo root) back to the real, current data so
contract validation exercises the genuine schema and identity graph, while
every subtree ethos might *write* to is left absent and created fresh and
empty. `TestBuildStageContract_ValidatesAgainstRealEthosCLI` uses it by
building the isolated `[]string` env and assigning it explicitly to each of
its two `exec.Command` calls' `.Env` field (the `createCmd.Env` and
`abandonCmd.Env` assignments).

That explicit-`.Env` mechanism cannot protect a test that drives the real
`OnNewMail` → `Executor` → `ClaudeRunner.Run` path, because
`createMissionFromContract` (in `mission.go`) and `ClaudeRunner.Run`'s
`ethos mission close` call (in `runner.go`) both construct their own
`exec.Command` with no `.Env` override — they inherit `os.Environ()`.
`isolatedEthosEnv`'s `HOME`
redirection matches `testenv.New`'s mechanism (`t.Setenv`, process-wide) but
its `ETHOS_REPO_ROOT` redirection has no equivalent in `testenv` today. §"What
stays real, what gets faked" below closes that gap by porting
`isolatedEthosEnv`'s logic into `testenv` as a `t.Setenv`-based helper, so it
protects every `exec.Command("ethos", ...)` call transitively, not just the
ones a test invokes directly.

### `docs/TESTING.md` — the pyramid this tier extends

The existing pyramid (`docs/TESTING.md:5-12`) has no row for "daemon pipeline,
end to end, real ethos CLI, faked LLM subprocess." The nearest row is "MCP
handler: full stack via in-process IMAP/SMTP (`testserver`), < 3s." This
tier is the daemon-package analog of that row, plus the one thing that row
doesn't have: a real `ethos mission create` in the loop. It slots in between
"MCP handler" and "IMAP/SMTP (`integration` tag)" — no build tag, because the
hard constraint below rules that out.

`docs/TESTING.md`'s "Key Rules" (`docs/TESTING.md:14-18`) already state "All
tests must pass. If a test is failing, fix it. Do not skip, ignore, or work
around it" and mandate `-race` on every run. The existing `t.Skip("gpg not
installed")` sites inside `internal/daemon` — `handler_test.go:37,174,203`
and `signature_test.go:39` (centralized in one helper, `gpgBinary`,
`signature_test.go:35-42`) — already violate that stated rule; they have
simply never been caught because GitHub's `ubuntu-latest` runner image ships
`gnupg` preinstalled, so the skip path has (almost certainly) never actually
fired in CI, only ever on a fresh local machine. §"gpg: the same defect,
smaller blast radius" below scopes a fix for the two `internal/daemon` sites;
the further sites across `internal/pgp` and `internal/email` are named but
left to a follow-up bead, since fixing them touches files entirely outside
this mission's write set and outside the four gates this design owns. Their
count is deliberately not stated here — see that section for why a bare
count in this kind of document is itself a liability, and for the command
that reproduces it.

### `.github/workflows/test.yml` — today's CI invocation

`.github/workflows/test.yml:22-38` runs `make vet`, `make staticcheck`,
`make lint-strict`, `make vulncheck`, `make lint-shell`, then the literal
command `go test -race -count=1 ./...` (`test.yml:38`) — not `make check` and
not `make test` as a named target, but the **identical command** `make test`
expands to (`Makefile`: `test: ## Run tests with race detection` →
`go test -race -count=1 ./...`). Dev/CI parity for the test step already
holds today by this literal-command-match convention, not by CI invoking
`make check` as one target. This design's "one entry point" requirement
(below) follows the same convention: no new Makefile target this tier's
tests must be routed through, and no new CI step whose command differs from
what a developer runs. `ethos` is not installed anywhere in `test.yml` today
— that is the gap `beadle-axj` names and this design closes.

### `cmd/beadle-daemon/main_test.go` — the daemon-startup half of gate 3

`internal/daemon` is not the only package with pipeline-adjacent tests: the
`cmd/beadle-daemon` binary's own `main_test.go` already covers, in full and
with no external-tool dependency, the "absent/ambiguous `daemon.json` →
command loading disabled entirely, never an unsigned fallback" half of the
mission brief's gate-3 description — `cmd/beadle-daemon/main.go`'s
`resolveDaemonOwnerKeyID` and `loadDaemonCommands` (line citations into
`main.go` omitted per the citation-style note above: it is one of the six
files PRs #257/#259/#260 already moved), exercised by `main_test.go:77-272`'s
nine `Test*`
functions, all pure (`t.TempDir()`, a fake `slog.Handler`, no `gpg`, no
`ethos`, no daemon.json in the real `$HOME`). This tier's gate-3 work is
scoped down accordingly — see "Gate 3's scope" below the gate table.

## The four gates

Each gate gets its own positive and negative case. All four already have
partial coverage; the gap is (a) `beadle-8gt`'s gate (4) skips in CI, and (b)
no test exercises all four gates *together*, through the real `OnNewMail`
entry point, the way a live message actually would.

| # | Gate | File:line | Positive case | Negative case | Induced by |
|---|------|-----------|----------------|----------------|------------|
| 1 | Trust classification | `internal/daemon/handler.go:150-161` (`verifyTrust`, called from `OnNewMail`) | PGP-signed or Proton-trusted message from a granted sender creates a pipeline | Unsigned message from the same sender creates nothing | `testserver.AddRawMessage` with a signed/trusted/bare RFC822 body (three builders above) |
| 2 | Per-contact `x` permission | `internal/contacts/permissions.go:75-89` (`CheckPermission`), gated at `internal/daemon/handler.go:143-148` | `rwx` or `r-x` sender's verified message creates a pipeline | `rw-` sender's verified message creates nothing, and an unknown sender (no contact record) is skipped before permission is even checked (`handler.go:137-141`) | `testenv.AddContact(name, addr, "rw-")` vs `"rwx"`; simply never adding a contact for the unknown-sender case |
| 3 | Command-file signature | `internal/daemon/signature.go:133-167` (`VerifySignature`), gated at `internal/daemon/command.go:142-150` (`loadCommand`) | A command file signed by the configured owner key loads | An unsigned file, a file signed by a different key, and a file signed by an *expired* owner key are all rejected by `VerifySignature` when a real owner key is configured | `CanonicalCommandBytes` (`signature.go:100-116`) + an ephemeral GPG key (`testenv.GenKey`/`GenKeyNoExpiry`, `internal/testenv/gpg.go:44-80`) to sign; a second, unrelated key to produce a wrong-key rejection |
| 4 | Contract validity against the real ethos CLI | `internal/daemon/pipeline.go`'s `buildStageContract` validated by the real `ethos mission create` binary | A generated stage contract is accepted | A contract missing a required field is rejected — see "Gate 4's existing test" below for the negative control this design adds | `isolatedEthosEnv`-equivalent (ported into `testenv`, see below) + the real `ethos` binary, pinned per §"Pinning ethos" |

**Gate 3's scope, and a sub-case this design does not need to add.** The
mission brief that opened this design also names a fourth command-signature
sub-case: "an absent or ambiguous `daemon.json` disables command loading
entirely (zero commands, not unsigned fallback)." That sub-case is real
(`docs/ARCHITECTURE.md`'s "Design Invariants" § zero agent authority) but it
does not live inside `internal/daemon.LoadCommands`/`VerifySignature` at
all — reading `command.go:142-153` shows that when `ownerKeyID == ""`,
`loadCommand` skips `VerifySignature` entirely and *loads the command
unsigned*, exactly like it did before signing enforcement existed. The
zero-commands behavior is enforced one package up, in
`cmd/beadle-daemon/main.go`: `resolveDaemonOwnerKeyID` turns an absent,
unreadable, or ambiguous `daemon.json` into `loadCommandsEnabled == false`,
and `loadDaemonCommands` never calls `daemon.LoadCommands` at all when that
flag is false — returning an empty
map instead of calling `LoadCommands` with an empty `ownerKeyID`, which is
exactly the backdoor the invariant closes (`loadDaemonCommands`'s own doc
comment in `main.go` states this explicitly). That resolution logic already has full,
dependency-free unit coverage that needs nothing this design adds:
`cmd/beadle-daemon/main_test.go`'s
`TestResolveDaemonOwnerKeyID_MissingConfigIsSilent`,
`_UnreadableConfigLogsError`, `_UnresolvableOwnerLogsError`,
`_DirectFingerprintResolves`, `_AmbiguousConfigDisablesLoading`, and
`_MalformedFingerprintDisablesLoading` (`main_test.go:77-180`, six tests),
plus `TestLoadDaemonCommands_DisabledNeverCallsLoadCommands`,
`_EnabledLoadsCommands`, and `_AbsentConfigNeverCallsLoadCommands`
(`main_test.go:197-272`) — none of which touch `gpg`, `ethos`, or any other
external binary, so none of them carry the dev/CI-parity risk this design
exists to close. This design's survey missed this file on a first pass; it
is corrected here rather than left as a gap, per this repo's "no
pre-existing issue" rule. **Consequence for scope:** gate 3's row above, and
the combined end-to-end test below, cover only the three sub-cases that
`internal/daemon` itself is responsible for (unsigned, wrong-key, expired-key,
all with a *configured* owner key) — the absent/ambiguous-config sub-case is
`cmd/beadle-daemon`'s responsibility, already discharged, and out of this
design's write set.

Gate 4's existing test, `TestBuildStageContract_ValidatesAgainstRealEthosCLI`,
is **kept exactly as written** — it is already the correct shape (build the
contract, hand it to the real binary, assert success, clean up via
`ethos mission abandon` in `t.Cleanup`). The only change gate 4 needs is
provisioning: once `ethos` is guaranteed present (see below), its `t.Skip`
becomes dead code and must convert to a `t.Fatalf` naming the remedy —
`"ethos not found on PATH:
install it via go install github.com/punt-labs/ethos/v4/cmd/ethos@$(ETHOS_VERSION)"`
— a fail-with-remedy message, per the hard constraint, not a skip.

**Gate 4's negative control.** The existing test only asserts success. That
is a real gap, independent of the "no invalid contract that should be
accepted" claim above: without a case that must be *rejected*, a systemic
harness bug — broken `$HOME`/`$ETHOS_REPO_ROOT` isolation, a `PATH`
misconfiguration that silently no-ops the CLI call, an error return that
gets swallowed on the Go side — could make every `ethos mission create`
invocation spuriously report success regardless of input, and gate 4 would
never notice. That is a second-order instance of exactly the failure mode
this whole design exists to close: a test whose only assertion is "it
passed" cannot distinguish "the schema accepted this contract" from "the
harness never actually asked the schema." The real ethos schema does have
genuine required-field validation to exercise:
`internal/mission/store.go:599`'s `Store.Create` (the function
`ethos mission create` calls) enforces `internal/mission/validate.go:190`'s
`evaluator.handle is required` and `validate.go:206`'s `write_set must
contain at least one entry` before a contract is ever persisted — both
independently confirmed by reading `punt-labs/ethos` at the pinned
`v4.16.0` tag (identical line numbers to the `v4.15.0` reading this
citation originally recorded — the module-path rename between those two
tags touched only `go.mod`/`cmd/ethos`, not `internal/mission`). This
design adds a second test,
`TestBuildStageContract_RealEthosCLIRejectsMalformedContract`, alongside
the existing one: build a stage contract the same way, delete its
`evaluator.handle` field (or, as a second subtest, empty its `write_set`),
hand the mutated contract to the same real `ethos mission create` binary
under the same isolation, and assert the command **fails** with a
non-zero exit and the expected validation message — never `t.Skip`s, never
`t.Cleanup`s an `ethos mission abandon` (there is no mission ID to
abandon, since creation never succeeded).

## New: the combined end-to-end test

The four gates above are exercised individually today (three of them; gate 4
stands alone). None of the existing tests drives all four *together* through
`OnNewMail`, the way `beadle-8gt` was actually found — by running the real
path once and watching it break. This design adds exactly one new test,
`TestOnNewMail_EndToEnd` in a new `internal/daemon/e2e_test.go` (`package
daemon`, matching the existing white-box convention every other `_test.go`
in this package already uses), that:

1. Builds a `testenv.Env` and a `testserver.Fixture`, adds one `rwx`
   contact, and seeds one PGP-signed message from that contact
   (gates 1 and 2, reusing the exact builders `handler_test.go` already has —
   relocated, not rewritten; see "Assertion surface" below).
2. Writes one signed `Command` YAML to a scratch commands directory and
   loads it with `LoadCommands` (gate 3), using a `FakeSpawner` — see next
   section — wired into a real `ClaudeRunner`, a real `Executor`, and a real
   `StubPlanner` (already exported for tests, `planner.go:71-80`) that
   returns exactly one `CommandCall` naming the loaded command.
3. Constructs a real `MailHandler` via the exported `NewMailHandler`
   (`handler.go:53-77`) with that `Spawner`, a real `MissionTemplate{TmpDir:
   t.TempDir()}`, the real `Executor`'s `Planner`/`Commands`/`Runners`.
4. Calls `handler.OnNewMail(1)`, then `handler.Stop()` — `Stop` cancels the
   handler's context and calls `h.wg.Wait()` (`handler.go:80-83`), which
   blocks until the background pipeline goroutine spawned inside `OnNewMail`
   (`handler.go:169-217`) has actually finished. This is the deterministic
   synchronization point the design needs (see "Bypassing the poll loop"
   below) — no sleep, no polling loop, no `require.Eventually`.
5. Asserts: the `FakeSpawner` recorded exactly one call (gate 4 ran —
   `ClaudeRunner.Run` only calls `Spawner.Run` after
   `createMissionFromContract` succeeds against the real `ethos` binary);
   the real ethos mission the test created is visible via `ethos mission
   show` in the isolated `ETHOS_REPO_ROOT` and gets cleaned up in
   `t.Cleanup` the same way `TestBuildStageContract_ValidatesAgainstRealEthosCLI`'s
   `ethos mission abandon` cleanup already does.
6. A second sub-test repeats the same setup with the message unsigned
   (gate 1 negative) and a third with an `rw-` contact (gate 2 negative);
   both assert `FakeSpawner` recorded zero calls and no ethos mission was
   created.

This is one new test function (with subtests for the negative cases), not a
parallel harness that duplicates `handler_test.go`'s existing gate-1/2-only
coverage — that coverage stays where it is.

## Decisions

### Where the harness lives

Two shapes were considered:

- **Unexported helpers inside `package daemon`.** This is the existing
  convention — every `_test.go` file in `internal/daemon` is white-box
  (`package daemon`, not `package daemon_test`), and they already share
  helpers freely across files in the same test binary (`discardLogger()`,
  `testCommands()`, `testLogger()`, `shortGPGHome()`, `gpgBinary()`). Nothing
  this tier needs actually requires unexported access — `Command`,
  `CanonicalCommandBytes`, `LoadCommands`, `NewMailHandler`, `OnNewMail`,
  `Spawner`, `WorkerResult`, `StubPlanner` are all already exported — so this
  option is available, not just convenient.
- **A new `internal/daemontest` package**, parallel to `internal/testserver`
  and `internal/testenv`, both of which already exist as separate,
  general-purpose, importable packages rather than living inside the
  packages whose tests consume them.

**Decision: split by reuse potential, matching the precedent those two
existing packages already set.** The signing helper (build and sign a
`Command`, gate 3) and `FakeSpawner` (gate 4's fake seam) go in a new
`internal/daemontest` package: they depend only on `internal/daemon`'s
exported surface, so a shared package costs nothing (the tradeoff named in
the mission brief — "cannot touch daemon's unexported surface" — never
binds, because nothing here needs to) and gains the same thing
`testserver`/`testenv` already gained: importability from any future
consumer without a `package daemon` import cycle. `internal/mcp`'s own tests
are one plausible future consumer if MCP tooling ever needs to construct a
signed command fixture. The combined end-to-end test itself
(`TestOnNewMail_EndToEnd`) stays in `internal/daemon` as `package daemon`,
matching every other daemon test — it needs no unexported access either, but
co-locating it next to `handler_test.go`'s existing RFC822 builders
(`buildPGPSignedRFC822`, `buildTrustedRFC822`) is what "cheap to extend"
means in practice: a future gate-5 test is one function in a file that
already has every fixture it needs one scroll away, not an import plus a
context switch to a different package.

### The assertion surface: what a new gate test costs to add

The cost target is: **one function, in `internal/daemon/e2e_test.go` or
`internal/daemon/handler_test.go`, that builds a fixture and calls
`OnNewMail` once.** Concretely, after this design ships, adding a fifth gate
(hypothetically) costs:

1. One fixture builder, if the gate needs a new kind of induced state not
   already covered by `testserver.AddRawMessage`, `testenv.AddContact`, or
   `daemontest.SignCommand` (new, gate 3's helper).
2. One `NewMailHandler(...)` call — every argument already has a working
   default (`nil` spawner/templates for the synchronous gate-1/2-only path
   `handler_test.go` already uses; the full `FakeSpawner` + real `Executor`
   wiring from `TestOnNewMail_EndToEnd` for anything downstream of the
   planner).
3. One `handler.OnNewMail(n)` + `handler.Stop()` pair — no sleep, no manual
   goroutine synchronization, because `Stop()` already blocks on the
   handler's own `sync.WaitGroup`.
4. Assertions against either the mock (`mockMissionCreator`,
   `handler_test.go:24-32`) or the `FakeSpawner`'s recorded calls —
   whichever layer the new gate's effect is visible at.

No new server process, no new isolation mechanism, no new build tag. The one
thing a gate-3-or-later test must remember is to route through
`testenv`'s (post-this-design) ethos isolation rather than inventing its own
— named explicitly in the next section so it is not rediscovered by a future
author the hard way.

### Pinning `ethos`

`ethos` is a separate Go module — as of `v4.16.0` (commit `13f83dc`), the
module path is `github.com/punt-labs/ethos/v4`, matching the major-version
suffix Go modules require — with its CLI at `cmd/ethos`
(`../ethos/go.mod:1`,
`../ethos/cmd/ethos`). Before this rename,
`ethos` tagged `v4.x` releases under the unsuffixed `github.com/punt-labs/ethos`
path, which Go's module resolver cannot treat as a `v4` module at all — a `go
install .../ethos@v4.15.0` resolved only through an incompatible v1
pseudo-version, not the real tag, making a pinned install-by-tag effectively
impossible. Verified against a clean module cache: `go install
github.com/punt-labs/ethos/v4/cmd/ethos@v4.16.0` now resolves cleanly as a
real tagged version with a checksum-database (`h1:`) hash. This repo's Makefile
already has the exact pattern to follow for a versioned external tool that
both a developer and CI must agree on byte-for-byte — `STATICCHECK_VERSION`,
`GOLANGCI_LINT_VERSION`, and `GOVULNCHECK_VERSION` (`Makefile`, top-of-file
comment: "Pinned once here. CI invokes these via `make vet`, `make
staticcheck`, `make lint-strict`, and `make vulncheck` rather than
duplicating the versioned commands in `.github/workflows/test.yml`, so local
and CI can never drift apart.") Those three are installed transiently via `go
run pkg@version` per invocation, which works for a tool invoked as a Go
subcommand but not for `ethos`, which the tests must find via
`exec.LookPath("ethos")` (`TestBuildStageContract_ValidatesAgainstRealEthosCLI`,
in `pipeline_test.go`) and invoke as an independent binary
(`createMissionFromContract` in `mission.go`, `ClaudeRunner.Run`'s
`ethos mission close` call in `runner.go`). `ethos` therefore needs an actual
installed binary on `PATH`, not a per-invocation `go run`.

**Decision:** add one variable, `ETHOS_VERSION`, to the same pinned-version
block at the top of the `Makefile`, and one new target, wired as a real
prerequisite of `test` (not a standalone target a developer must remember to
invoke):

```makefile
ETHOS_VERSION := v4.16.0

tools-ethos: ## Install the pinned ethos CLI (needed by internal/daemon's gate-4 tests)
    go install github.com/punt-labs/ethos/v4/cmd/ethos@$(ETHOS_VERSION)

test: tools-ethos ## Run tests with race detection
    go test -race -count=1 ./...
```

(shown indented with spaces for markdownlint; the actual Makefile recipe
lines the implementation mission writes must use a real tab, as `make`
requires.) `check: lint lint-strict vulncheck docs test` (`Makefile:89`)
already depends on `test`, so this one edge — `test: tools-ethos` — is
sufficient to make `tools-ethos` a real prerequisite of both `make test` and
`make check`, matching the prose below: a developer never runs
`make tools-ethos` by hand except the very first time (`go install` is a
few-hundred-millisecond no-op once the binary is already on `PATH` at the
pinned version — re-running it on every `make test` is the same shape as
`staticcheck`'s existing `go run pkg@version` re-invocation on every `make
staticcheck`, just installing to `PATH` instead of the module cache's
`go run` binary cache).

Every file that must reference `ETHOS_VERSION`, so a version bump is one
edit:

- **`Makefile`** — the `ETHOS_VERSION` variable, the `tools-ethos` target
  (new), and the `test: tools-ethos` prerequisite edge on the existing
  `test` target (`Makefile:83-84`).
- **`.github/workflows/test.yml`** — a new step, `run: make tools-ethos`,
  placed before the `Test` step (`test.yml:37-38`), so the runner's `PATH`
  has `ethos` before `go test` starts. `go install` places the binary at
  `$(go env GOPATH)/bin`; `actions/setup-go` (`test.yml:18-20`) does not add
  that to `$GITHUB_PATH` automatically. **Decision:** the new step commits to
  `echo "$(go env GOPATH)/bin" >> "$GITHUB_PATH"` (run once, immediately
  after `make tools-ethos`, in the same or a following step) rather than
  threading an explicit `$(go env GOPATH)/bin/ethos` path through the test
  code — this keeps `pipeline_test.go`'s `exec.LookPath("ethos")` call
  working completely unmodified, on both a developer's machine (where
  `go install` already puts `ethos` on a `PATH` most Go setups already
  export) and CI (where `$GITHUB_PATH` is the mechanism GitHub Actions
  documents for exactly this "a step installed a binary, later steps in the
  same job need to find it" case). The implementation mission writes this
  step, not a placeholder.
- **`docs/TESTING.md`** — a line in "Key Rules" naming `ethos` (pinned via
  `Makefile`'s `ETHOS_VERSION`) as a required tool for `internal/daemon`'s
  test suite, the same way GPG homedir rules are already documented there.
- **A developer's local machine** — nothing manual. The `test: tools-ethos`
  edge above means `make test` and `make check` provision `ethos`
  automatically; a developer is never told to remember a separate command.

No `t.Skip` survives this: once `tools-ethos` is a real prerequisite of
`make check`/CI (`test: tools-ethos`, plus the CI step above), `ethos`
missing means the *provisioning step* failed loudly (a `go install`
network/build error), not that a downstream test quietly skipped. The one
remaining defensive `exec.LookPath("ethos")` check inside the test itself
converts from `t.Skip` to `t.Fatalf` with the exact install command in the
message, matching the hard constraint's "the test
FAILS with a message naming what to install."

**CI budget.** `.github/workflows/test.yml`'s single job carries
`timeout-minutes: 10` (`test.yml:15`) for the whole sequence — vet,
staticcheck, `lint-strict`, `vulncheck`, `lint-shell`, then `go test`.
Installing `ethos` adds a `go install` of a second module and its transitive
dependency graph, which is new network and build cost on any cache miss.
This design's stated assumption: `actions/setup-go` (`test.yml:18-20`)
caches `$(go env GOMODCACHE)` and `$(go env GOCACHE)` by default (`cache:
true` is the action's default; `test.yml` does not override it), so the
first CI run after this change lands pays the full `go install` network
cost once, and every run after that reuses the same cache directories.
That cache key is derived from `hashFiles('**/go.sum')` in *this* repo — and
`ethos` is invoked as a standalone binary, never imported as a Go module
dependency of `beadle`, so this repo's `go.sum` never references it. A
future `ETHOS_VERSION` bump therefore does not itself bust the cache key: it
either hits an already-warm `$(go env GOMODCACHE)` from a prior run (likely,
since the cache directory persists across cache-key hits and `go install`
only needs to fetch what is not already present) or falls through to a live,
uncached fetch of the delta — the same shape as the very first run, not
worse. **Stated fallback**, if either the first run or a version-bump run
blows the ten-minute budget: bump `timeout-minutes` in `test.yml`. This is a
one-line change the implementation mission makes if and when CI timing shows
it is needed — not a redesign, and not silently assumed away.

### Bypassing the poll loop

`internal/email/poller.go`'s `loop` (`poller.go:184-197`) calls `p.poll()`
immediately, then ticks on `interval`. `poll()` (`poller.go:199-249`) always
updates `lastSeen`/`lastCheck`, but `notify` (`poller.go:257-267`) only fires
`onNewMail` when `!first && unseen > prev` (`poller.go:261`) — the very first
poll of a poller's lifetime never fires it, by design (the doc comment at
`poller.go:251-256` explains why: "startup does not prompt for mail already
waiting"). The minimum configurable interval is `1m`
(`internal/email/config.go:182-190`'s `validPollIntervals` map, whose keys
are echoed in `poller.go:104`'s error message). Put together: a test that
tries to observe new mail via the real `Poller` needs at least two ticks
(the immediate first poll, which cannot fire the callback, and a subsequent
tick, which can) with a minimum real wall-clock gap of one minute between
them — far too slow for a pre-commit gate, and it would make the test's
timing a hidden dependency on `Poller`'s internals rather than on the
gates this tier actually owns.

**Decision: bypass the `Poller` entirely.** `MailHandler.OnNewMail` is an
exported method (`handler.go:87`) that the `Poller` calls as its
`NewMailFunc` callback (`poller.go:17`), but nothing about `OnNewMail`
requires a `Poller` to invoke it — `handler_test.go`'s existing `TestOnNewMail`
(`handler_test.go:34-166`) already calls `handler.OnNewMail(uint32(len(tt.messages)))`
directly, with no `Poller` in the picture at all. This tier does the same:
seed the fixture, construct the handler, call `OnNewMail(n)` once, and (for
the async-goroutine path introduced by a non-nil `Spawner`/`Templates`) call
`Stop()` to block for completion. This is not a workaround for `Poller`'s
timing — it is testing the right layer: `Poller` has no security-relevant
behavior of its own (it is a dumb ticker plus an unseen-count comparison);
the gates this design covers all live inside `OnNewMail` and downstream, so
driving `OnNewMail` directly is testing exactly what needs testing, at the
cheapest possible cost. A future test of `Poller` itself (e.g., its
first-poll-suppression or its interval validation) is a `Poller`-only unit
test with no IMAP fixture at all — `poller.go`'s `notify` is already
"unit-testable without a live mailbox" by its own doc comment
(`poller.go:255-256`) — and is out of scope for this design.

### What stays real, what gets faked

Default is real. Each fake below earns its place with a specific reason it
cannot be real inside a pre-commit-gate test.

| Component | Real or faked | Reason |
|---|---|---|
| IMAP/SMTP transport | **Real** (`testserver`) | In-process, no network, already proven fast (`docs/TESTING.md`'s "MCP handler" row: < 3s) |
| PGP signing/verification | **Real** (`gpg` CLI, ephemeral keys) | The whole point of gates 1 and 3 is that real `gpg --verify`/`--status-fd` output is classified correctly; a fake verifier would test the classifier's *shape*, not its correctness |
| Trust classification (`verifyTrust`) | **Real** | Production code under test |
| Contact permission (`CheckPermission`) | **Real** | Production code under test; cheap, pure, no I/O |
| Command-file loading/signature (`LoadCommands`, `VerifySignature`) | **Real** | Production code under test |
| Planner | **Faked** (`StubPlanner`, already exported for this purpose, `planner.go:71-80`) | The planner's own correctness (rule matching, LLM planning) is out of scope for this tier — it exists to test the daemon's gates, not the planning logic, which has its own tests (`planner_test.go`) |
| `Executor`, `ClaudeRunner` | **Real** | These build and submit the actual stage contract gate 4 must validate; faking either one is what let `beadle-8gt` ship |
| `ethos` CLI (mission create/abandon/close) | **Real**, isolated | This is gate 4 itself — the only thing that catches a schema mismatch is the real binary. Isolated via the ported `isolatedEthosEnv` mechanism (below), never via a fake |
| `Spawner` (the Claude Code worker subprocess) | **Faked** (`FakeSpawner`) | Named explicitly in the mission brief as the one seam that should stay faked: it is the only component whose real form is "spawn a real LLM session," which is unbounded in time, cost, and non-determinism — none of which this tier can afford in a pre-commit gate. Everything upstream of it (contract construction, mission creation) is still exercised for real; only the LLM turn itself is replaced with a canned `WorkerResult` |

**Porting `isolatedEthosEnv` into `testenv`.** `isolatedEthosEnv` (in
`pipeline_test.go`) currently isolates ethos only for the two `exec.Command`
calls that explicitly adopt its returned `[]string` as `.Env` (the
`createCmd.Env` and `abandonCmd.Env` assignments inside
`TestBuildStageContract_ValidatesAgainstRealEthosCLI`). `testenv.New`
already redirects `$HOME` via `t.Setenv` (`env.go:44`), which — because
`t.Setenv` mutates the real process environment for the test, not a
per-command override — already protects every `exec.Command` in the code
under test that inherits `os.Environ()`, including the ones gate 4 needs
protected (`createMissionFromContract` in `mission.go`, `ClaudeRunner.Run`'s
`ethos mission close` call in `runner.go`) with zero extra plumbing at each
call site. The one piece `testenv.New` is missing is `$ETHOS_REPO_ROOT`.

**Decision:** add `testenv.IsolateEthos(t *testing.T)`, called from
`testenv.New` (so every `testenv.Env` gets it automatically, since any test
that creates ethos identities is a candidate for eventually driving a real
`ethos mission create`), that ports `isolatedEthosEnv`'s directory layout —
global scratch dir with `archetypes` symlinked, repo scratch dir with
`identities`/`personalities`/`roles`/`teams`/`talents`/`writing-styles`
symlinked — but calls `t.Setenv("ETHOS_REPO_ROOT", scratchRepo)` instead of
returning an `[]string` env slice. `isolatedEthosEnv` itself is then
redundant with `testenv.IsolateEthos` and should be deleted in the same
change that introduces it, with `TestBuildStageContract_ValidatesAgainstRealEthosCLI`
switched to rely on ambient `t.Setenv`-based isolation (dropping its explicit
`createCmd.Env = isolatedEnv` / `abandonCmd.Env = isolatedEnv` assignments)
rather than maintaining two parallel isolation mechanisms that could drift.

**The property the port must preserve, not just the directory layout.**
The fix is already on `main`: mission `m-2026-08-30-051` (`beadle-bvo`)
merged as PR #257 on 2026-08-30. `internal/daemon/pipeline_test.go`'s
`isolatedEthosEnv` environment-filtering loop now strips every `HOME=` and
every `ETHOS_*` variable — its filter condition reads
`strings.HasPrefix(kv, "HOME=") || strings.HasPrefix(kv, "ETHOS_")` — before
re-adding the scratch values, and `isolatedEthosEnv`'s doc comment states
that property directly (per the citation-style note above, this passage
names the function and quotes the predicate rather than a line range: this
exact passage cited `pipeline_test.go:942-949`/`:892-917` in an earlier
draft, and PR #260 had already shifted both line numbers by the time that
draft was reviewed): *every* `ETHOS_*` variable is stripped, not just
`ETHOS_REPO_ROOT`, because the calling agent's own session sets
`ETHOS_SESSION`, and leaving it in place would let the exec'd `ethos` binary
resolve a real session identifier while its backing storage sits under the
redirected scratch roots — an ambient-state leak that would violate this
design's own "zero dependence on ambient developer state" hard constraint.
`testenv.IsolateEthos` ports from this current state: the general property
(zero `ETHOS_*` ambient leakage into any `exec.Command("ethos", ...)` this
design's tests spawn), not a re-derivation of the filter's own diff.

### Live-data symlinks: a stated determinism tradeoff, not a silent one

`isolatedEthosEnv`'s read-only subtrees (`archetypes` globally;
`identities`/`personalities`/`roles`/`teams`/`talents`/`writing-styles`
under the repo root) are symlinked to the real, *live*, mutable files on
disk — not a snapshot taken at test-start. `testenv.IsolateEthos` inherits
that exact mechanism, so it inherits the same property: two calls to
`ethos mission create` inside one test run, or two separate test runs
started moments apart, can observe different identity/role/team data if
another session in this repo's shared, multi-agent environment
(`docs/ARCHITECTURE.md`'s own working assumption for this codebase) edits
one of those files mid-run, and a developer's dirty working tree (staged or
unstaged edits under `.punt-labs/ethos/`) validates against that dirty
state rather than the last-committed one a clean CI checkout would see.

**Decision: accept this explicitly as a stated tradeoff, not a gap to close
in this design.** Three reasons: (1) it is not a new risk this design
introduces — `isolatedEthosEnv` already carries it today, unstated, in the
gate-4 test this design keeps "exactly as written"; naming it here is a
correction of an existing silence, not new exposure. (2) It does not touch
CI's actual failure mode: a GitHub Actions runner checks out one immutable
commit for the whole job, so no concurrent writer can touch those subtrees
during a CI run — the race is real only on a developer's own machine,
during local `make test`, which is exactly the environment where a human
can `/who`-check for other active sessions before running tests, per this
repo's own git-safety convention. (3) A dirty working tree validating
against its own uncommitted edits to `.punt-labs/ethos/` is arguably
*correct* behavior for a local run — the test is checking what is actually
on disk, not a stale committed snapshot — and only becomes a genuine
divergence from CI in the narrow case where those particular files are the
ones dirty. Snapshotting those subtrees into the scratch tree at test start
(copy instead of symlink) would remove the residual local-only race at the
cost of a copy step on every test run and a second code path that could
itself drift from what `isolatedEthosEnv` proved out; this design does not
propose it. If the concurrent-edit race is ever observed to actually flip a
test's outcome, that observation — not speculation — is the trigger for a
follow-up bead to add snapshotting.

### `gpg`: the same defect, smaller blast radius

`internal/daemon`'s four `t.Skip("gpg not installed")` sites
(`handler_test.go:37,174,203`, centralized behind `signature_test.go:35-42`'s
`gpgBinary` helper for the fourth) are the identical failure mode this whole
design exists to close, just for a different missing tool. `docs/TESTING.md`
already states GPG-based tests must not skip (see above). This design
**converts these two files' skip sites to `t.Fatalf`** (naming `apt install
gnupg` / `brew install gnupg` as the remedy), since they sit squarely inside
the four gates this design owns (gates 1 and 3 both depend on real `gpg`).
GitHub's `ubuntu-latest` runner image ships `gnupg` preinstalled, so this
conversion should be a no-op in CI and only ever fire on a genuinely
misconfigured developer machine — which is the intended behavior. Further
`t.Skip("gpg not installed")` sites remain in `internal/pgp` (`verify_test.go`,
`decrypt_test.go`, `encrypt_test.go`, `expiry_test.go`, `probe_test.go`,
`sign_test.go`) and `internal/email` (`send_test.go`, `reply_test.go`) — the
same defect but outside this mission's write set (`docs/daemon-test-harness.md`
only) and outside the four named gates. **Recommendation, not part of this
mission:** file a follow-up bead to convert those sites the same way, since
they are load-bearing for the *existing* PGP integration tier `docs/TESTING.md`
already lists as `< 5s, tag: none` — i.e., already claimed to run
unconditionally, just not actually enforced.

This document originally put a number on those sites ("eleven"). That number
was wrong — the real count is more than 3x higher — and it survived two
evaluator rounds here, was copied verbatim into a follow-up bead, and was
then transcribed again into `docs/TESTING.md`, all without anyone actually
running the count. That chain is the exact failure mode this whole design
exists to close, reproduced in miniature inside the design document itself.
The fix is not a corrected number — a corrected number rots the same way the
wrong one did, the moment a test is added or removed. Reproduce it instead:

```bash
grep -rn 'gpg not installed' internal/ --include='*_test.go' | grep -v internal/daemon | wc -l
```

## Dev/CI invocation

Identical to how `test.yml` already achieves parity for the existing test
step (see the survey above) — no new named target the tier's tests are
gated behind:

```bash
# Developer, every run — same commands as today, now provisioning ethos
# automatically via the `test: tools-ethos` prerequisite edge:
make test          # tools-ethos, then go test -race -count=1 ./...
make check         # lint + lint-strict + vulncheck + docs + test(+tools-ethos)

# CI (.github/workflows/test.yml), new step inserted before "Test":
- name: Install ethos
  run: make tools-ethos
- run: echo "$(go env GOPATH)/bin" >> "$GITHUB_PATH"
- name: Test
  run: go test -race -count=1 ./...   # unchanged — same literal command
```

No build tag, no `-tags=integration` (that flag already exists for the
separate `IMAP/SMTP` row in the pyramid and this tier does not need it — it
runs under the plain `test` target, same as `MCP handler` does today), no
environment variable a developer must remember to set, no branch on
`CI=true`, and — per the "Pinning `ethos`" decision above — no manual
provisioning step a developer must remember either: `test: tools-ethos`
makes `make test`/`make check` self-provisioning, and CI's two new steps
install the same pinned binary onto `$GITHUB_PATH` the same way.

## Summary of files touched by the implementation mission

Named here so the implementation mission's write-set is a direct
transcription of this design, not a new negotiation:

- `Makefile` — `ETHOS_VERSION` variable, `tools-ethos` target, and the
  `test: tools-ethos` prerequisite edge on the existing `test` target so
  `make test`/`make check` provision `ethos` automatically.
- `.github/workflows/test.yml` — one new step (`make tools-ethos`) and one
  `$GITHUB_PATH` append (`echo "$(go env GOPATH)/bin" >> "$GITHUB_PATH"`),
  both before the `Test` step.
- `docs/TESTING.md` — document `ethos` as a required tool, pinned via
  `Makefile`'s `ETHOS_VERSION`, alongside the existing GPG rules.
- `internal/testenv/env.go` (or a new `internal/testenv/ethos.go`) —
  `IsolateEthos(t)`, called from `New`, preserving the no-`ETHOS_*`-leakage
  property described in "Porting `isolatedEthosEnv` into `testenv`" below.
- `internal/daemon/pipeline_test.go` — delete `isolatedEthosEnv`; update
  `TestBuildStageContract_ValidatesAgainstRealEthosCLI` to rely on ambient
  isolation from `testenv.IsolateEthos` (or an equivalent standalone call, if
  this specific test does not otherwise use `testenv.New`); convert its
  `t.Skip` to `t.Fatalf` with the install remedy; add
  `TestBuildStageContract_RealEthosCLIRejectsMalformedContract` (gate 4's
  negative control, see "Gate 4's existing test" above).
- `internal/daemon/handler_test.go`, `internal/daemon/signature_test.go` —
  convert the four `t.Skip("gpg not installed")` sites to `t.Fatalf` with the
  install remedy.
- `internal/daemontest/` (new package) — `SignCommand` (gate-3 signing
  helper built on `CanonicalCommandBytes` + `testenv.GenKey`/`GenKeyNoExpiry`)
  and `FakeSpawner` (implements `daemon.Spawner`, records calls, returns a
  canned `WorkerResult`).
- `internal/daemon/e2e_test.go` (new file) — `TestOnNewMail_EndToEnd` and its
  two negative-case subtests, per "New: the combined end-to-end test" above.

No production Go file changes — every gate this tier exercises already
exists and is already correct (gate 4's regression coverage already caught
`beadle-8gt` once the test itself actually runs). This is purely a testing
and provisioning change.

## Proposed `DESIGN.md` ADR entry

To be pasted into `DESIGN.md` at merge time of the implementation PR:

```markdown
## ADR: Daemon mail-triggered pipeline gets a dev/CI-parity test tier

**Date**: <merge date>
**Status**: Accepted

**Context**: The daemon path (email → trust classification → per-contact
permission → planner → executor → ethos mission create → worker spawn) had
never actually run until a manual live test surfaced four defects, including
a mission-contract shape ethos's real schema rejects outright (`beadle-8gt`).
A regression test for that exact defect already existed
(`TestBuildStageContract_ValidatesAgainstRealEthosCLI`) but skipped in CI
because `.github/workflows/test.yml` never installed `ethos` — so the one
test that would have caught the shipped defect never ran where it mattered.

**Decision**: Pin `ethos` via a `Makefile` `ETHOS_VERSION` variable and a
`tools-ethos` target (matching the existing `STATICCHECK_VERSION`/
`GOLANGCI_LINT_VERSION`/`GOVULNCHECK_VERSION` pattern), install it in CI
before the test step, and convert every `t.Skip` on a missing `ethos` or
`gpg` binary inside `internal/daemon` to a fail-fast `t.Fatalf` naming the
install remedy. Add one combined end-to-end test
(`TestOnNewMail_EndToEnd`) that drives all four security-relevant gates
(trust classification, per-contact `x` permission, command-file signature,
real-ethos contract validity) through the actual `OnNewMail` entry point in
one pass, bypassing `email.Poller` entirely (calling `OnNewMail` directly,
then `MailHandler.Stop()` for deterministic goroutine drain) since `Poller`'s
first-poll suppression and 1-minute minimum interval make it unusable for a
pre-commit-gate test and carry no security-relevant behavior of their own.
Only `daemon.Spawner` (the real-LLM-subprocess seam) is faked; everything
upstream, including the real `ethos` CLI, runs for real inside a
`testenv`-isolated `$HOME`/`$ETHOS_REPO_ROOT` (porting `isolatedEthosEnv`'s
proven directory layout into `testenv.IsolateEthos` so every `exec.Command`
that inherits `os.Environ()` is protected, not just ones that opt in
explicitly).

**Rejected alternatives**:

- *Keep the `t.Skip` and rely on developer discipline to notice.* This is
  the exact failure mode that shipped `beadle-8gt` — rejected on its face.
- *A build tag (`integration`) gating these tests, run only in a separate CI
  job.* Rejected: the mission's hard constraint requires one entry point
  identical in dev and CI; a build tag reintroduces exactly the
  dev/CI-divergence this design exists to close, and the existing
  `integration`-tagged IMAP/SMTP tier already shows the failure mode this
  guards against (a developer's `make check` silently not running it).
- *Fake the `ethos` CLI with a hand-rolled stub that returns success.* This
  is precisely what let `beadle-8gt` through in the first place — a test
  that only checks the Go string, never the real schema, does not catch a
  schema mismatch and must not be trusted to catch a recurrence.
- *Drive the real `email.Poller` with a very short custom interval.* Not
  possible without changing production code: `validPollIntervals` bottoms
  out at `1m`, and the first poll of any `Poller`'s lifetime never fires the
  new-mail callback by design. Calling `OnNewMail` directly tests the
  security-relevant layer without depending on `Poller` internals at all.
```
