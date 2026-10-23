# Design: resolving MCP server binaries at spawn time, not from the worker's PATH

Design for bead `beadle-tsku` (P2, found by Qodo reviewing PR #280).

## What this change alters

`WorkerSpawner.Run` (`internal/daemon/spawner.go`) sets the Claude Code
worker subprocess's `PATH` to a fixed three-entry string:

```go
workerPATH := filepath.Dir(claudePath) + ":/usr/bin:/usr/local/bin"
```

That restricted `PATH` is deliberate — the comment above it ties it to
defense against a compromised or adversarial worker reaching arbitrary
host binaries. But `DefaultMCPRegistry()` (`internal/daemon/templates.go`)
names three stdio MCP servers by bare command name — `ethos`, `beadle-email`,
`biff` — and `BuildMCPConfig` writes those bare names straight into the
per-spawn `mcp-config.json` it hands to `claude --mcp-config`. When the
worker process starts, Claude Code's MCP client resolves `"command":
"ethos"` against *the worker subprocess's own* `PATH` — the three-entry
string above — not against the daemon's `PATH`.

None of the three install locations the Makefile's provisioning targets
use are on that list:

| Binary | Provisioning target | Install location |
|---|---|---|
| `ethos` | `tools-ethos` | `go install`'s resolved `GOBIN` or first `GOPATH` entry's `bin/` (`$GOINSTALL_BIN`, Makefile L242-244) |
| `biff` | `tools-biff` | `uv tool dir --bin` (typically `~/.local/bin`, but not guaranteed) |
| `beadle-email` | `tools-beadle-email` | `$(CURDIR)/.tmp/bin` (per-checkout self-build) |

`handler.go`'s own `whitelistDirs` for the **CLI** runner already hits this
exact mismatch for a parallel case — it includes
`~/.local/bin` plus the running daemon binary's own directory, which
happens to catch `beadle-email` when the daemon is installed there, but
still misses a `go install`-provisioned `ethos` or a `uv`-provisioned
`biff` whose bin dir differs from `~/.local/bin`. The **Claude worker's**
`PATH` (`spawner.go`'s `workerPATH`) is a separate, even narrower list that
contains none of the three.

Result: every `claude` pipeline stage spawned via `ClaudeRunner.Run` starts
a worker that cannot reach its own MCP servers unless the three binaries
happen to already sit in `/usr/bin`, `/usr/local/bin`, or next to the
`claude` binary — true on no host provisioned per this repo's own
Makefile. `TestDefaultMCPRegistry_StdioCommandsAreReal` (`templates_test.go`)
catches the registry naming a nonexistent subcommand, by invoking the
command directly from the *test process's* `PATH` (widened by `make
test`'s `TEST_RUN` to include all three provisioning targets) — it does
not catch this bug, because it never goes through `WorkerSpawner.Run`'s
narrowed `PATH` at all. The gap is between two correct-in-isolation
pieces: the registry test proves the commands exist and work; the spawner
proves the subprocess environment is restricted. Nothing proves the
restricted environment can still reach what the registry names.

## How this leaves the system better

Today, whether an MCP server is reachable from inside a worker depends on
an accident of which bin directories exist at `/usr/bin`,
`/usr/local/bin`, and next to `claude` on the specific host — i.e., on
whatever the worker's environment happens to contain, inherited un-audited
from however the daemon process itself was started. That is not a trust
boundary at all; it is incidental behavior that happens to work on a
developer's machine (where `~/.local/bin` is often already first on a
*user's* shell `PATH`, though never on the worker subprocess's,
constructed fresh in `spawner.go` rather than inherited) and silently
breaks in a minimal deployment (a systemd unit with an explicit,
minimal `Environment=` block; the provisioning targets' install
locations were never a promise about runtime `PATH` contents, only
about where `make test` can find them).

The fix moves resolution of "which binary backs this MCP server" into the
daemon's own decision, made once, explicitly, at the moment the daemon
builds each worker's `mcp-config.json` — using the daemon's *own*
environment (which the operator controls directly, e.g. via the
provisioning unit's `PATH=`) to look up each registry entry's `Command`,
and writing the resolved absolute path into the config. The worker's `PATH`
stays exactly as restricted as it is today; it is simply no longer asked
to do this resolution at all. What a worker can execute is a *daemon-made
decision, frozen into an immutable per-spawn file before the worker
starts*, not an environment variable the worker's own process can act on.
That is a tighter trust boundary than today's: today's design depends on
nobody standing between the daemon-set `PATH` string and the worker's
ability to decide — for itself, via normal `PATH` search rules — what
`ethos` resolves to, if `PATH` is ever widened for any other reason.
Fixing it at the config-write step removes that dependency entirely:
widening the worker's `PATH` tomorrow, for an unrelated reason, no longer
has any bearing on which `ethos` binary a worker's MCP client invokes.

This is also a narrower change than it might first appear against the
zero-agent-authority invariant (`ARCHITECTURE.md`'s Design Invariants):
*which* MCP servers a stage grants (`cmd.MCPServers`, the registry names)
is signed command-file content already governed by that invariant —
nothing here changes what a command file can name. This change concerns
only *how* a name already authorized by a signed command file resolves to
an executable path, a mechanical step downstream of that authorization,
not a new grant of authority.

## Approaches compared

### (A) — Resolve at config-build time, write the absolute path

`BuildMCPConfig` (or a thin wrapper `ClaudeRunner.Run` calls before it)
resolves each **selected** entry's `Command` via `exec.LookPath` run in
the *daemon's* own environment (i.e., the daemon process's inherited
`PATH`, never the worker's), and writes the resolved absolute path as
`MCPServerConfig.Command` in the per-spawn JSON. The worker's `PATH`
(`spawner.go`'s `workerPATH`) is untouched.

- **What the worker can execute.** This fixes what Claude Code's MCP
  client execs for the servers `cmd.MCPServers` grants — exactly the
  fixed absolute paths the daemon resolved at spawn time, regardless of
  what the worker's own `PATH` contains, because the config no longer
  does a `PATH` search at MCP-connect time at all; Claude Code execs the
  literal path string. It is **not** an allowlist of what the worker
  *process* can execute, and this design claims no such sandbox:
  `WorkerSpawner`'s restricted `PATH` and `--tools` grant bound the
  worker's own built-in tool surface (`spawner.go`), not OS-level
  execution, so a worker holding `Bash` — or a worker compromised via
  prompt injection — can still invoke any binary it can reach by an
  absolute path or a directory already on its restricted `PATH`,
  independent of anything this change resolves. This approach narrows
  one specific resolution step (which binary a given MCP server name
  means) to a value the daemon fixed in advance; it narrows nothing
  about what the worker process itself is able to run.
- **What a compromised worker gains.** Nothing new. A compromised worker
  already inherits whatever `cmd.MCPServers` + the registry grant it for
  that stage; this approach does not expand or contract that grant, it
  only removes the one path by which an *accidental* `PATH` change
  (widening `/usr/bin` with a symlink, say) could have altered what those
  names resolve to. This approach does mean the **daemon's own** `PATH`
  becomes the sole arbiter of which binary each registry name resolves
  to — a writable directory placed ahead of the real binary on the
  daemon's `PATH` (classic `PATH` poisoning) would let the daemon itself
  resolve `ethos` to an attacker-controlled binary before a worker is
  ever spawned. That route is not new here: the daemon already resolves
  three other bare command names against its own inherited `PATH` with
  no additional check — `internal/daemon/mission.go:147`
  (`exec.Command("ethos", "mission", "create", ...)`), `internal/daemon/runner.go:95`
  (`exec.CommandContext(ctx, "ethos", "mission", "close", missionID)`),
  and `cmd/beadle-daemon/main.go:125`'s `gpgBinary = "gpg"`, which
  `internal/daemon/signature.go` then execs for the owner-key
  verification that gates every signed command file — the single check
  the zero-agent-authority invariant rests on. The daemon's own
  environment, `PATH` included, is already an operator-trusted input
  outside the worker-sandbox threat model this document is otherwise
  concerned with; approach (A) extends that existing assumption to MCP
  server resolution rather than introducing a new one.
- **What happens when the binary is absent at spawn time.** `exec.LookPath`
  fails in `BuildMCPConfig`, which already returns `(string, error)` for
  exactly this family of failure (`unknown MCP server`, `cfg.Validate()`
  failure) — this is one more branch of the same function, not a new
  failure surface. `ClaudeRunner.Run` already treats a `BuildMCPConfig`
  error as fatal to the stage (`runner.go:75-78`, `return "", fmt.Errorf("build
  mcp config: %w", err)`), which the caller (`Executor.Run`, `pipeline.go`)
  already turns into `p.Error` + `fireElse` — the pipeline is marked
  failed and the sender is replied to, with no `claude` process ever
  started. This is the existing failure path for `beadle-qtei`'s declared
  env vars (`runner.go`'s `resolveEnvVars` check, same function), reused
  verbatim for a declared MCP command.
- **What the daemon logs.** One `slog` line per resolution failure, naming
  the registry entry and the unresolved `Command` — the same shape
  `e.Logger.Info("stage mission created", ...)` already uses just above
  the call site, so no new logging convention is introduced.

### (B) — Extend the worker PATH with the provisioning targets' install dirs

Add `$GOINSTALL_BIN`-equivalent and `uv tool dir --bin`-equivalent
directories to `spawner.go`'s `workerPATH` construction, alongside
`/usr/bin` and `/usr/local/bin`.

- **What the worker can execute.** Any executable in those directories,
  not only `ethos`/`biff`/`beadle-email` — `go install` and `uv tool
  install` are general-purpose install mechanisms, and either bin dir can
  (now or later) contain other tools an operator or a prior `uv tool
  install` happened to place there. The grant is "this whole directory,"
  not "this one registry entry."
- **What a compromised worker gains.** The ability to invoke anything that
  directory holds, by name, via the worker's own built-in `Bash` tool (when
  granted) or any other mechanism that shells out — a strictly larger
  surface than approach (A), which never exposes a searchable `PATH`
  directory to the worker at all, only a config file naming fixed absolute
  paths for the servers Claude Code itself spawns.
- **What happens when the binary is absent at spawn time.** Nothing
  catches it before the worker starts. The worker starts normally
  (`--bare` mode has no pre-flight MCP connectivity check), Claude Code
  attempts to connect to `ethos serve` as its first MCP use, `PATH`
  search fails, and the worker either silently runs without that MCP
  server or fails deep inside its own turn loop — exactly the
  "pipeline record created, nothing downstream actually runs" failure
  mode `ARCHITECTURE.md`'s Design Invariants section already flags as a
  known gap for a different reason (missing GPG authorization on mission
  creation). This approach adds a second way to reach that same silent
  failure shape, for an entirely mechanical cause.
- **What the daemon logs.** Nothing, by construction — the daemon never
  attempts the resolution itself, so it has nothing to log. Any diagnostic
  trail exists only inside the worker's own transcript, if the worker's
  turn gets far enough to report the connection failure at all.

### (C) — A daemon-config allowlist of absolute paths per registry entry

Add a field (e.g. `~/.punt-labs/beadle/daemon.json`'s existing `Config`
struct, per `docs/wire-verifysignature.md`'s precedent) mapping each
registry entry name to an absolute path the operator sets explicitly,
e.g. `{"mcp_binaries": {"ethos": "/home/op/go/bin/ethos", ...}}`. The
daemon reads this at startup and uses it in place of resolution.

- **What the worker can execute.** The exact paths an operator wrote into
  config — in principle the narrowest of the three, since nothing is
  derived at runtime at all.
- **What a compromised worker gains.** Nothing beyond approach (A) — the
  worker still only ever sees the resolved absolute paths in its
  `mcp-config.json`.
- **What happens when the binary is absent at spawn time.** Depends on
  whether the daemon validates the configured path's existence at startup
  (not specified by this approach alone) versus only at spawn time (same
  shape as (A)). Either way, this approach adds a **new required
  onboarding step**: `docs/wire-verifysignature.md` already documents that
  there is no `beadle init`/`sign` onboarding flow yet (tracked as
  `beadle-7gm`), so an operator today configures nothing beyond `email.json`
  to get a running daemon. This approach would make every fresh deployment
  non-functional until the operator manually populates three more config
  paths it is their job to keep in sync with wherever `make tools-*`
  happened to install things — the exact kind of configuration drift (A)
  avoids, since (A) re-derives the answer from the daemon's own `PATH` on
  every spawn.
- **What the daemon logs.** Same as (A), once the binary-presence check
  exists — but the check is now validating operator-entered config,
  which is a second source of truth to keep current, not a live read of
  the daemon's own resolved environment.

### Recommendation

**Approach (A).** It is the only one of the three that *removes* a trust
surface rather than relocating or widening it: the worker's `PATH` search
capability for the registry's binaries is eliminated entirely, replaced by
a daemon-computed, written-once, immutable-per-spawn absolute path. (B)
loses because it grants the worker a directory, not a binary — a strictly
larger surface for the same problem, and one that degrades silently
instead of failing the stage. (C) loses on convenience and staleness, not
on the trust boundary itself — it is roughly A-equivalent in what the
worker can reach, but it reintroduces exactly the kind of manual,
drift-prone configuration step `ARCHITECTURE.md`'s Design Invariants
section already singles out as an open gap (`beadle-7gm`) for an unrelated
reason, and it would be the first daemon feature whose happy path requires
hand-editing `daemon.json` before the daemon is useful at all. (A) instead
extends the pattern `runner.go`'s `resolveEnvVars` already established for
declared env vars: a declared dependency is resolved against the daemon's
own environment, at the point of use, and a resolution failure fails the
stage before any subprocess starts.

## Failure behavior

When a selected registry entry's `Command` cannot be resolved via
`exec.LookPath` in the daemon's environment, `BuildMCPConfig` returns an
error before any temp file is written, naming the registry entry and the
unresolved `Command` string. `ClaudeRunner.Run` propagates that error
immediately — **no `claude` process is ever started** for that stage.
`Executor.Run` (`pipeline.go`) treats this exactly like every other
stage-runner error already handled at that call site: it sets `p.Error`
to the wrapped message, calls `e.fireElse(p)`, which marks the pipeline
record failed and sends the configured reply to the email's sender. This
mirrors `runner.go`'s existing `resolveEnvVars` check line for line: both
are preflight checks against a declared dependency, both fail the stage
before any subprocess work happens, and both produce a pipeline record
whose `Error` field names exactly what was missing — never a worker that
starts, consumes its turn budget, and fails opaquely on first MCP use.

## Implementation write set (for the follow-up implementation mission)

- `internal/daemon/templates.go` — add the resolution step to
  `BuildMCPConfig` (or a small helper it calls), using `exec.LookPath`
  against the *process* environment (the daemon's, never a value threaded
  in from the worker side) for each selected entry whose `Command != ""`;
  leave http-shaped entries (`context7`) untouched, since `Validate()`
  already distinguishes the two shapes and only a stdio entry has a
  `Command` to resolve.
- `internal/daemon/templates_test.go` — the new test described below;
  confirm whether `TestDefaultMCPRegistry_StdioCommandsAreReal` changes
  (see below — it does not need to, see reasoning).
- `internal/daemon/spawner.go` — update the comment above `workerPATH`
  (currently reads only as a security rationale for the restriction) to
  note that MCP server binaries no longer rely on this `PATH` at all,
  resolved instead at config-build time by `BuildMCPConfig` — so a future
  reader does not assume widening `workerPATH` is ever the right fix for
  an MCP server the worker cannot reach.
- `docs/ARCHITECTURE.md` — amend the "Design Invariants" section (or the
  Package Map's `internal/daemon` row) to state the new invariant: an MCP
  server's `Command` reaching a worker's `mcp-config.json` is always an
  absolute path resolved by the daemon at spawn time, never a bare name
  the worker's own `PATH` resolves.

### The proving test

A table or single-case test in `templates_test.go`:

1. Build a scratch directory (`t.TempDir()`), write an executable fake
   binary into it (e.g. a trivial shell script, `0o755`), and set `PATH`
   to that directory only for the duration of the test
   (`t.Setenv("PATH", dir)`).
2. Call `BuildMCPConfig` with a registry containing one stdio entry whose
   `Command` matches the fake binary's name.
3. Read back the written JSON and assert the `mcpServers.<name>.command`
   field is the fake binary's **absolute** path (`filepath.Join(dir,
   name)`), not the bare name — proving resolution happened and the
   result was written, not deferred to the subprocess.
4. Remove the fake binary (or point `PATH` at an empty directory) and call
   `BuildMCPConfig` again with the same registry; assert it now returns a
   non-nil error naming the registry entry and the unresolved command —
   proving the negative case fails closed, before any file is written for
   that call, matching `resolveEnvVars`'s precedent.

**Does `TestDefaultMCPRegistry_StdioCommandsAreReal` change?** No. That
test exercises `DefaultMCPRegistry()`'s *entries* directly against
`exec.LookPath` and `--help` in the *test process's* own `PATH` (widened by
`make test`'s `TEST_PREREQS`) — it is proving the registry's `Command`/`Args`
values name real, well-behaved binaries, a concern orthogonal to *where*
that resolution happens inside the spawn path. The new test above exercises
`BuildMCPConfig`'s resolution behavior specifically, with a synthetic
registry and a scratch `PATH` it controls — it does not touch
`DefaultMCPRegistry()` at all, so there is no reason for the existing
test's registry-correctness assertions to move or duplicate.

## Decisions for the operator

1. **Where resolution runs: approach (A), daemon-environment `PATH`
   lookup inside `BuildMCPConfig`, vs. (C), an explicit `daemon.json`
   allowlist.** Recommend (A) — it needs no new onboarding step, matches
   the `resolveEnvVars` precedent already in this codebase, and never goes
   stale relative to wherever `make tools-*` actually installed a binary.
   (C) is the fallback if the operator wants an explicit, auditable,
   hand-maintained record of exactly which binary backs each MCP server —
   a stronger supply-chain story at the cost of a new required config step
   on every fresh deployment.
2. **Whether `workerPATH`'s three-entry restriction should also drop
   `/usr/local/bin` and `/usr/bin`, now that MCP resolution no longer
   needs any directory on the worker's `PATH` at all.** Out of scope for
   this bead (those two entries exist for `git` and other basic tools a
   worker's `Bash` grant may need, unrelated to MCP resolution), but worth
   a follow-up bead if the goal is to shrink the worker's reachable
   binary set further. Recommend leaving it alone here and filing that
   separately, so this PR stays scoped to the one defect Qodo found.
3. **Whether the new `BuildMCPConfig` resolution failure should also
   appear as a structured field on the pipeline record** (today, errors
   land only in the free-text `p.Error` string, per the existing pattern
   for every other stage-runner failure). Recommend no change here —
   consistent with how `resolveEnvVars`'s failure and every other
   `ClaudeRunner.Run`/`CLIRunner.Run` error already surface, and a
   structured-error redesign for pipeline failures generally is a bigger,
   separate concern than this bead.

## Draft DESIGN.md ADR entry

```markdown
## DES-037: MCP server binaries resolved at spawn time, not via worker PATH

**Decision:** `BuildMCPConfig` (`internal/daemon/templates.go`) resolves
each selected stdio MCP server's `Command` with `exec.LookPath` run in the
daemon's own environment, and writes the resolved absolute path into the
per-spawn `mcp-config.json`. The worker subprocess's restricted `PATH`
(`spawner.go`'s `workerPATH`) is unchanged and plays no role in locating
MCP server binaries.

**Why:** `DefaultMCPRegistry()` names `ethos`, `beadle-email`, and `biff`
by bare command name, relying on the worker's own `PATH` to resolve them
at MCP-connect time. The worker's `PATH` is `<claude dir>:/usr/bin:/usr/local/bin`
(`spawner.go`) — none of which matches where this repo's own provisioning
targets (`tools-ethos`, `tools-biff`, `tools-beadle-email`) install those
binaries. Every `claude`-runner pipeline stage therefore started a worker
that could not reach its own MCP servers on any correctly provisioned
host (found by Qodo on PR #280; tracked as `beadle-tsku`).

**Alternatives considered:**

- Widen `workerPATH` with the provisioning targets' install directories.
  Rejected: grants the worker an entire searchable directory rather than
  one resolved binary path, is a strictly larger reachable-execution
  surface for a compromised worker, and degrades silently (the worker
  starts, then fails deep inside its own turn loop on first MCP use)
  rather than failing the pipeline stage before any subprocess starts.
- An explicit `daemon.json` allowlist mapping registry entries to absolute
  paths. Rejected for now: adds a new required, hand-maintained onboarding
  step with no onboarding flow to carry it (`beadle-7gm` tracks the
  general gap), and goes stale independently of wherever `make tools-*`
  actually installed a binary — a maintenance cost (A) avoids by
  re-resolving from the daemon's live environment on every spawn.

**Consequences:** An MCP server binary absent from the daemon's own `PATH`
at spawn time now fails the pipeline stage immediately, with the registry
entry and command named in the pipeline's error and the sender's reply —
before any worker process starts, matching the existing preflight-failure
pattern `resolveEnvVars` established for declared env vars
(`internal/daemon/runner.go`). Which binary a given MCP server name resolves
to is now daemon-computed and written once per spawn, rather than resolved
at runtime from inside the worker process. This is **not** a sandbox and
claims no broader execution boundary: it fixes what Claude Code's MCP
client execs for the servers a stage is granted, not what the worker
process itself can run. `WorkerSpawner`'s restricted `PATH` and `--tools`
grant are the (pre-existing, unchanged) controls on the worker's own
execution surface; a worker holding `Bash`, or one compromised via prompt
injection, can still invoke any binary it can reach by an absolute path or
a directory already on its restricted `PATH`, independent of this change.
```
