# Delegation with Missions and Pipelines

All code delegation uses ethos missions. Missions are typed contracts between a leader (claude) and a worker (bwk, rsc, mdm, djb, adb, kwb, jbh, jms, jra) that enforce write-set admission, frozen evaluators, bounded rounds, and append-only event logs. The roster is the `beadle` team in `.punt-labs/ethos/teams/beadle.yaml`, resolved repo-only — a handle not on it does not exist here. It is sized to what this repo dispatches: Go, Go toolchain, security, infrastructure, CLI, test, QA, and formal methods (Z/B). Python, UX, and product seats return by adding them to the team and re-running `ethos vendor --team beadle --apply --prune`.

Two seats never write production code: `kwb` (test-engineer, write set is test files only) and `jbh` (qa-engineer, no Write/Edit at all). They exist so the top of the testing pyramid — adversarial tests, integration tiers, end-to-end journeys through the real entry point — has an owner who is not the person whose code is under test.

## Pipeline Selection

Every non-trivial task uses an ethos pipeline. Select the pipeline based on the nature of the work:

| Pipeline | When to use |
|----------|------------|
| `quick` | Well-understood changes: bug fixes, single-file tasks, mechanical updates |
| `docs` | Documentation-only: ADRs, README, CHANGELOG, architecture docs |
| `coverage` | Test gap identified: measure, write tests, verify delta |
| `standard` | Features with clear goals: 4+ files, needs design-implement-test-review-document |
| `product` | New feature with product uncertainty: Working Backwards validation first |
| `formal` | Complex stateful systems: Z-spec modeling before implementation |
| `coe` | Recurring bugs, data corruption, incidents: structured root cause analysis |
| `full` | Epics, cross-cutting work: product validation + formal spec + implementation + retro |

Selection rules (evaluate in order):

1. Context mentions PR/FAQ, working backwards, product validation → `product`
2. Context mentions Z-spec, formal spec, model check, state machine → `formal`
3. Context mentions cause of error, recurring bug, postmortem → `coe`
4. Write-set is all markdown or `docs/` → `docs`
5. Context mentions test gap → `coverage`
6. 11+ files or multi-repo context → `full`
7. 4+ files or 3+ success criteria → `standard`
8. Otherwise → `quick`

Escalation only goes up. Never demote mid-flight.

## Instantiation

```bash
# Preview before creating
ethos mission pipeline instantiate standard --dry-run \
  --leader claude --worker bwk --evaluator mdm \
  --var feature=pipeline-v2 \
  --var target=internal/daemon/

# Create — produces one mission per stage with depends_on wiring
ethos mission pipeline instantiate standard \
  --leader claude --worker bwk --evaluator mdm \
  --var feature=pipeline-v2 \
  --var target=internal/daemon/
```

This creates 5 missions (design → implement → test → review → document) with `inputs_from` dependency edges. The leader is the quality gate between stages.

**`--worker`/`--evaluator` are global: ethos stamps the same pair onto every stage of the pipeline — there is no per-stage override flag.** So the command above does *not* route the test stage to a different handle on its own. The leader enforces the seat split at spawn time, not through instantiation: when the `test` stage unblocks, the leader spawns `kwb` (test-engineer) for it — not the `--worker` implementer — and when `review` unblocks, spawns the implementer's pair evaluator. The implementer is never the test author of record for its own code; the spawn is what makes that true, not the instantiate command. Where the split must be enforced in the contract itself, dispatch the test stage as its own single mission (`ethos mission dispatch --worker kwb --evaluator <implementer>`) instead of relying on the pipeline's global worker. After `review`, before the PR opens, the leader dispatches `jbh` (qa-engineer) for the verification gate against the installed artifact (`docs/WORKFLOW.md` § C. DEMO).

## Execution Loop

For each stage in the pipeline:

1. **Spawn or continue** (see "Reuse vs respawn" below): for a stage's first pass, `Agent(subagent_type=<worker>, run_in_background=true)` with a prompt pointing at the mission ID. For a re-examination of work the same agent just did — a reviewer looking at a fix to findings it raised, a worker reworking its own round — `SendMessage` to that agent instead, so it keeps the context it already built.
2. **Wait**: notification arrives when agent completes.
3. **Review**: read the diff and the result artifact.
4. **Reflect**: `ethos mission reflect <id> --file <path>`. Pass or findings.
5. **Advance or close**: if findings → `ethos mission advance <id>`; `SendMessage` the same worker to fix (not a fresh spawn) when the fix builds on its own round. If clean → mission closes, next stage unblocks.
6. **Commit**: after each clean stage. Tests must pass at every commit.

### Reuse vs respawn

A fresh `Agent()` reads everything from scratch — the diff, what the bug was,
why the last attempt failed. When the next task *builds on context the agent
already holds*, that re-read is wasted tokens and a worse result: a reviewer
spawned fresh for round 2 cannot tell "they fixed finding #2 but #3 is still
open," because it never held the findings. The harness supports continuing an
agent: `SendMessage` to its ID or name resumes it with context intact; a new
`Agent()` call starts cold.

The discriminator is **whether the next task builds on context the agent
already has:**

- **Spawn fresh** (`Agent()`): a stage's first pass, or genuinely new,
  unrelated work. There is nothing to carry, and stale context would be noise.
