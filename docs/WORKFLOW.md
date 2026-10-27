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

The pseudocode at each level gives the control flow; a small Z schema at each
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
forks, and confirms verification of what only a human can judge. Claude runs
everything else: the backlog, the missions, the review cycles, and the
merges. Review of code is done by agents — the local review agents before
the PR, then Qodo and Bugbot on the PR. There is no human review gate. The operator
reads code in the IDE, but that inspection is separate, feeds design
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

Work arrives from five places: GitHub issues filed by users, Dependabot
security alerts, biff messages from agents in other repositories, requests
from the operator, and new lines of work we discover ourselves. That last
source carries a boundary: a defect found inside the unit of an open PR is
fixed in that PR and never becomes a bead; only genuinely new scope —
discovered outside any open PR, or clearly a separate rollback unit — enters
at intake. At intake, each signal is either turned into a bead or closed at
the door with a stated reason — duplicate of an existing bead, or invalid.
Nothing is left sitting in an external queue: a GitHub issue gets a reply
naming its bead, and it is closed when that bead closes.

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
3. **Active epic continuity.** An epic in flight (e.g. the headless Docker
   agent) keeps its claim until it is done. Interleaving epics is how a
   backlog churns without shipping.
4. **Debt that blocks throughput.** Decomposition and testability work that
   makes the next ten changes cheaper.
5. **Features.** Shipping-scope work before nice-to-haves.

The operator is asked for a focus ruling only when the ordering hits a
genuine fork the rules above cannot resolve: two epics competing to be the
active one, a strategic pivot, or a drift in the debt-versus-feature balance
that changes what shipping means. Direction belongs to the operator;
sequencing inside a settled direction does not require asking.

## Level 2 — The PR loop

One iteration of the PR loop produces one merged pull request. It is entered
when the backlog loop hands down a unit of work sized for throughput, and it
runs the missions (Level 3) needed to build that unit.

### Sizing: throughput, not purity

Nobody reads these diffs line by line for style. Agents review them. So the
size of a PR is an economic decision, not a hygiene one:

- **The floor is transaction cost.** Every PR pays a roughly fixed overhead —
  branch, full-diff review rounds, verification, CI, remote review cycles, the
  merge gate, the recap. A PR too small pays that overhead for too little
  value.
- **The ceiling is reviewer effectiveness.** The reviewers are agents, and
  past a certain diff size their quality drops: local review agents lose
  focus across a sprawling diff, and the remote reviewers degrade outright on
  very large ones. A PR too large buys throughput at the cost of review
  quality.
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
        # coverage rises with every mission
        # trust-model / concurrency work gets its z-spec model-check first

    # B. FULL-DIFF VERIFICATION (local, before any PR exists)
    make check                            # full gate on the accumulated diff
    if local_review(full diff) is blocked: stop the unit until the
        operator rules                    # local_review: after close_out

    # C. VERIFY — drive the real entry point, not just the tests
    make install                          # build + copy beadle-email to ~/.local/bin
    write down the expected outcome, then exercise the change:
        MCP tools via `beadle-email serve` (restart Claude Code to reload)
        CLI via `beadle-email list|read|send|move|doctor|status`
    cover one invalid input, one missing-dependency case, one boundary
    the operator confirms what only a human can judge — a real message sent
        and received, a signature that verifies, a trust level classified
        correctly against a live mailbox

    # D. SHIP
    bd close the bead                       # per org convention: close before
                                            # push, once local review + verify
                                            # pass and the unit is merge-ready
    push; create the PR (description carries the verification playbook)
    if the PR changes a file that is not demo_exempt:
        comment "demo confirmed: <head sha>"   # once step C passed on that
                                               #   head, with the operator's
                                               #   confirmation only where
                                               #   step C requires it
        # Qodo reviews once, on open (.pr_agent.toml); Bugbot reviews once,
        #   on its own, at open (Cursor dashboard: once per PR) — no
        #   "bugbot run" comment for round 1
    if the operator asked for Copilot: request one review, at open   # never
                                          #   by default, never on push
    schedule a background poll with /loop 3m  # never gh pr checks --watch,
                                              # never foreground sleep loops
    # If the PR is later abandoned or closed unmerged, reopen its bead
    # (bd update <id> --status open) so the backlog never shows unshipped
    # work as done — the one case where a closed bead returns to the funnel.

