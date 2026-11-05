# Design: handing the triggering email's payload to a worker outside the tracked contract

Design for bead `beadle-4gm1` (P1, found reviewing ethos PR #539 / DES-080).

## What this change alters

`buildStageContract` (`internal/daemon/pipeline.go:232-261`) writes the
triggering email's body, the current stage's call args, and the prior
stage's pipeline output into the mission contract's top-level `context`
field, via `stageContext` (`pipeline.go:272-299`). The same function also
writes `meta.From`, `meta.MessageID`, and `meta.Subject` into
`inputs.trigger`. `createMissionFromContract` (`mission.go:122-153`) then
hands that YAML to `ethos mission create --file`, which stores it as
`<repoRoot>/.punt-labs/ethos/missions/<id>/contract.yaml` — a file this
repo's own `.gitignore` keeps untracked today
(`.punt-labs/ethos/missions/`), but whose on-disk shape is the one
`ethos mission show <id>` prints, and the one a future export
(`ethos mission export`, already used for `missions-closed/`) commits.

On ethos v4.16.0 (this repo's pinned `ETHOS_VERSION`), `ethos mission show`
and `ethos mission create` round-trip that YAML byte-for-byte: a worker
reading its own contract sees exactly what `buildStageContract` wrote,
including the real email body, the real sender address, and any stage arg
the planner derived from the email.

PR #539 on `punt-labs/ethos` (DES-080, not yet merged) changes that.
Every mission-record writer — `Store.writeContract`
(`internal/mission/store.go:2366-2391`), results, reflections, the event
log, delegation records, `missions.jsonl`, export, and migrate — now
routes through `RedactedYAML`/`RedactRecord`
(`internal/mission/redact.go`), which walks the struct via reflection
(`internal/redact/redact.go`'s `scrub`) and rewrites every string field
not tagged `redact:"keep"` through `redact.Prose`: a credential pass
(`SecretsString`, matching token shapes, `NAME=value`/`name: value`/
`--flag value` pairs whose name looks credential-bearing, PEM blocks,
bearer and basic auth headers, URL userinfo), an email-address pass
(`EmailPattern` → `[redacted-email]`), and a home-path pass. `Contract`'s
`Context` field and `Inputs.Trigger`'s `From`/`MessageID`/`Subject`
fields carry no `redact` tag at all (`internal/mission/mission.go:109`,
`:229-234`), so they are not exempt — they fall into the default case in
`scrub`'s struct branch, which applies the caller's content function,
`PathRedactor.Content` (`r.Content` → `redact.Prose(r.Text(s))`), to any
untagged field. Concretely, once a worktree is pinned past #539:

- `inputs.trigger.from` is always an email address, so it is always
  rewritten to `[redacted-email]`.
- `context`'s embedded `email body: <text>` line is credential- and
  address-swept: any token-shaped secret in the body becomes
  `[redacted-secret]`, any address becomes `[redacted-email]`, and a
  home-looking path becomes `~`. The body is not removed, but it is no
  longer the byte-for-byte content a stage's prompt (`cmd.Prompt`,
  `success_criteria`) was written assuming the worker would see.
- The same sweep hits `stage args: <k>=<v>, ...` for any arg value the
  planner derived from the email (an extracted address, a pasted secret),
  and `pipeline output: <...>` for any prior stage's result that echoed
  email content forward.

There is no exemption to ask ethos for. `context` and `inputs.trigger`
are free text and provenance metadata by design (the package comment on
`internal/redact` states the policy plainly: "Ethos writes mission
records ... into git-tracked trees ... every writer ... uses this
package"), and the tracked contract genuinely is adversarial-input-bearing
once an external sender controls part of it. Asking ethos to special-case
one field defeats the invariant PR #539 exists to establish —
`TestTrackedWritersAreRedacted` structurally enumerates every writer and
fails closed when one is missed, which is the opposite of carving out an
exception. The fix is on beadle's side: stop putting the payload
somewhere ethos redacts, because ethos redacting it is correct, not a bug
to route around.

Beadle's own Makefile pins `ETHOS_VERSION := v4.16.0`
(`Makefile:11`), which predates #539, so `buildStageContract`'s current
behavior does not regress *today*. It regresses silently the day someone
bumps that pin, because nothing in beadle's own test suite would catch
it: `TestBuildStageContract` and its siblings
(`pipeline_test.go:836-1078`) assert against the Go string
`buildStageContract` returns and against what the real `ethos mission
create` CLI accepts, never against what a *later* `ethos mission show`
returns — the redaction a mission-record *reader* sees is invisible to a
test that only exercises the *writer*. This design fixes the hand-off
so it holds on both the current pin and a post-#539 pin, per this
mission's contract, without bumping the pin in this unit.

## How this leaves the system better

Today, whether the worker's prompt matches what the planner and the
operator intended depends on an ethos version the daemon does not
control and has no visibility into. A pin bump in an unrelated PR — or a
future ethos default that always redacts tracked writes — silently
turns every pipeline stage's instructions into a prompt built from
partially-scrambled email content, with no error, no log line, and no
test failure on beadle's side: the contract still validates, the mission
still runs, the worker still gets *a* body, just not the right one. That
is a correctness bug waiting on someone else's unrelated release.

The fix removes the coupling entirely: the tracked contract stops being
the channel that carries the payload, and becomes what it already is
for `inputs.files` and `inputs.references` — a pointer to where the real
input lives, not the input itself. The real payload travels through a
channel ethos's mission-record writers never touch: the per-spawn system
prompt file `BuildSystemPromptForTools` already writes under
`MissionTemplate.TmpDir` (`templates.go:216-261`), with `0o600`
permissions, removed by `ClaudeRunner.Run`'s deferred `os.Remove`
(`runner.go:90`) once the worker exits. That file was never part of the
mission record ethos persists — it is a `claude --append-system-prompt-file`
argument, read directly into the worker's system prompt before the
worker's first turn, and gone before `ethos mission show` could ever be
asked about it. Routing the payload there means:

- The pipeline's actual behavior (what text the worker sees) stops
  depending on which ethos version happens to be installed. It is
  identical on v4.16.0 today and on a post-#539 pin, because neither
  version's redaction code path is ever reached by this file.
- The tracked contract becomes safe to export, to `ethos mission show`,
  and eventually to commit under `missions-closed/` without separately
  auditing whether this particular contract happened to embed adversarial
  content — exactly the property #539 is trying to establish for every
  *other* mission writer, extended here to beadle's own stage contracts
  instead of being the one caller that defeats it by construction.
- The worker still gets the full triggering content it needs to act on
  (beadle-ivtd's original goal — the worker must not be left to fetch the
  email itself, which required a broader mailbox-read grant for a
  single-message task) — nothing about *what the worker can read* gets
  narrower. Only *where that content physically lives* changes.

## Approaches compared

The payload is three things today: the triggering email's body
(`p.Body`, already capped to `maxContractFieldRunes` = 500 runes at
pipeline-creation time, `pipeline.go:66`), the current stage's call args
(`call.Args`, each value capped the same way in `stageContext`), and the
prior stage's pipeline output (`pipe`, left uncapped — up to the CLI
runners' own 1 MB `cappedWriter` ceiling). All three move together; none
of the three approaches below treats them differently.

### (A) — The per-spawn system prompt file

`BuildSystemPromptForTools` (`templates.go:216`) already writes a
temporary, untracked, `0o600` file per spawn, and `WorkerSpawner.Run`
already loads it via `--append-system-prompt-file`
(`spawner.go:97`) before the worker's first turn — independent of what
`--tools` grants. Extend it to also render the stage payload (args, prior
pipe, capped body) into that same file, and have `buildStageContract`
write a stable reference in `context` instead of the payload itself.

- **What the contract records.** No email address, no body text, no
  stage-arg value derived from the email — `context` becomes a fixed,
  daemon-generated reference string (e.g. "stage input delivered via
  system prompt; see pipeline `<id>` stage `<n>`"), which contains
  nothing ethos's redaction passes have anything to act on.
- **Who can read the payload, under what permissions.** Whoever can read
  the daemon's own temp directory (`MissionTemplate.TmpDir`, same
  filesystem permissions — `0o600`, owned by the daemon's user — as the
  mcp-config file sitting right next to it) and the worker process
  itself, which receives the content directly in its system prompt with
  no tool call required. No new grant, no new reader: this is the same
  access boundary `BuildMCPConfig`'s temp file already has.
- **No Bash, no Read case.** Unaffected. `--append-system-prompt-file` is
  consumed by the `claude` process before any tool is invoked — it is
  how the system prompt is assembled, not something the worker fetches.
  A recipe with `Tools: []` (no built-in tools at all) still receives the
  full payload; there is no failure mode to design for here, which is
  the property the other two approaches lack.
- **What is left on disk, who removes it.** Nothing new. The system
  prompt file already exists in production; `ClaudeRunner.Run`'s
  existing `defer func() { _ = os.Remove(promptPath) }()`
  (`runner.go:90`) already removes it once the worker exits, payload or
  not.
- **Does the reply still reach the sender.** Yes, unconditionally, and
  this was already true before this change: `Executor.Run`'s auto-reply
  (`pipeline.go:184-211`) and `fireElse`'s failure reply
  (`pipeline.go:400-428`) both read the reply address from
  `p.Email.From` — the `Pipeline` struct's own field, persisted
  separately by `PipelineStore.Save` (`persist.go`) — never from the
  mission contract's `inputs.trigger.from`. Moving the payload, or
  letting `inputs.trigger.from` get redacted by ethos, changes nothing
  about who the daemon replies to, because that code path never reads
  the contract at all.

### (B) — A per-stage payload file under the daemon's own store

Write the payload to a new file under `PipelineStore.Dir` (or a sibling
directory under `paths.DataDir()`), named by pipeline ID and stage index,
and reference it from `context` by that ID. The worker reads it with its
own granted `Read` tool.

- **What the contract records.** Same as (A): a reference, no payload.
- **Who can read it.** Anyone who can read `PipelineStore.Dir` on disk —
  unchanged from today, since pipeline records already live there with
  the full (capped) body in `Pipeline.Body` (`persist.go`'s `0o700`
  directory, `0o600` files) — *plus* the worker, conditionally: only if
  the stage's recipe grants `Read` and the file's path is one the
  worker's write-set/precondition admission allows it to open.
- **No Bash, no Read case.** This is where (B) loses outright. A recipe
  with `Tools: []` — the same shape `TestOnNewMail_EmptyMCPServersPassesNone`
  and `TestOnNewMail_EmptyToolsSystemPromptNoBashdependentResult`
  (referenced in `harness_test.go`'s `e2eFixture` comment) already
  exercise for the *result-submission* instruction — has no tool capable
  of opening the file at all. The worker would need to either be told to
  use a tool it was never granted (silently doing nothing, the exact
  failure shape `docs/worker-mcp-path.md` already calls out for a
  different cause: "pipeline record created, nothing downstream actually
  runs"), or the pipeline would need a new preflight check that fails
  the stage before spawning when `Read` is absent — new logic (B)
  specifically requires and (A) does not.
- **What is left on disk, who removes it.** A new per-stage file that
  nothing currently reaps. `PipelineStore.Prune` (`persist.go:164-204`)
  only knows about `<id>.json` pipeline records, not a sibling payload
  file; this approach adds a second artifact type retention policy would
  need to cover, or it leaks indefinitely.
- **Reply to sender.** Unaffected, same as (A) — `p.Email.From`, not the
  contract.

### (C) — A daemon-side MCP tool or API, called by reference

Expose the payload through a new MCP server (or a method on an existing
one) that the worker calls, passing the pipeline ID and stage index from
the contract's reference.

- **What the contract records.** Same reference-only shape as (A)/(B).
- **Who can read it.** Whatever process can reach the new MCP server —
  broader than (A) or (B) by construction, since an MCP server is a
  long-lived endpoint a worker's *other* MCP clients could in principle
  also query, not a single file scoped to one spawn.
- **No Bash, no Read case.** No better than (B): MCP tool access is
  itself a grant (`cmd.MCPServers`, `templates.go`'s
  `BuildMCPConfig`) a recipe can omit, and omitting it leaves the worker
  with no way to call the new tool, same failure shape as (B)'s missing
  `Read`.
- **What is left on disk, who removes it.** Nothing new on disk, but a
  new long-lived server process and a new stateful API surface
  (`internal/mcp` or a new daemon-side server) that has to track which
  pipeline/stage pairs are still live, expire stale entries, and be
  reasoned about as its own trust boundary — the kind of surface
  `docs/worker-mcp-path.md` (DES-037) was written specifically to narrow,
  not expand.
- **Reply to sender.** Unaffected, same as (A)/(B).
- **Implementation cost.** Strictly larger than (A): a new server, new
  registration in `DefaultMCPRegistry`, a new tool schema, and a new
  per-stage addressing scheme — all to reproduce exactly what the
  existing system-prompt file already does for free.

### Recommendation

**Approach (A).** It is the only one of the three that needs no new
failure path for the no-Bash, no-Read recipe case, because the channel
it uses — the system prompt, loaded before any tool call — was never
gated on a tool grant in the first place. (B) and (C) both lose on that
case specifically: either requires a tool grant the contract may
legitimately omit, and neither can deliver the payload without one. (A)
also needs no new artifact type (no new file for `Prune` to learn about,
no new server to run), reuses a file and a cleanup path that already
exist in production, and changes nothing about the daemon's security
posture — the temp directory's permissions and lifetime are identical to
what `BuildMCPConfig`'s sibling file already has today.

## The exact contract shape after this change

`inputs.trigger` keeps its four fields, but only `type` and `message_id`
are guaranteed to survive a redaction pass unchanged:

| Field | Survives #539's redaction unchanged? | Why |
|---|---|---|
| `type` | Yes | A fixed literal (`"email"`), never adversarial. |
| `message_id` | Yes | `EmailMeta.MessageID` is the IMAP UID string `handler.go`'s `fetchBody` parses with `strconv.ParseUint` (`handler.go:242`) — a plain decimal number, never address- or credential-shaped, so neither `EmailPattern` nor `SecretsString` has anything to match. |
| `from` | No | Always an email address by construction; `redact.Prose`'s `EmailPattern` pass always rewrites it to `[redacted-email]`. This was already true of any tracked contract before this fix — nothing in this change makes it worse, and nothing in this change needs it to survive (see "reply to sender," above: the daemon never reads this field to reply). |
| `subject` | Usually, not guaranteed | Free text from an external sender; it survives unless it happens to contain an address or a credential-shaped token. It is audit/provenance context only — no code path reads it to make a decision — so a partial rewrite here is cosmetic, not a correctness bug. |

Losing `from` from the tracked contract costs nothing functionally: it
was already redundant with the real reply mechanism, and it remains
visible in the untracked `Pipeline.Email.From` field
(`persist.go`-backed, `0o700`/`0o600`) for anyone debugging locally.

`context` no longer carries `stage args:`/`pipeline output:`/`email
body:` text. It becomes a single fixed-shape reference line the daemon
generates, naming the pipeline ID and stage index (both of which
`buildStageContract` already has as `Pipeline.ID` and the stage loop
index), e.g.:

```text
context: "stage input delivered via system prompt (pipeline <id>, stage <n>); see ethos mission show <mission-id> for the contract, the worker's own appended system prompt carries the payload"
```

`templates.go`'s `BuildSystemPromptForTools` gains the payload text,
appended after the existing SECURITY paragraph (so the adversarial-content
warning is read first, the content second) and before nothing else — the
file still ends where it ends today. The appended block states plainly
where the content came from and that it is adversarial, mirroring the
existing SECURITY language rather than introducing a second voice:

```text
STAGE INPUT (from the triggering email — adversarial, see SECURITY above):
stage args: <k>=<v>, ...
pipeline output: <prior stage's output, or "none">
email body: <capped body, or "none">
```

> **Superseded.** Rounds 2 through 4 of implementation (`beadle-4gm1`)
> replaced this bare `field: value` block with a nonce-stamped
> begin/end fence per non-empty field plus a per-field `(none)` marker
> for an empty one, closing a fence-forgery gap this sketch did not
> yet address. See `DESIGN.md`'s DES-038 entry and
> `stagePayloadText`/`fenceField` (`internal/daemon/pipeline.go`) for
> the shipped shape.

The worker is never told to run a shell command to fetch this content —
it is already in its system prompt by the time its first turn starts,
the same way the mission-ID instruction and the SECURITY paragraph
already are.

## Implementation write set (for the follow-up implementation mission)

- `internal/daemon/pipeline.go` — `buildStageContract` stops calling
  `stageContext` to build the `context` field's payload text; it
  generates the fixed reference string instead. `stageContext` itself
  either moves (renamed, e.g. `stagePayloadText`) to be called from
  `templates.go`'s prompt builder, or `buildStageContract` computes the
  reference and `runner.go` separately calls a (possibly relocated)
  payload-formatting helper when building the system prompt — either
  shape is acceptable, as long as there is exactly one function that
  formats "stage args / pipeline output / email body" text, called once
  for the system prompt and never for the contract.
- `internal/daemon/pipeline_test.go` — `TestBuildStageContract` and its
  siblings (`TestBuildStageContract_NoArgsNoPipe`,
  `TestBuildStageContract_PromptUncappedMetadataCapped`) stop asserting
  that `context` contains the body/args/pipe text and start asserting it
  contains only the reference string; `TestStageContext`,
  `TestStageContext_LongArgValueCapped`, `TestStageContext_LongBodyCapped`
  move or get renamed to match wherever the payload-formatting function
  ends up.
- `internal/daemon/runner.go` — `ClaudeRunner.Run` passes `call.Args`,
  `pipe`, and `p.Body` (already available at the call site, line 75's
  `buildStageContract(p.Email, cmd, call, pipe, p.Body)` call) into the
  system-prompt builder call, alongside `missionID` and `cmd.Tools`.
- `internal/daemon/runner_test.go` — covers the new argument threading;
  existing runner tests that construct a system prompt indirectly via
  `ClaudeRunner.Run` gain an assertion that the spawned prompt file
  (via `daemontest.FakeSpawnerCall.SystemPromptContent`, where used) does
  not depend on `context`'s shape.
- `internal/daemon/templates.go` — `BuildSystemPromptForTools` gains a
  parameter (a small struct is cleaner than three more positional
  strings plus an int) carrying the stage args, pipe, and body; it
  appends the "STAGE INPUT" block described above after the SECURITY
  paragraph when the payload is non-empty, and appends nothing when all
  three are empty (preserves today's shorter prompt for the `handler.go`
  fallback path, `BuildContract`, which has no stage-level payload at
  all).
  > **Superseded.** The shipped `BuildSystemPromptForTools`
  > (`internal/daemon/templates.go`) always renders the STAGE INPUT
  > block, with an explicit `(none)` marker per empty field, rather
  > than omitting the whole block when all three happen to be empty —
  > see `StagePayload`'s doc comment for why a per-field marker beat an
  > all-or-nothing block.
  `BuildSystemPrompt` (the `tools == nil` convenience wrapper) is
  updated to pass an empty payload, so its existing callers see no
  output change.
- `internal/daemon/templates_test.go` — `TestBuildSystemPrompt` keeps
  asserting today's no-payload shape; new tests cover the payload block
  (below).
- `docs/ARCHITECTURE.md` — add an invariant paragraph (Design Invariants
  section) stating that a pipeline stage's triggering-email content, args,
  and prior output never reach a tracked mission-record field; they are
  delivered only through the per-spawn, untracked system prompt file.

### The proving tests

1. **The contract carries nothing.** A test (in `pipeline_test.go`, next
   to `TestBuildStageContract`) that builds a contract from a multi-line
   body longer than `maxContractFieldRunes`, containing an email address
   and a token-shaped secret, and asserts the returned string contains
   neither `meta.From` nor any substring of the body longer than one
   line (so a coincidental short overlap, e.g. a shared word, does not
   false-fail the assertion while a real leak of the body's content
   still would).
2. **The system prompt carries everything.** A test (in
   `templates_test.go`) that calls the updated
   `BuildSystemPromptForTools` with a populated payload (args, pipe,
   body) and asserts the written file contains the full capped body, the
   stage args, and the prior pipeline output verbatim.
3. **No tools, payload still delivered.** A test that calls the same
   function with `tools = nil` (or `[]string{}`) and the same payload,
   and asserts the file still contains the full payload — proving
   approach (A)'s central claim directly, independent of `--tools`.
   (Approach A needs no "fails the spawn" branch, so there is no
   negative case to write here; a reviewer confirming the design chose
   (A) should expect this test to be the only one of its kind.)
4. **`TestOnNewMail_EndToEnd` (`harness_test.go`).** No required change.
   It asserts `record.Email.From` against the untracked `Pipeline`
   struct's JSON, not against the ethos contract text, and that
   assertion is untouched by this design. It may optionally gain an
   assertion that `spawner.Calls()[0].SystemPromptContent` contains the
   fixture's message body, as an end-to-end confirmation that the new
   hand-off actually reaches a real spawn through the real pipeline —
   worth adding, not required for this design to be correct, since (2)
   and (3) above already prove the unit-level behavior directly.
5. **`TestBuildStageContract_RealEthosCLIRejectsMalformedContract`
   (`pipeline_test.go`).** No change needed. It deletes
   `evaluator.handle` from the generated contract and asserts the real
   `ethos mission create` CLI rejects it — a check that holds regardless
   of what `context` contains, before or after this design, since the
   rejection is driven by the missing required field, not by anything
   in `context`.

## Decisions for the operator

None. The design is forced by the one hard constraint the mission's own
context already states — the worker must receive its full stage input
even when a recipe grants it no built-in tools at all — and only
approach (A) satisfies that constraint without adding a new failure
branch or a new stateful surface. There is no trust-model trade-off to
rule on: the worker's read access to the payload is unchanged (it could
always see the full body before this fix), only the on-disk location and
the lifetime of the channel carrying it change, both strictly narrower
(untracked, `0o600`, single-spawn-lifetime) than "committed to a
redaction-dependent tracked file."

## Draft DESIGN.md ADR entry

```markdown
## DES-038: deliver pipeline stage input via the per-spawn system prompt, not the tracked mission contract

**Decision:** `buildStageContract` (`internal/daemon/pipeline.go`) no
longer embeds the triggering email's body, the current stage's call
args, or the prior stage's pipeline output in the mission contract's
`context` field. Instead it writes a fixed, daemon-generated reference
(pipeline ID and stage index). The actual payload is appended to the
per-spawn system prompt file `BuildSystemPromptForTools`
(`internal/daemon/templates.go`) already writes under
`MissionTemplate.TmpDir` — untracked, `0o600`, removed by
`ClaudeRunner.Run`'s deferred cleanup once the worker exits — and
reaches the worker via `claude --append-system-prompt-file`
(`internal/daemon/spawner.go`) before its first turn, independent of
what built-in tools the stage's recipe grants.

**Why:** ethos PR #539 (DES-080) redacts every mission-record writer,
including the untagged `Contract.Context` and `Inputs.Trigger` fields a
stage contract used to carry the real email content in. On a pin that
includes it, a worker reading its own contract via `ethos mission show`
would see `[redacted-email]` and a partially-scrubbed `context` instead
of the real triggering content — a correctness regression with no error,
log line, or beadle-side test failure to catch it, since beadle's
existing tests exercise only `ethos mission create`'s acceptance of the
generated contract, never what a later `ethos mission show` returns.
ethos will not exempt these fields: they are free text and provenance
metadata carrying adversarial input by design, and `#539`'s structural
test (`TestTrackedWritersAreRedacted`) exists specifically to catch an
unredacted writer, not to be carved around.

**Alternatives considered:**

- A per-stage payload file under the daemon's own `PipelineStore`
  directory, referenced by pipeline ID and read via the worker's granted
  `Read` tool. Rejected: a recipe with no built-in tools (`Tools: []`,
  already a supported shape — see `BuildSystemPromptForTools`'s own
  Bash-gated result-submission instruction) has no way to open the file,
  and the file itself is a new artifact `PipelineStore.Prune` does not
  know to reap.
- A daemon-side MCP tool exposing the payload by pipeline/stage ID.
  Rejected for the same no-tools failure mode as the file approach, plus
  a strictly larger implementation cost (a new server, registry entry,
  and stateful addressing scheme) to reproduce what the existing system
  prompt file already does.

**Consequences:** The tracked mission contract's `inputs.trigger.from`
is no longer guaranteed to survive a redaction pass (it already wasn't,
functionally — the daemon's auto-reply and failure-reply paths read
`Pipeline.Email.From`, never the contract, so this was always cosmetic).
`message_id` and `type` remain stable since neither is address- or
credential-shaped. The worker's access to the real stage input is
unchanged; only the channel carrying it moves from a file ethos commits
and redacts to one the daemon owns for the lifetime of a single spawn.
```
