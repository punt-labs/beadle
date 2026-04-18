# Beadle Development Process

How the COO executes multi-task epics using ethos pipelines, missions,
and specialist sub-agents. This is the approved process for all
non-trivial feature work in beadle.

## Principles

1. **The COO does not write code.** All implementation is delegated
   via ethos missions to specialist sub-agents.
2. **Sub-agents are stateless.** Each spawn is fresh — no memory of
   prior runs. The mission contract carries context between stages
   via `inputs_from`.
3. **The architecture doc is not the design.** An architecture doc
   describes the end state. A task-level design describes how to get
   from the current code to that state — exact struct changes, function
   signatures, migration steps, test cases. Both must exist before
   implementation begins.
4. **The pipeline is the sequencer.** `ethos mission pipeline instantiate`
   creates one mission per stage with `depends_on` wiring. The COO is
   the quality gate between stages, not a scheduler.
5. **One branch, one PR.** All stages commit to the same feature branch.
   One PR at the end. Tests must pass at every commit.

## Pipeline Selection

Every non-trivial task uses an ethos pipeline. Select by evaluating
these rules in order:

| Pipeline | When |
|----------|------|
| `quick` | Bug fixes, single-file tasks, mechanical updates |
| `docs` | Documentation-only changes |
| `coverage` | Test gap identified |
| `standard` | Features: 4+ files, clear goal, needs design through documentation |
| `product` | New feature with product uncertainty |
| `formal` | Complex stateful systems needing Z-spec |
| `coe` | Recurring bugs, incidents |
| `full` | Epics, cross-cutting work |

Escalation only goes up. Never demote mid-flight.

## Execution: The Standard Pipeline

The `standard` pipeline has 5 stages. This is the default for feature
work. Each stage produces an artifact that feeds the next.

### Stage 1: Design

**Who:** Worker (bwk for Go work).

**Input:** The architecture doc (end-state description) and the current
codebase.

**Output:** A task-level implementation design. For each task in the
epic:

- Exact struct changes (fields added, fields removed, new types)
- Exact function signature changes
- Exact migration steps (YAML files, test fixtures, validators)
- Exact test cases to write
- Dependency order between tasks

The design must be concrete enough that implementation requires no
architectural judgment. If bwk has to make a design decision during
stage 2, the stage 1 design was incomplete.

**COO role:** Review the design. Verify it matches the architecture
doc. Verify every task is covered. Verify migrations are atomic where
required (e.g., struct + YAML + test fixtures in one commit when
`KnownFields(true)` is in play). Reflect with findings or pass.

### Stage 2: Implement

**Who:** Worker (same as stage 1).

**Input:** The stage 1 design (via `inputs_from: design`).

**Output:** Working code. All tasks implemented in dependency order.
`go vet` and `go test -race` passing.

The worker reads the stage 1 design and executes it mechanically.
Deviations from the design are findings in review — not creative
latitude.

**COO role:** Review the diff against the stage 1 design. Any
deviation is a finding. Verify tests pass. Reflect, advance if needed.
Commit when clean.

### Stage 3: Test

**Who:** Worker (same).

**Input:** The stage 2 implementation (via `inputs_from: implement`).

**Output:** Additional integration and edge-case tests beyond what
stage 2 produced. Focus areas:

- Happy path through the full feature
- Error paths and boundary conditions
- Cross-component interactions (e.g., hybrid pipelines with mixed runners)
- Failure modes (e.g., mid-chain abort, schema rejection)
- Security-relevant behavior (e.g., whitelist enforcement)

**COO role:** Review test coverage. Are the critical paths tested? Are
the edge cases from the architecture doc covered? Reflect, advance if
needed. Commit when clean.

### Stage 4: Review

**Who:** Security engineer (djb) for security-critical work; otherwise
the default evaluator.

**Input:** The full diff from stages 2-3 (via `inputs_from: test`).

**Output:** A review report with findings categorized by severity.

**COO role:** Read every finding. For each:

- If valid: spawn a fresh worker to fix. The fix is a new round within
  stage 4 — not a new stage. After the fix, spawn the reviewer again
  to verify. Repeat until the reviewer reports clean. This cycle may
  take multiple rounds.
- If invalid: document why in the reflection and close.

Do not skip findings. Do not defer findings to follow-up work. Fix
everything in this stage.

### Stage 5: Document

**Who:** COO directly (documentation is not delegated).

**Output:**

- CHANGELOG entry under `## [Unreleased]`
- DESIGN.md ADR status updates (PROPOSED → SETTLED)
- DESIGN.md example updates if the implementation changed field names
- README updates if user-facing behavior changed

Commit when done.

## Execution Loop Per Stage

```text
1. Spawn: Agent(subagent_type=<worker>, run_in_background=true)
   Prompt: "Mission <id> is yours. Read it: ethos mission show <id>."
   Fresh agent — no memory of prior stages.

2. Wait: notification arrives when agent completes.

3. Review: read the diff and the result artifact.

4. Reflect: ethos mission reflect <id> --file <path>
   Pass → mission closes, next stage unblocks.
   Findings → ethos mission advance <id>.

5. If advanced: spawn fresh worker to address findings.
   Go to step 2. Repeat until clean.

6. Commit: git add + git commit. Tests must pass.
```

## Stage 4 Review Cycle (Detail)

Stage 4 can take multiple rounds. Each round is:

```text
Round N:
  1. Spawn reviewer (djb or evaluator)
  2. Reviewer produces findings
  3. COO reads findings, reflects
  4. If findings exist:
     a. Spawn fresh worker (bwk) to fix
     b. Worker fixes, submits result
     c. COO reviews fix, advances to round N+1
     d. Go to step 1 — reviewer re-reviews
  5. If no findings: close stage
```

Budget the mission with enough rounds for 2-3 review cycles.
Security-critical stages (whitelist, exec, crypto) typically need
2 cycles. Interface changes typically need 1.

## Setup Checklist

Before starting an epic:

1. `bd update <epic-id> --status=in_progress`
2. `git checkout -b feat/<name> main`
3. `/plan` with epic summary
4. `ethos mission pipeline instantiate <pipeline> --leader claude --worker bwk --evaluator mdm --var feature=<name> --var target=<path>`
5. Close the design stage immediately if the architecture doc already exists (rare — usually the detailed design still needs to be produced)
6. Begin stage 1

## Shipping

After stage 5:

1. `make check` — all quality gates pass
2. Push branch, create PR
3. Copilot review cycle (2-6 rounds expected)
4. Merge
5. `bd close` for all task beads and the epic
6. Recap email to the CEO

## What Can Go Wrong

| Risk | Mitigation |
|------|-----------|
| Stage 1 design is incomplete | Stage 2 worker makes design decisions → caught in review as deviations |
| Stage 2 is too large (many tasks) | Reflect with specific findings per task, not a blanket "redo" |
| Stage 4 review finds many issues | Multiple rounds — budget for it. Don't rush to close. |
| Worker misreads the mission contract | Fresh spawn with clearer prompt. The contract is the spec — if the contract is ambiguous, fix the contract. |
| Tests pass but the feature doesn't work | Stage 3 must include e2e tests, not just unit tests. COO verifies by running the code. |