# Terms, all read from GitHub (Qodo's login is qodo-code-review[bot]):
#   round(pr)    = 1 + the number of "/agentic_review" comments the leader
#                  has posted on the PR
#   trigger(N)   = the PR open for N = 1; the leader's (N-1)th
#                  "/agentic_review" comment for N > 1
#   head(N)      = the PR head commit when trigger(N) happened
#   qodo_in(N)   = `qodo read pr-review-session findings --pr-url <url>`
#                  reports review_session.status "completed" with a
#                  started_at at or after trigger(N) and a commit_sha that
#                  is head(N) or a later commit on the branch; or the
#                  outage fallback ran for round N. A session that started
#                  before trigger(N) — a round-1 session for N = 2 — never
#                  completes round N, and a clean review counts like any
#                  other.
#   bugbot_in(N) = a "Cursor Bugbot" check run on head(N) or a later branch
#                  commit, started at or after trigger(N) — the PR open for
#                  N = 1 (Bugbot's automatic run), round 2's "bugbot run"
#                  comment for N = 2 — and completed with a conclusion
#                  showing the review finished (success or neutral). A run
#                  that failed, timed out, or was cancelled reviewed
#                  nothing, so it does not count; a later push does not
#                  undo a completed run. A clean run posts no review, so
#                  read the check run. Or: 30 minutes have passed since
#                  trigger(N) (or since the re-run comment below, if
#                  later) and bugbot_open is false — the timeout covers an
#                  outage, never a live run.
#   copilot_in   = no Copilot review was requested on the PR, or one was and
#                  (a review by copilot-pull-request-reviewer exists or 30
#                  minutes have passed since the request)
#   pending(pr)  = (not skipped(pr) and (not qodo_in(round(pr))
#                  or not bugbot_in(round(pr)))) or not copilot_in
#                  # a requested Copilot review counts on a skipped
#                  # (bot-authored) PR too
#   finding      = any unresolved review thread, from any author, or any
#                  Medium in Qodo's summary — whenever it arrives, late ones
#                  included
#   answered(f)  = one path per source, never both:
#                  - a finding with a Qodo id (from the CLI read): its
#                    attribution_status is closed (implemented, dismissed,
#                    outdated); close it with `qodo pr-review-session
#                    mark-implemented` or `dismiss` plus the reason and
#                    file:line. A ledger line does not close it.
#                  - any other finding — Bugbot, a security automation,
#                    Copilot, a human, the local outage fallback: fixed by a
#                    commit on the PR head, or a ledger comment gives the
#                    reason and file:line.
#                  A finding repeated from an earlier round keeps its answer.
#   new High     = a High raised by a round-2 review (one that started at
#                  or after trigger(2)) and not raised in an earlier round;
#                  a manual review may re-scan the whole PR and repeat old
#                  findings, and a round-1 High that arrives late is not
#                  new — it takes the normal fix path
#   escalation   = the leader's "escalated:" comment on one new round-2
#                  High, or on a stuck Bugbot run; closed by a later ledger
#                  entry recording the operator's ruling and how it was
#                  applied
#   skipped(pr)  = the PR author is a bot ([github_app] ignore_bot_pr in
#                  .pr_agent.toml); no title or label lets an author skip
#   reviewed_head = the newest commit covered by a Qodo review, the outage
#                  fallback, or the local delta review below; a skipped
#                  (bot-authored) PR has none until the local delta review
#                  has covered its whole diff
#   delta_head   = the newest commit a local delta review has completed on
#   bugbot_open  = a Cursor Bugbot check run on any commit of the PR, not
#                  only the head, whose status is not completed (queued or
#                  in progress), whatever its age, and that has no recorded
#                  "proceed" ruling; only the run ending or a "proceed"
#                  ruling clears it
#   bugbot_stuck = a Bugbot run queued or in progress for more than 60
#                  minutes, with no recorded "proceed" ruling
#   demoed_head  = the commit named in the leader's latest "demo confirmed:"
#                  PR comment, newer than any "demo failed:" comment; the
#                  leader posts it once the verification gate passes on
#                  that commit, with the operator's confirmation only where
#                  the gate requires it (a live-mailbox result a human must
#                  judge)
#   demo_exempt(f) = f is *.md, under docs/, or *_test.go, and is not under
#                  plugin/ — commands and hooks there are behavior even when
#                  Markdown, because Claude Code executes them
#   demo_ok(pr)  = every file the PR changes is demo_exempt, or demoed_head
#                  exists, is the head or an ancestor of it, and every file
#                  changed since it is demo_exempt
# A "ledger comment" is a PR comment that records one finding, its reason,
# and the code reference. "Pre-existing", "intentional", "expected", and
# "by design" are not reasons.

