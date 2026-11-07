# Development Workflow

All work in this repository runs as three nested loops. The outermost loop
owns the backlog: what is worth doing and in what order. The middle loop owns
one pull request: a single shippable, revertible change. The innermost loop
owns one mission: a single delegated piece of work inside that change.

```text
Level 1 — Backlog loop   one iteration = one work batch     (beads)
  Level 2 — PR loop      one iteration = one pull request
    Level 3 — Mission    one iteration = one delegated mission
                         (design, code, test — a do-while)
```

Each level hands work down and receives finished results back. Escalation of
scope only goes up: a mission that uncovers a bigger problem grows its PR; a
PR that uncovers a new line of work files a bead for the backlog loop. Defects
move the other way — anything found while a PR is open is fixed in that PR,
never sent back to the backlog.

This file instantiates
[punt-kit/standards/workflow.md](https://github.com/punt-labs/punt-kit/blob/main/standards/workflow.md) for
beadle, and defers the PR-review machine to
[punt-kit/standards/pr-review.md](https://github.com/punt-labs/punt-kit/blob/main/standards/pr-review.md). The
pseudocode at each level gives the control flow; a small Z schema at each
level gives the doorway conditions — what must be true to enter an iteration
and what must be true to leave it. The state those schemas observe:

```text
LoopState
  signals       : ℙ SIGNAL  -- issues, alerts, messages not yet triaged
  open          : ℙ BEAD    -- open beads: the single work funnel
  validated     : ℙ BEAD    -- re-proven against current main
  claimed       : ℙ BEAD    -- the current batch
  closed        : ℙ BEAD    -- beads completed by a merged PR
  activeWorkers : ℙ WORKER  -- sub-agents editing the shared worktree
  testCount     : ℕ         -- tests collected by go test
  ------------------------------------------------------------------
  validated ⊆ open
  claimed   ⊆ validated
  open ∩ closed = ∅         -- a bead is open or closed, never both
```

A bead's lifecycle is a walk through these sets: `open → validated → claimed
→ closed`. Steps that were performed ("the recap was sent") appear as named
predicates over declared terms, never as bare primed flags.

## Roles

The operator owns requirements and design direction, rules on genuine design
forks, and confirms demos of what only a human can judge. Claude runs
everything else: the backlog, the missions, the review cycles, and the
merges. Review of code is done by agents — the local review agents before the
PR, then Qodo and Bugbot on the PR. There is no human review gate. The
operator reads code in the IDE, but that inspection is separate, feeds design
discussion only, and nothing in these loops waits on it.

## Level 1 — The backlog loop

The backlog loop keeps the bead tracker true and decides what to work on
next. Beads are the single funnel: every piece of work, whatever its origin,
is a bead before it is anything else. One iteration selects and completes one
batch of work. The loop runs at session start and again whenever the current
batch is done.

```text
function backlog_loop():

    # 1. INTAKE — every signal becomes a bead or is disposed at the door
    for signal in [github_issues, dependabot_alerts, biff_messages,
                   operator_requests, new_scope_found_while_working]:
                   # a defect inside an open PR's unit is fixed in that PR,
                   # never filed — only genuinely NEW scope arrives here
        if duplicate(signal):  close_at_door(signal, link_existing_bead)
        elif invalid(signal):  close_at_door(signal, stated_reason)
        else:                  bd_create(signal, labels, link_back_to_source)

    # 2. VALIDATE — a bead must be true before it is workable
    for bead in candidates(bd_ready):
        confirm it is still real against current main
        confirm nothing merged or decided has superseded it
        confirm it is one rollback-coherent unit (split or merge if not)
        confirm its blocked-by links reflect reality
        otherwise: fix the bead, or close it with the reason

    # 3. ASSESS — automatic ordering; escalate only on a genuine fork
    order = sort(validated, by:
        security severity                  # trust-model / PGP defects first
        > broken email journeys            # send/read/verify paths in use
        > active epic continuity           # finish what is started
        > debt that blocks throughput      # decomposition, testability
        > features)                        # shipping scope before nice-to-have
    if the ordering hits a fork the charter cannot resolve:
        ask the operator for a focus ruling
    else:
        proceed without asking

    # 4. SELECT AND EXECUTE
    batch = claim a realistic set from the top of the order
    for unit in rollback_coherent_units(batch):
        pr_loop(unit)                      # Level 2

    # 5. CLOSE OUT
    close GitHub issues resolved by the merged PRs, linking each PR
    send the batch recap via beadle-email  # covers what the per-merge recaps
                                           # do not: intake dispositions, beads
                                           # closed at validation, order changes
    return to intake — new signals have accrued while working
```

Entry and exit for one batch iteration:

```text
EnterBatch ≙ [ LoopState ]                -- no precondition: the backlog loop
                                          -- is a do-while(true); intake always
                                          -- has standing to run

ExitBatch ≙ [ Δ LoopState |
  intakeDisposed(signals)                 -- every signal observed at this
                                          --   iteration's intake became a bead
                                          --   or was closed at the door with a
                                          --   reason; new signals keep accruing,
                                          --   so the live queue is never empty
  ∧ claimed′ = ∅ ∧ claimed ⊆ closed′      -- the batch drained: every claimed
                                          --   bead closed by a merged PR
  ∧ resolvedIssuesClosed(mergedPRs)       -- GitHub issues answered with their PR
  ∧ batchRecapSent(claimed) ]
```

### Intake

Work arrives from five places: GitHub issues, Dependabot security alerts,
biff messages from agents in other repositories, requests from the operator,
and new lines of work we discover ourselves. That last source carries a
boundary: a defect found inside the unit of an open PR is fixed in that PR and
never becomes a bead; only genuinely new scope — discovered outside any open
PR, or clearly a separate rollback unit — enters at intake. At intake, each
signal is either turned into a bead or closed at the door with a stated reason
— duplicate of an existing bead, or invalid. Nothing is left sitting in an
external queue: a GitHub issue gets a reply naming its bead, and it is closed
when that bead closes.

Security alerts map severity to priority: a critical or high alert becomes a
P1 bead and goes to the front of the order. Because beadle handles owner
credentials, PGP keys, and a tamperproof audit log, a defect that weakens the
trust model is a security alert, not a feature bug — it jumps the queue.

Intake must not depend on remembering to look. A recurring poll (via the
`/loop` skill) or a session-start sweep checks the GitHub issue list and the
Dependabot alert list, so an alert filed overnight is a bead by the time the
first batch is selected.

### Validation

The codebase moves; beads rot. Before a bead is workable, confirm it is still
real: reproduce the bug or re-check the premise against current main, confirm
no merged PR or design decision has superseded it, confirm it describes one
rollback-coherent unit of work, and confirm its dependency links are correct.
A stale bead is closed with the reason. A bloated bead is split. Validation
covers the candidates for the coming batch, not the whole backlog every time.

### Assessment

Ordering is automatic in the steady state. The sort is:

1. **Security severity.** Any defect that weakens the four-level trust model,
   leaks credentials, or corrupts the audit log outranks everything.
2. **Broken email journeys.** A bug on a path owners actually hit — send,
   read, list, verify, move — beats new work.
3. **Active epic continuity.** An epic in flight keeps its claim until it is
   done. Interleaving epics is how a backlog churns without shipping.
4. **Debt that blocks throughput.** Decomposition and testability work that
   makes the next ten changes cheaper.
5. **Features.** Shipping-scope work before nice-to-haves.

The operator is asked for a focus ruling only when the ordering hits a genuine
fork the rules above cannot resolve: two epics competing to be the active one,
a strategic pivot, or a drift in the debt-versus-feature balance. Direction
belongs to the operator; sequencing inside a settled direction does not
require asking.

## Level 2 — The PR loop

One iteration of the PR loop produces one merged pull request. It is entered
when the backlog loop hands down a unit of work sized for throughput, and it
runs the missions (Level 3) needed to build that unit.

### Sizing: throughput, not purity

Nobody reads these diffs line by line. Agents review them, and they
squash-merge. So the size of a PR is an economic decision, not a hygiene one:

- **The floor is transaction cost.** Every PR pays a roughly fixed overhead —
  branch, full-diff review rounds, the demo, CI, remote review cycles, the
  merge gate, the recap. A PR too small pays that overhead for too little
  value.
- **The ceiling is reviewer effectiveness.** The reviewers are agents, and
  past a certain diff size their quality drops. A PR too large buys throughput
  at the cost of review quality.
- **The typical right size** is several small beads batched together, or one
  coherent slice of a larger bead. A bead too large to slice into one PR is
  mis-filed: decompose it into an epic whose child beads fit the band.
- **Rollback coherence still binds.** Whatever merges must revert together
  sensibly. That is the one structural constraint. "Purity," "one concern per
  PR," and "keep the diff small" are not criteria — a docs fix, a debt
  paydown, or an adjacent bug fix riding along is welcome, and an improvement
  is never held back or split out for tidiness.

```text
function pr_loop(unit):

    # A. BUILD — missions, one at a time
    branch from main                      # feat/ fix/ refactor/ docs/ chore/
    if unit is architectural (new contract, new types, trust-model change,
                              new channel, cross-cutting flow):
        design mission first, with no prescribed write-set
        leader reviews the design end-to-end
        substantive issues go to the operator as concrete decisions
        implementation waits for the operator's ratification
    for mission in unit:
        mission_loop(mission)             # Level 3
        # bug fixes are TDD: a failing test reproduces the defect first
        # trust-model / concurrency work gets its z-spec model-check first

    # B. FULL-DIFF VERIFICATION (local, before any PR exists)
    make check                            # full gate on the accumulated diff
    repeat:
        findings = local review agents on the full diff   # see below
        fix every finding in this PR      # no deferrals
    until a round produces zero findings

    # C. DEMO — drive the real entry point, not just the tests
    qa_mission(unit)                      # jbh (qa-engineer), read-only + Bash:
        make install                      #   build + copy beadle-email to ~/.local/bin
        write down the expected outcome, then exercise the CLI entry points:
            beadle-email list|read|send|move|doctor|status
        cover one invalid input, one missing-dependency case, one boundary
        report expected-vs-actual, verified / inferred / unknown kept apart,
            and name the MCP and human-judged remainders
    the leader runs the MCP-tool journeys jbh cannot reach (jbh has Bash
        but no beadle-email MCP tools): the `email` tools over the running
        `beadle-email serve`, expected output written first
    a journey jbh or the leader finds broken is a defect: kwb writes the
        failing test, the implementer fixes it, in this PR
    the operator confirms the human-judged remainder — a real message
        sent and received, a signature that verifies, a trust level
        classified correctly against a live mailbox

    # D. SHIP
    bd close the bead
    push; create the PR (description carries the verification playbook)
    # Qodo reviews once on open; Bugbot reviews once on open — pr-review.md
    schedule a background poll with /loop   # never gh pr checks --watch,
                                            # never foreground sleep loops

# poll_tick and merge_gate are NOT redefined here. They are
# pr-review.md's, run exactly as written there — including the demo
# re-confirmation after a non-exempt fix push (demo_ok), the reviewed_head
# / local delta-review transition that keeps a changed head from merging
# unreviewed, the round-2 Qodo+Bugbot trigger after a round-1 High fix, and
# the separate check for queued or in-progress Bugbot runs. Restating a
# subset here would create a second, weaker gate that drifts from the
# standard; do not. beadle adds only these deltas on top of the standard's
# loop, nothing subtracted:
#   - CI failure: `gh run view --log-failed`; fix in this PR
#   - a fix commit runs local_review (below) on its diff before the push
#   - the demo gate is driven by jbh (qa-engineer); its re-confirmation
#     after a non-exempt fix push is the standard's demo_ok, exercised
#     through beadle's real entry points (§ The demo gate)
#   - merge is squash with expectedHeadSha; mergeStateStatus must be in
#     {CLEAN, HAS_HOOKS}
# When the standard's merge_gate holds: merge (squash); close_out(). Do not
# ask, do not wait.

function close_out():
    cancel the poll loop
    delete the branch; checkout main; pull
    send the merge recap via beadle-email # to jim@punt-labs.com, every merge,
                                          # unprompted — the 8-part recap plus
                                          # the review-metrics recap block from
                                          # ../.bin/review-metrics.py pr
                                          # punt-labs/beadle <N> --format recap
    start the next unit immediately       # no stopping to report
```

Entry and exit for one PR iteration. The pre-merge gate is pr-review.md's
`merge_gate`, not a subset restated here; `ExitPR` names the state after
close-out completes and carries every conjunct the standard's gate requires.

```text
EnterPR ≙ [ LoopState; unit : ℙ BEAD |
  unit ⊆ claimed
  ∧ rollbackCoherent(unit)                -- reverts together sensibly
  ∧ inThroughputBand(unit)                -- above transaction cost,
                                          --   below reviewer degradation
  ∧ (architectural(unit) ⇒ designRatified(unit)) ]  -- operator has ruled

ExitPR ≙ [ Δ LoopState; pr : PR |
  merged(pr)
  ∧ localFindings(pr) = ∅                 -- held BEFORE the PR opened, and
                                          --   before every fix push
  ∧ demoOk(pr)                            -- the standard's demo_ok: the demo
                                          --   covers the head, re-confirmed by
                                          --   jbh after any non-exempt fix push
  ∧ ciGreen(head(pr))
  ∧ ¬ pending(pr)                         -- Qodo and Bugbot rounds in
  ∧ ¬ bugbotOpen(pr)                      -- no queued/in-progress Bugbot run
  ∧ reviewedHead(pr) = head(pr)           -- the merged commit was reviewed;
                                          --   a fix push advances it only after
                                          --   its delta review's findings clear
  ∧ (∀ f : findings(pr) • answered(f))    -- fixed on head, or reasoned
  ∧ unresolvedThreads(pr) = ∅
  ∧ round(pr) ≤ 2                         -- round 2 only after a High fix
  ∧ openEscalations(pr) = ∅
  ∧ beadsOf(pr) ⊆ closed′
  ∧ mergeRecapSent(pr) ]
```

### Local review before the PR

Full-diff local review is where issues die cheaply. A local round costs
seconds; a remote review cycle costs minutes to hours. The review sequence —
agent selection, round limits, the dismissal standard, and what "installed"
means — is owned by
[punt-kit/standards/pr-review.md](https://github.com/punt-labs/punt-kit/blob/main/standards/pr-review.md). For
beadle: `code-reviewer`, `silent-failure-hunter`, and `qodo review` run on the
unit's first full diff and on every fix diff of 20 lines or more;
`invariant-completeness-reviewer` joins any claimed invariant or
closed-set classification; and `djb` joins any diff that touches credentials,
secrets, or caller text reaching a file, a log line, or a header — in beadle
that is `internal/pgp`, `internal/secret`, and the daemon's command-file
loader. Every finding is fixed in this PR. A dismissal requires the exact
finding, the specific reason it does not apply, and the code reference.
"Pre-existing", "by design", "intentional", and "expected" are not reasons.
Opening a PR with unresolved local findings is a procedural violation.

### Remote review: Qodo and Bugbot

Remote review is automated-only and runs on already-clean code. Qodo is the
primary reviewer (`.pr_agent.toml`): it reviews when the PR opens, never on
push; Highs post as blocking threads, Mediums in the summary, Lows are
dropped. Bugbot reviews each PR once, on open. Round 2 (one `/agentic_review`
plus one `bugbot run`) happens only when round 1's fixes included a High, and
it is the last; a new High in round 2 goes to the operator. The full
terminology — `round`, `qodo_in`, `bugbot_in`, `answered(f)`, `reviewed_head`,
`bugbot_open`, the outage fallbacks, and the model-checked rules — is
[punt-kit/standards/pr-review.md](https://github.com/punt-labs/punt-kit/blob/main/standards/pr-review.md)'s, run
as written there. Copilot is not a default reviewer; request it only when the
operator asks, at open.

### The demo gate

`make check` passing means the code compiles and the tests pass. It does not
mean the feature works. Before any PR opens, `jbh` (qa-engineer) drives the
feature through its real entry point — an MCP tool call over `beadle-email
serve`, a `beadle-email` CLI subcommand, `beadle-email doctor` — with the
expected outcome written down first, then compared against actual output, and
returns an expected-vs-actual evidence block (verified / inferred / unknown
kept separate). Because beadle talks to a live mail system, the human-judged
part is real: a message actually sent and received, a PGP signature that
actually verifies, a sender actually classified at the right trust level. That
remainder is the operator's to confirm, and it is a demo, not a diff.
Docs-only changes are the one exception — markdownlint plus a read-through is
their verification.

## Level 3 — The mission loop (design, code, test)

One iteration is one delegated mission: a single piece of design,
implementation, test, or review work executed by a specialist sub-agent under
an ethos mission contract. The next mission does not start until this loop
completes on the current one. Pipeline selection, the worker/evaluator table,
and the contract schema live in
[`.claude/rules/delegation.md`](../.claude/rules/delegation.md); this section
owns only the control flow.

The mission loop is a do-while: the work runs at least once, and the
review-and-fix cycle repeats until a round comes back clean.

```text
function mission_loop(mission):
    contract = write_contract(mission)   # problem, invariants, quality bar,
                                         # commit discipline — never a
                                         # write-set for design work
    dispatch(contract)                   # mission create + background spawn;
                                         # verify the worker is running
    do:
        worker designs / codes / tests   # tests lead; TDD for bug fixes;
                                         #   the worker's unit tests ship in
                                         #   the same commit as the code
        worker commits locally           # each commit passes make check
        result   = worker submits
        findings = test-engineer tests   # kwb: adversarial tests for the
                                         #   worker's code and the pyramid
                                         #   tiers its commit does not reach;
                                         #   *_test.go only; a test needing a
                                         #   production change is a finding
                 + evaluator review      # distinct specialist (delegation.md)
                 + leader verification   # run the change, review agents
                                         # on the mission diff
        if findings: reflect(findings)   # another round, same mission
    while findings remain
    close(mission)
    # the test stage is a seat, not a step the implementer performs on itself:
    # the implementer is never the test author of record, and the test author
    # never edits production code
```

Entry and exit for one mission iteration:

```text
EnterMission ≙ [ LoopState; m : MISSION |
  contracted(m)                           -- problem, invariants, quality bar
  ∧ workerRunning(m)                      -- dispatch is two operations; a
                                          --   contract alone is orphaned work
  ∧ activeWorkers = {workerOf(m)}         -- one worker at a time in a shared
                                          --   worktree
  ∧ (trustOrConcurrencyClass(m) ⇒ modelChecked(m)) ]  -- z-spec before code

ExitMission ≙ [ Δ LoopState; m : MISSION |
  verdict(m) = accept                     -- from an evaluator ≠ worker
  ∧ findings(m) = ∅                       -- the do-while ran dry
  ∧ (∀ c : commitsOf(m) • checkGreen(c))  -- every commit passed make check
  ∧ testCount′ ≥ testCount                -- coverage never decreases
  ∧ missionClosed(m) ]
```

### Who does what

The leader runs the workflow; the specialists produce the work. The boundary
is strict in both directions.

**The leader owns the workflow.** The backlog, the mission contract,
dispatch, monitoring, the local review agents (run on each mission's diff and
on the full diff), the demo, and every git and GitHub operation: creating
branches, pushing, opening the PR, driving remote review, resolving threads,
merging, and closing out. The leader does not write production code. The
leader authors only docs (CLAUDE.md, DESIGN.md, README.md, CHANGELOG.md, this
file, memory and plan files).

**The worker owns the work.** The thinking and the code inside its mission —
the design decisions its contract leaves open, the tests, the implementation,
and local commits on the current branch. A worker never creates branches,
never pushes, never opens PRs, and never touches review threads. Putting
workflow operations into a worker's prompt is a contract defect.

**The evaluator** — always a different specialist from the worker, per the
table in `delegation.md` — reviews the worker's result inside the mission
before the leader accepts it.

**The test/QA seats never write production code.** `kwb` (test-engineer, Beck)
owns the test stage: it writes the tests the implementer did not — the
untested error path, the invariant asserted only in a comment, the pyramid
tiers above unit (`testserver` integration, `daemontest` end-to-end, property
and boundary cases, the `-tags integration` suite) — and reproduces every
reported bug as a failing test before its fix lands, writing only
`*_test.go`/`testdata/`/`internal/*test/`. `jbh` (qa-engineer, Bach) owns the
demo gate and has no Write/Edit at all. Neither is the implementer whose code
is under test.

**Remote review findings are the one delegation that is not a mission.** When
Qodo or Bugbot report on the PR, the leader reads each finding and hands the
mechanical fix to a bare agent, then pushes and resolves the thread itself.

### The lifecycle

1. **Contract.** The leader writes the mission contract: the problem, the
   invariants, the quality bar, and the commit discipline — one commit per
   logical step, each passing `make check`, never more than thirty minutes of
   work uncommitted. A design mission's contract never prescribes a write-set;
   the specialist decides what to create, split, or extract. For crypto or
   trust-boundary work the contract names the invariant it must not break
   (zero agent authority, isolated keychain, non-expiring keys rejected,
   tamperproof audit log).
2. **Dispatch is two operations.** Creating the mission writes the contract; a
   separate background agent spawn starts the worker. Verify the worker is
   actually running — a contract with no agent behind it is orphaned work.
3. **The worker executes, then `kwb` tests it.** Tests lead: a bug fix starts
   from a failing test that reproduces the defect; a feature ships its unit
   tests with the code; the test count never goes down. Work in the
   trust-model, concurrency, or stateful-protocol class gets its z-spec
   model-check before implementation. Every commit passes `make check` — zero
   exceptions, zero suppressions. The test engineer (`kwb`) then writes the
   tests the implementer did not, on the tiers the implementer's commit does
   not reach.
4. **The leader monitors by the filesystem, never by git activity.** A worker
   editing files is working, even with zero commits — analysis and reading are
   invisible. An empty commit log is never a reason to intervene, commit by
   proxy, or stop the agent. A genuine stall — no file changes over a long
   window and no response to a status message — is the only cause for taking
   over. Monitoring is silent: concrete facts or action, no narration of the
   wait.
5. **Result and evaluation.** The worker submits its result; the evaluator
   reviews it; reflect-and-advance rounds continue until the evaluator
   accepts.
6. **The leader verifies and closes.** Check the result against the contract.
   Dispatch `jbh` to drive the change through its real entry points — `make
   install`, then the CLI subcommands and `doctor` — with expected output
   written first and one invalid input, one missing dependency, one boundary
   covered; `jbh` returns an expected-vs-actual evidence block. `jbh` has Bash
   but no beadle-email MCP tools, so the leader runs the MCP-tool journeys
   itself (the `email` tools over a running `beadle-email serve`), expected
   output written first. The leader reads jbh's evidence, re-runs the one line
   it doubts, and never accepts on the worker's report alone. Run
   the applicable local review agents on the mission diff and fix every
   finding; a dismissal requires the exact finding, the specific reason it
   does not apply, and the code reference. If the result raises a design
   question, it goes to the operator as a concrete decision — with a
   recommendation — before any dependent mission dispatches. Then close the
   mission.

## Formal verification (z-spec)

Some defects are not fixable by writing another test. When a change involves
the **trust model, a lock discipline, a stateful protocol, or the audit-log
state machine**, model-checking finds in one pass what empirical testing
chases across many rounds. z-spec is **required** for a change in any of these
classes:

- **Trust-level classification.** The four-level model (`trusted`, `verified`,
  `untrusted`, `unverified`) is a state machine with a safety invariant: a
  message is classified at exactly one level, and no external sender is ever
  classified `trusted`.
- **Preflight / permission validation.** "Preflight before execute" and "zero
  agent authority" are safety invariants of the form "never X before Y" —
  model-check that no execution path bypasses preflight.
- **Concurrency / interleaving.** Any change where the daemon, a poller, and a
  command runner contend for shared state (the audit log, a mailbox, a config
  file).
- **The recurrence signal (the hard rule).** The moment the *same class* of
  defect surfaces across two or more fix/review rounds — stop. Do not open a
  third empirical round. Formalize the state machine and model-check it.

Use the z-spec plugin skills: `z-spec:code2model` to model the entity,
`z-spec:check` to `fuzz` type-check, `z-spec:test` to model-check every
invariant and deadlock, then a fidelity check — the model must reproduce the
known defect when the fix is removed. Commit the spec (`docs/<name>.tex`) as a
regression artifact. Purely sequential logic and data-format tweaks do not
need z-spec — a table test suffices.

## Branch discipline

All code changes go on feature branches. Never commit directly to main;
branch protection blocks it. Regular branches by default; use a worktree only
when `/who` shows other active sessions that could conflict with your working
tree.

```bash
git checkout -b feat/short-description main
```

| Prefix | Use |
|--------|-----|
| `feat/` | New features |
| `fix/` | Bug fixes |
| `refactor/` | Code improvements, no behavior change |
| `docs/` | Documentation only |
| `chore/` | Build, dependencies, CI, config |

## Micro-commits

- One logical change per commit. A single refactor touching 10 files is still
  one logical change.
- `make check` passes before every commit — not before the PR, before each
  commit. A commit that fails the gate is a broken commit.
- Commit message format: `type(scope): description`, using the prefixes above.

## Documentation discipline

Every PR updates the docs it affects. If a PR changes user-facing behavior and
the diff is missing any of these, the PR is not ready to merge.

- **CHANGELOG**: entries under `## [Unreleased]`, following
  [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) — Added, Changed,
  Deprecated, Removed, Fixed, Security.
- **README**: update when user-facing behavior changes — new flags, commands,
  defaults, or config.
- **DESIGN.md**: add an ADR for a design decision with rejected alternatives.

## Invariants

1. **Beads are the single funnel.** Every piece of work is a bead before it is
   anything else; external queues (issues, alerts) drain into it at intake.
2. **A satisfied merge gate means merge now.** The gate is the only path to a
   merge, and nothing waits once it passes. Merge is always explicit — never
   auto-merge, never an admin override.
3. **Findings never wait.** Review feedback is handled the moment it arrives,
   never in a follow-up PR. A thread is resolved only once it is answered, one
   by one, never in bulk.
4. **A push restarts CI, not review.** Local rounds run until clean before the
   PR opens and before every fix push. PR rounds are at most two, and the
   second only after a High fix.
5. **Defects flow inward, scope flows outward.** A defect found while a PR is
   open is fixed in that PR. Only genuinely new lines of work become beads.
6. **The operator gates demos and direction, never diffs or sequencing.**
7. **Close-out is inside the loop.** The recap email, branch hygiene, and
   starting the next unit are steps of the loop, not afterthoughts.
