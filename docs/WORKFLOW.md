# Development Workflow

## Development Loop

Implements Phases 2-6 of the org lifecycle (Branch, Implement & Verify, Document, Local Review, Ship) from the workspace CLAUDE.md. The inner loop is one mission (Phase 3-5). The outer loop is one PR (Phase 6).

### Algorithm

```text
inner_loop(mission):
    delegate(mission)
    gate()
    install()
    verify(mission.expected, mission.edge_cases)
    local_review(scope(mission))
    while findings:
        fix(findings)
        gate()
        local_review(scope(mission))
    commit()

outer_loop(feature):
    for mission in feature.missions:
        inner_loop(mission)
    gate()
    local_review(scope(feature))       # cross-mission issues
    while findings:
        fix(findings)
        gate()
        local_review(scope(feature))
    install()
    verify(feature.expected, feature.edge_cases)
    open_pr()
    review_cycle()                     # Copilot + Bugbot, 2-6 rounds
    merge()
```

### Definitions for beadle

```text
delegate(mission):
    # Spawn ethos specialist — see .claude/rules/delegation.md
    # for worker/evaluator table and contract schema.
    Agent(subagent_type=<worker>, run_in_background=true)

gate():
    make check
    # Expands to: go vet, staticcheck, markdownlint, go test -race.
    # Must exit 0. No suppressions. No truncating output.

install():
    make install
    # Builds beadle-email and copies to ~/.local/bin.
    # For MCP: restart Claude Code to pick up the new binary.

verify(expected, edge_cases):
    # 1. Write expected behavior BEFORE running.
    # 2. Drive through real entry point:
    #      beadle-email serve (MCP tools)
    #      beadle-email list/read/send (CLI)
    #      beadle-email doctor (health check)
    # 3. Compare actual output to expected.
    # 4. Cover: one invalid input, one missing-dependency case,
    #    one boundary condition from edge_cases.
    # 5. Ask operator to confirm.
    # Exception: docs-only changes — markdownlint + read-through.

local_review(scope):
    # Always: code-reviewer + silent-failure-hunter
    # If new type/dataclass/Protocol: + type-design-analyzer
    # If doc/comment changes: + comment-analyzer
    # If test changes: + pr-test-analyzer
    # After others clean: + code-simplifier
    # Expect 2-6 agents depending on scope.

scope(unit):
    # Trivial (<=1 file, no new types): 2 agents
    # Single feature: 3-4 agents
    # Cross-cutting: 5-6 agents

review_cycle():
    # See "Code Review" section below.
    # 2-6 rounds. Read every comment. Fix or explain. Re-push.

commit():
    # type(scope): description
    # One logical change. gate() already passed.
```

## Branch Discipline

All code changes go on feature branches. Never commit directly to main. Pushing to main is blocked by branch protection rules.

Pre-PR review: before creating a GitHub PR, run the `code-reviewer` and `silent-failure-hunter` agents in parallel on the diff. Address any issues they find before opening the PR.

Regular branches by default. Use worktrees only when `/who` shows other active sessions that could conflict with your working tree.

```bash
git checkout -b feat/short-description main
```

| Prefix | Use |
|--------|-----|
| `feat/` | New features |
| `fix/` | Bug fixes |
| `refactor/` | Code improvements |
| `docs/` | Documentation only |

## Code Review

Copilot auto-reviews every push via branch ruleset. No manual review request needed.

Every PR takes 2-6 review cycles. Do not assume a clean CI run means the PR is ready. Reviewers (Copilot, Bugbot) post comments minutes after CI completes. Read and address every comment before merging.

1. **Create PR** via `mcp__github__create_pull_request`. Include summary and test plan.
2. **Wait for CI + reviews** — Copilot and Bugbot can take 5-10 minutes after CI. Do not proceed until comments have arrived or the review cycle is confirmed complete.
3. **Read all feedback** using MCP tools:
   - `mcp__github__pull_request_read` with `get_review_comments` — inline comments
   - `mcp__github__pull_request_read` with `get_reviews` — review verdicts
   - `gh pr checks <number>` — verify all checks green
4. **Take every comment seriously.** If a reviewer flags it, fix it. No "pre-existing" or "out of scope" excuses.
5. **Fix, re-push, repeat.** Each push triggers a new review cycle. Run `make check` before each push. After pushing, go back to step 2.
6. **Merge only when the last cycle produces no actionable comments** — all checks green. Use `mcp__github__merge_pull_request` (not `gh pr merge`, which has local side effects).
7. **Post-merge: check for late comments.** Read review comments one final time after merging. Fix in a follow-up PR if needed.

```bash
# After merging via MCP:
git fetch origin main && git checkout origin/main
git checkout -b feat/next-thing
```

## Documentation Discipline

Every PR must update the docs it affects. If a PR changes user-facing behavior and the diff is missing any of these updates, the PR is not ready to merge.

- **CHANGELOG**: Entries written in the PR branch, before merge. Follow [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) format. Add entries under `## [Unreleased]`. Categories: Added, Changed, Deprecated, Removed, Fixed, Security.
- **README**: Update when user-facing behavior changes — new flags, commands, defaults, or config.
- **PR/FAQ**: Update `prfaq.tex` when the change shifts product direction or validates/invalidates a risk assumption.

## Micro-Commits

- One logical change per commit. A single refactor touching 10 files is still one logical change.
- Quality gates pass before every commit.
- Commit message format: `type(scope): description`

| Prefix | Use |
|--------|-----|
| `feat:` | New feature |
| `fix:` | Bug fix |
| `refactor:` | Code change, no behavior change |
| `test:` | Adding or updating tests |
| `docs:` | Documentation |
| `chore:` | Build, dependencies, CI |

## Pre-PR Checklist

- [ ] **CHANGELOG entry** under `## [Unreleased]`
- [ ] **README updated** if user-facing behavior changed
- [ ] **prfaq.tex updated** if product direction shifted
- [ ] **Quality gates pass** — `make check`
- [ ] **install.sh SHA updated** if releasing

## Session Close Protocol

Before ending any session:

```bash
git status                  # Check for uncommitted work
git add <files>             # Stage changes
git commit -m "..."         # Commit
bd sync                     # Sync beads
git push                    # Push to remote
git status                  # Must show "up to date with origin"
```

Work is NOT complete until `git push` succeeds.

## Distribution

Static binaries via GitHub Releases. Four platforms: darwin/arm64, darwin/amd64, linux/arm64, linux/amd64.

```bash
make dist    # Cross-compile all four targets + checksums.txt into dist/
```