function poll_tick(pr):
    state = current reviews, threads, checks
    N = round(pr)
    if CI failed: fix in this PR
    if not skipped(pr) and not qodo_in(N)
            and 30 minutes have passed since trigger(N)
            and the fallback has not yet run for round N:
        run code_reviewer locally on the diff Qodo was asked to review;
            it completes round N for qodo_in only if it completed — on an
            error, re-run once, then report to the operator and stop;
            its findings have no Qodo id, so they take the ledger path
                                                   # outage fallback
    if a Bugbot run for round N completed without a finished review
            (failure, timeout, or cancelled — a fix push cancels an
            in-progress run) and no re-run was requested for round N
            and bugbot_in(N) is still false:
        comment "bugbot run"               # once; if the re-run fails too or
                                           #   none starts, the 30-minute
                                           #   fallback in bugbot_in applies
    if a bugbot_stuck run has no "bugbot run" comment since it got stuck:
        comment "bugbot run"               # once; the leader cancels nothing
    if a run is still bugbot_stuck 30 minutes after that comment and no
            escalation has been posted for this run:
        post an escalation comment         # once, ever, per run; the operator
                                           #   rules: "proceed" — the run is
                                           #   abandoned, the ledger entry
                                           #   names its check-run id, and
                                           #   bugbot_open ignores it; "wait" —
                                           #   bugbot_open stays true until
                                           #   the run ends, and the operator
                                           #   may rule again later
    for each finding not answered(f) and not under an open escalation:
        if N == 2 and f is a new High:
            post an escalation comment; the operator rules
        else:
            fix it now, or answer it by its source's path (answered(f))
            # never re-litigated; Qodo Lows are dropped by comments_routing;
            # no deferral beads
    for each escalation the operator has ruled on:
        apply the ruling; record it in a ledger comment
    if fixes were made: delegate them to a bare agent; make check; commit
        if local_review(the fix diff) is blocked:
            stop; the operator rules
        push once                          # every fix reaches the PR head
    close each escalated finding the operator ruled on, by its answered(f)
        path — a finding with a Qodo id through the Qodo CLI, because a
        ledger line does not close it: a dismiss ruling closes it (dismiss)
        as soon as the ruling is recorded; a fix ruling closes it
        (mark-implemented) only once its fix commit is on the PR head and
        passed local_review
        # a fix ruling closed earlier would let a stopped fix merge
        #   unfixed: no open escalation, no unanswered finding
    resolve each thread that is answered, one by one — the leader
        resolves, not the agent
    if not skipped(pr) and not pending(pr) and N == 1
            and a High was fixed since trigger(1):
        comment "/agentic_review"; comment "bugbot run"   # opens round 2,
                                                          #   the last
    if not pending(pr) and (the PR head is newer than both reviewed_head
            and delta_head, or skipped(pr) and neither is set):
        run code_reviewer locally on the PR's merge-base..head diff when
            reviewed_head is unset (a bot-authored PR), else on the
            reviewed_head..head delta; its findings are findings like any
            other; set delta_head to the head once the review has completed
    if every finding of the delta review at delta_head is answered(f):
        set reviewed_head to delta_head    # bounded rounds, but no
                                           #   unreviewed merge
    if not demo_ok(pr) and nothing was pushed this tick:
        re-run the verification gate on the head; once it passes, comment
            "demo confirmed: <sha>" naming the head exercised, after the
            operator's confirmation only where the gate requires it; on a
            failure comment "demo
            failed: <sha>" with what was observed, and the failure is a
            finding like any other
    if not pending(pr) and not bugbot_open and no finding is unanswered:
        cadence = 15m                      # only CI or a late thread can
                                           #   change state now
    else: cadence = 3m
    if the poll's interval is not cadence:
        cancel the poll job; schedule /loop <cadence> in its place
    re-read state                          # the gate must see this tick's
                                           #   push and comment
    if merge_gate(state): merge (squash); close_out()