- **Continue** (`SendMessage` to the same agent): the *same work re-examined* —
  a code-reviewer verifying a fix to findings it raised, a test-engineer
  looking at the next round of the same code, a QA agent re-running a journey
  after a fix, a worker reworking its own prior round. It still holds the code
  model, the findings list, and the reasoning, so it only looks at what
  changed.

Two bounds: reuse is **within** a round-trip on one piece of work, not "keep
one agent alive forever" — an agent carried across different files or different
missions accumulates irrelevant context and degrades. And the mission contract
still frames the work (dispatch is two operations); `SendMessage` reuse is an
optimization on the follow-up, not a replacement for the contract.

For security-critical stages, spawn djb (security engineer — cryptographic implementation, threat-modeling, and policy) as a separate background review agent before reflecting.

## When NOT to Use Pipelines

- Exploratory research — no write-set, no success criteria
- Work you do directly (docs, ADRs, CLAUDE.md, memory files)
- Copilot/Bugbot fix rounds — mechanical, tight scope, 1 round. Use bare `Agent()` calls with the specific fix instructions.

## Single-Mission Dispatch

For work that genuinely doesn't fit a pipeline (rare):

```bash
ethos mission dispatch \
  --worker bwk --evaluator mdm \
  --write-set "internal/email/config.go,internal/email/config_test.go" \
  --criteria "1m is a valid poll interval,make check passes" \
  --context "Add 1m to validPollIntervals map" \
  --ticket beadle-xyz --budget 1
```

## Contract Schema (Required Fields)

```yaml
leader: claude
worker: bwk                    # bwk|rsc|mdm|djb|adb|kwb|jbh|jms|jra
evaluator:
  handle: mdm                  # must differ from worker, no shared role
inputs:
  bead: beadle-xyz             # optional bead link
write_set:                     # repo-relative paths, at least one
  - internal/email/smtp.go
  - internal/email/smtp_test.go
success_criteria:              # at least one verifiable criterion
  - implicit TLS connects to smtp.fastmail.com:465
  - make check passes
budget:
  rounds: 2                    # 1-10
  reflection_after_each: true  # leader reflects after each round
```

## Worker Prompt Template

```text
Mission <id> is yours. Read it first: `ethos mission show <id>`.
The contract names the write set, success criteria, and budget.
Only write to files listed in the write set. After your work for
this round, submit a result artifact:
`ethos mission result <id> --file <path>`. See
`ethos mission result --help` for the YAML shape. Do not commit,
push, or merge — return results to me.
```

Write-set admission is advisory — the leader verifies compliance during review, not the runtime.

## Evaluator Defaults

Two specialists per domain. Within each row, the worker and evaluator must be distinct handles.

| Task type | Worker | Evaluator |
|-----------|--------|-----------|
| Go internals / library design | `bwk` (Kernighan) | `rsc` (Cox) — toolchain, supply-chain |
| Go module / dependency / vuln | `rsc` | `bwk` |
| CLI / command design, help text, 80-column table output | `mdm` (McIlroy) | `bwk` — the implementer whose code renders it |
| Crypto / PGP implementation | `bwk` | `djb` (Bernstein) — security |
| Security / threat-modeling / policy (findings only) | `djb` | `bwk` |
| Infrastructure / CI / Makefile provisioning / install.sh | `adb` (Lovelace) | `djb` — provisioning is a trust boundary; `rsc` for a pure toolchain-pin change |
| Test stage of any pipeline; regression test for a bug; coverage gap | `kwb` (Beck) — `*_test.go` only | the implementing specialist (`bwk`, `mdm`, `adb`) — it is their code the tests indict |
| Test infrastructure (`testserver`, `daemontest`, fixtures, helpers) | `kwb` | `bwk` |
| QA / verification gate: installed binary, CLI and daemon entry points, `scripts/`, example recipes, the manual live tier — expected-vs-actual evidence, bug reproduction | `jbh` (Bach) — read-only + Bash | `kwb` — a broken journey becomes a failing test |
| Formal methods (Z): daemon-pipeline and trust-model schemas, model-checking a formal class before implementation | `jms` (Spivey) | `jra` (Abrial) — B method |
| Formal methods (B): refinement, Event-B | `jra` | `jms` |

## Task Tracking

Pipelines handle stage sequencing via `depends_on`. Use beads for tracking at the epic/task level. Use `ethos mission show <id>` and `ethos mission log <id>` for per-stage tracking. Do not duplicate pipeline sequencing in TaskCreate.

Mission contract YAMLs go in `.tmp/missions/`. Result artifact YAMLs go in `.tmp/missions/results/`.

## Biff Coordination

Biff is the team messaging system. Use it for presence, coordination, and async communication.

- `/tty <name>` — name this session
- `/plan <summary>` — set what you're working on
- `/who` — check who's active before destructive git operations
- `/read` — check inbox for messages
- `/write @<agent> <message>` — send a direct message
- `/wall <message>` — broadcast to all active agents

Start every session with `/tty beadle`, `/plan`, and `/loop 5m /biff:read`. All three before any bead work.

Every biff message requires a reply. Acknowledge instructions, answer questions, or confirm receipt. Silence is not acceptable.
