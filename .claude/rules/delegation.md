# Delegation with Missions and Pipelines

All code delegation uses ethos missions. Missions are typed contracts between a leader (claude) and a worker (bwk, mdm, djb, adb) that enforce write-set admission, frozen evaluators, bounded rounds, and append-only event logs.

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

This creates 5 missions (design → implement → test → review → document) with `inputs_from` dependency edges. The worker picks up each stage in order. The leader is the quality gate between stages.

## Execution Loop

For each stage in the pipeline:

1. **Spawn**: `Agent(subagent_type=<worker>, run_in_background=true)` with a prompt pointing at the mission ID. Fresh agent per stage — sub-agents are stateless, the mission contract carries context.
2. **Wait**: notification arrives when agent completes.
3. **Review**: read the diff and the result artifact.
4. **Reflect**: `ethos mission reflect <id> --file <path>`. Pass or findings.
5. **Advance or close**: if findings → `ethos mission advance <id>`, spawn fresh worker to fix. If clean → mission closes, next stage unblocks.
6. **Commit**: after each clean stage. Tests must pass at every commit.

For security-critical stages, spawn djb (cryptographic implementation) or bcs (threat-modeling and policy) as a separate background review agent before reflecting.

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
worker: bwk                    # bwk|rsc|mdm|rop|adb|kth|djb|bcs
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
| CLI / command design | `mdm` (McIlroy) | `rop` (Pike) — Plan 9 minimalism |
| CLI minimalism / man-page | `rop` | `mdm` |
| Crypto / PGP implementation | `bwk` | `djb` (Bernstein) |
| Threat-modeling / policy | `claude` (leader) | `bcs` (Schneier) |
| Infrastructure / CI | `adb` (Lovelace) | `kth` (Hightower) — cloud-native |
| Cloud-native / kubernetes | `kth` | `adb` |
| Product validation (PR/FAQ) | `claude` (leader) | `mcg` (Cagan) |
| Product discovery / interviews | `claude` (leader) | `tdt` (Torres) |

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