function merge_gate(state) -> bool:
    return CI green on the latest commit
       and not pending(pr)                 # no requested review outstanding
       and not bugbot_open                 # any queued or in-progress run on
                                           #   any commit, unless ruled
                                           #   "proceed": its findings land
                                           #   before the merge, until it
                                           #   ends
       and reviewed_head is the PR head    # a bot-authored PR is covered by
                                           #   the local delta review, never
                                           #   waived
       and every finding is answered
       and zero unresolved review threads
       and round(pr) ≤ 2
       and no escalation is open
       and demo_ok(pr)                     # confirmed on this head, or
                                           #   nothing demo-relevant since
    # A push restarts CI, not review. When this returns true: merge
    # explicitly. No auto-merge. No admin override. Do not ask, do not wait.

function close_out():
    cancel the poll loop
    delete the branch; checkout main; pull
    send the merge recap via beadle-email # to jim@punt-labs.com, every merge,
                                          # unprompted — a permanent record in
                                          # the 8-part recap structure, plus
                                          # the review data block from
                                          # ../.bin/review-metrics.py pr
                                          # punt-labs/beadle <N> --format
                                          # recap, pasted verbatim
    start the next unit immediately       # no stopping to report

function local_review(diff):
    # Local rounds are cheap and bugs die here; the PR gate is kept short.
    for round in 1..5:
        if this is the unit's first full diff or diff is 20 lines or more:
            reviewers = code_reviewer + silent_failure_hunter + `qodo review`
        else:
            reviewers = `qodo review`    # a small fix diff
        if diff touches credentials, secrets, or caller text that reaches
                a file, a log line, or a header:
            reviewers += djb             # whatever the diff's size
        if this is not the unit's first full diff:
            scope: regressions this diff introduces, and High findings
                inside it — nothing outside the diff, no Medium or Low on
                unchanged behaviour
        run reviewers in parallel        # qodo review from the worktree,
                                         #   against the pushed base
        re-run once any reviewer that did not complete (error, outage)
        if a reviewer still has not completed:
            report it to the operator
            return blocked               # a review that never ran is not
                                         #   clean
        findings = every reviewer's findings
        if round == 5 and a finding needs a fix:
            report the open findings to the operator
            return blocked               # the caller stops; see below
        answer each finding: fix it, or dismiss it with the exact finding,
            the specific reason, and file:line
        close each local Qodo finding through the Qodo CLI
            (mark-implemented or dismiss, with its local_review_id) —
            an unclosed one comes back as still_open in every later run
        if no finding was fixed:
            return clean                 # nothing found, or all dismissed
        make check; commit
        diff = this round's fix diff     # every change is reviewed,
                                         #   Low fixes included
    # at most four fix rounds, plus a fifth review that only verifies the
    # fourth round's fixes. No deferral beads. Every caller acts only on
    # clean: on blocked, nothing opens and nothing is pushed until the
    # operator rules and local_review then returns clean.
```

Entry and exit for one PR iteration. `merge_gate` in the pseudocode is the
pre-merge subset of these conditions; `ExitPR` describes the state after
close-out completes.

```text
EnterPR ≙ [ LoopState; unit : ℙ BEAD |
  unit ⊆ claimed
  ∧ rollbackCoherent(unit)                -- reverts together sensibly
  ∧ inThroughputBand(unit)                -- above transaction cost,
                                          --   below reviewer degradation
  ∧ (architectural(unit) ⇒ designRatified(unit)) ]  -- operator has ruled

ExitPR ≙ [ Δ LoopState; pr : PR |
  merged(pr)
  ∧ localReviewClean(pr)                  -- a local round that left nothing
                                          --   to fix, BEFORE the PR opened and
                                          --   before every fix push
  ∧ verified(pr)                          -- driven through its real entry
                                          --   point BEFORE the PR opened
  ∧ ciGreen(head(pr))
  ∧ ¬ pending(pr)                         -- every requested review is in
  ∧ demoOk(pr)                            -- demo_ok held at the merge
  ∧ ¬ bugbotOpen(pr)                      -- bugbot_open was false at the
                                          --   merge
  ∧ reviewedHead(pr) = head(pr)           -- merged code was reviewed, bot-
                                          --   authored PRs included
  ∧ (∀ f : findings(pr) • answered(f))    -- fixed on head, or reasoned
  ∧ round(pr) ≤ 2                         -- round 2 only after a High fix
  ∧ unresolvedThreads(pr) = ∅
  ∧ openEscalations(pr) = ∅               -- each closed by a recorded ruling
  ∧ beadsOf(pr) ⊆ closed′
  ∧ mergeRecapSent(pr) ]
```

### Local review before the PR

Full-diff local review is where issues die cheaply. A local round costs
seconds; a remote review cycle costs minutes to hours. `local_review` (above)
runs `code-reviewer`, `silent-failure-hunter`, and `qodo review` on the unit's
first full diff and on every fix diff of 20 lines or more; a fix diff under 20
lines gets `qodo review` alone. `djb` joins any diff that touches credentials,
secrets, or caller text that reaches a file, a log line, or a header — in
beadle that is `internal/pgp`, `internal/secret`, and the daemon's
command-file loader — whatever the diff's size. Local rounds repeat until a
round leaves nothing to fix: at most four fix rounds plus one review that
verifies the last fixes. Every fix diff goes through the same loop before it
is pushed, including fixes made for PR findings.

From the second round on, the review is scoped to what the diff changed:
regressions it introduces and Highs inside it. Without that boundary a
failure-focused reviewer walks outward into code the diff never touched and
the rounds do not converge. The three reviewers are not substitutes — in the
vox pilot each found defects the other two missed. `qodo review` is the
engine that later reviews the PR, so what it finds locally does not come back
as a PR round.

Every finding is fixed in this PR. A dismissal requires the exact finding, the
specific reason it does not apply, and the code reference. "Pre-existing", "by
design", "intentional", and "expected" are not reasons. Opening a PR with
unresolved local findings is a procedural violation. Nothing is deferred to a
bead.

### Remote review: Qodo and Bugbot, at most two rounds

Remote review is automated-only and runs on already-clean code, so findings
should be few. The full specification is
[`punt-kit/standards/pr-review.md`](https://github.com/punt-labs/punt-kit/blob/main/standards/pr-review.md);
this section names what beadle does with it.

Qodo is the primary remote reviewer (`.pr_agent.toml`). It reviews when the PR
opens and never on push. High findings post inline as threads, and the
`required_review_thread_resolution` ruleset rule makes each one block the
merge. Medium findings appear in the summary. Lows are dropped by
`comments_routing`. Bugbot reviews each PR once, on its own, when the PR opens
(Cursor dashboard setting); a push draws no Bugbot review. Its findings are
handled whenever they arrive.

Round 1 is the review at open. Round 2 — one `/agentic_review` plus one
`bugbot run` — happens only when round 1's fixes included a High, and it is
the last. A new High in round 2 goes to the operator.

Copilot is not a default reviewer. Repo rulesets carry no
`copilot_code_review` rule, so Copilot reviews only when requested: at most
one request per PR, at open, never on push. Its findings are findings like any
other.

This holds only when four settings are in place: `.pr_agent.toml` is on
`main` (Qodo reads it from the default branch); "Review every push" is
Disabled in the Qodo portal; Bugbot is set to review each PR once, at open;
and the ruleset has no `copilot_code_review` rule. If a push still starts a
Bugbot run, its findings are findings like any other.

### The verification gate

`make check` passing means the code compiles and the tests pass. It does not
mean the feature works. Before any PR opens, the feature is driven through its
real entry point — an MCP tool call over `beadle-email serve`, a `beadle-email`
CLI subcommand, `beadle-email doctor` — with the expected outcome written down
first, then compared against actual output. Because beadle talks to a live
mail system, the human-judged part is real: a message actually sent and
received, a PGP signature that actually verifies, a sender actually classified
at the right trust level. That confirmation is the operator's, and it is a
demo, not a diff. Docs-only changes are the one exception — markdownlint plus a
read-through is their verification.

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
        worker designs / codes / tests   # tests lead; TDD for bug fixes
        worker commits locally           # each commit passes make check
        result   = worker submits
        findings = evaluator review      # distinct specialist (delegation.md)
                 + leader verification   # run the change, review agents
                                         # on the mission diff
        if findings: reflect(findings)   # another round, same mission
    while findings remain
    close(mission)
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
dispatch, monitoring, the local review agents (they are the leader's tools,
run on each mission's diff and on the full diff), the verification, and every
git and GitHub operation: creating branches, pushing, opening the PR, driving
remote review, resolving threads, merging, and closing out. The leader does
not write production code. The leader authors only docs (CLAUDE.md, DESIGN.md,
README.md, CHANGELOG.md, this file, memory and plan files).

**The worker owns the work.** The thinking and the code inside its mission —
the design decisions its contract leaves open, the tests, the implementation,
and local commits on the current branch. A worker never creates branches,
never pushes, never opens PRs, and never touches review threads. Putting
workflow operations into a worker's prompt is a contract defect.

**The evaluator** — always a different specialist from the worker, per the
table in `delegation.md` — reviews the worker's result inside the mission
before the leader accepts it.

**Remote review findings are the one delegation that is not a mission.** When
Qodo or Bugbot report on the PR, the leader reads each finding and hands
the mechanical fix to a bare agent, then pushes and resolves the thread
itself.

### The lifecycle

1. **Contract.** The leader writes the mission contract: the problem, the
   invariants, the quality bar, and the commit discipline — one commit per
   logical step, each passing `make check`, never more than thirty minutes of
   work uncommitted. A design mission's contract never prescribes a
   write-set; the specialist decides what to create, split, or extract. For
   crypto or trust-boundary work the contract names the invariant it must not
   break (zero agent authority, isolated keychain, non-expiring keys rejected,
   tamperproof audit log).
2. **Dispatch is two operations.** Creating the mission writes the contract; a
   separate background agent spawn starts the worker. Verify the worker is
   actually running — a contract with no agent behind it is orphaned work.
3. **The worker executes.** Tests lead: a bug fix starts from a failing test
   that reproduces the defect, and the fix is done when it passes; a feature
   ships its tests with the code; the test count never goes down. Work in the
   trust-model, concurrency, or stateful-protocol class gets its z-spec
   model-check before implementation. Every commit passes `make check` — zero
   exceptions, zero suppressions.
4. **The leader monitors by the filesystem, never by git activity.** A worker
   editing files is working, even with zero commits — analysis and reading are
   invisible. Progress is judged by whether the working tree is changing and
   advancing. An empty commit log is never a reason to intervene, commit by
   proxy, or stop the agent. A genuine stall — no file changes over a long
   window and no response to a status message — is the only cause for taking
   over. Monitoring is silent: concrete facts or action, no narration of the
   wait.
5. **Result and evaluation.** The worker submits its result; the evaluator
   reviews it; reflect-and-advance rounds continue until the evaluator
   accepts.
6. **The leader verifies and closes.** Check the result against the contract:
   `make install`, then exercise the change through its real entry point
   (`beadle-email serve` for MCP tools, the CLI subcommands, `doctor`) with
   expected output written first — one invalid input, one missing dependency,
   one boundary. Run the applicable local review agents on the mission diff
   and fix every finding; a dismissal requires the exact finding, the specific
   reason it does not apply, and the code reference. If the result raises a
   design question, it goes to the operator as a concrete decision — with a
   recommendation — before any dependent mission dispatches. Then close the
   mission.

## Formal verification (z-spec)

Some defects are not fixable by writing another test. When a change involves
the **trust model, a lock discipline, a stateful protocol, or the audit-log
state machine**, model-checking finds in one pass what empirical testing
chases across many rounds.

z-spec is **required — not optional — for a change in any of these classes:**

- **Trust-level classification.** The four-level model (`trusted`, `verified`,
  `untrusted`, `unverified`) is a state machine with a safety invariant: a
  message is classified at exactly one level, and no external sender is ever
  classified `trusted`. Encode it and model-check the invariant.
- **Preflight / permission validation.** "Preflight before execute" and "zero
  agent authority" are safety invariants of the form "never X before Y" —
  model-check that no execution path bypasses preflight.
- **Concurrency / interleaving.** Any change where the daemon, a poller, and a
  command runner contend for shared state (the audit log, a mailbox, a config
  file). If two paths can interleave on shared state, model-check it.
- **The recurrence signal (the hard rule).** The MOMENT the *same class* of
  defect surfaces across **two or more** fix/review rounds — stop. Do not open
  a third empirical round. Formalize the state machine and model-check it.

The workflow uses the z-spec plugin skills: `z-spec:code2model` to model the
entity, `z-spec:check` to `fuzz` type-check, `z-spec:test` / probcli
`-model_check` to check every invariant and deadlock over a bounded carrier
(the **merge gate** for the change), then a **fidelity check** — the model must
reproduce the known defect when the fix is removed. Commit the spec
(`docs/<name>.tex`) as a regression artifact and re-run `fuzz` plus the
model-check whenever the modeled code changes. Purely sequential logic and
data-format tweaks do not need z-spec — a table test suffices.

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

- **CHANGELOG**: entries written in the PR branch, before merge, under
  `## [Unreleased]`. Follow [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
  Categories: Added, Changed, Deprecated, Removed, Fixed, Security.
- **README**: update when user-facing behavior changes — new flags, commands,
  defaults, or config.
- **PR/FAQ** (`prfaq.tex`): update when the change shifts product direction or
  validates/invalidates a risk assumption.
- **DESIGN.md**: add an ADR for a design decision with rejected alternatives.

## Invariants

1. **Beads are the single funnel.** Every piece of work is a bead before it is
   anything else; external queues (issues, alerts) drain into it at intake.
2. **A satisfied merge gate means merge now.** The gate is the only path to a
   merge, and nothing waits once it passes. Merge is always explicit — never
   auto-merge, never an admin override.
3. **Do or do not.** A finding is fixed in this PR or dismissed with a
   reason, the moment it arrives. It is never deferred to a bead or a
   follow-up PR. A thread is resolved only once it is answered per
   `answered(f)`, one by one, never in bulk.
4. **A push restarts CI, not review.** Local rounds run until clean (at most
   four fix rounds plus a verifying review) before the PR opens and before
   every fix push. PR rounds are at most two, and the second only after a
   High fix.
5. **Defects flow inward, scope flows outward.** A defect found while a PR is
   open is fixed in that PR. Only genuinely new lines of work become beads.
6. **The operator gates verification and direction, never diffs or
   sequencing.**
7. **Close-out is inside the loop.** The recap email, branch hygiene, and
   starting the next unit are steps of the loop, not afterthoughts.

## Distribution

Static binaries via GitHub Releases. Four platforms: darwin/arm64,
darwin/amd64, linux/arm64, linux/amd64.

```bash
make dist    # Cross-compile all four targets + checksums.txt into dist/
```
