# CLAUDE.md

## No "Pre-existing" Excuse

There is no such thing as a "pre-existing" issue. If you see a problem — in code you wrote, code a reviewer flagged, or code you happen to be reading — you fix it. Do not classify issues as "pre-existing" to justify ignoring them. Do not suggest that something is "outside the scope of this change." If it is broken and you can see it, it is your problem now.

## Project Overview

Autonomous agent daemon with cryptographic owner control. GPG-signed instructions, declared permissions, tamperproof audit trail. Runs on the owner's machine as a background daemon. Written in Go.

The first shipping component is `beadle-email` — an MCP server for email communication via Proton Bridge with a four-level PGP trust model.

## Standards

This project follows [Punt Labs standards](https://github.com/punt-labs/punt-kit). When this CLAUDE.md conflicts with punt-kit standards, this file wins (most specific wins).

## Build & Run

```bash
make build                              # Build beadle-email binary
make install                            # Build and install to ~/.local/bin
make check                              # All quality gates (vet, staticcheck, markdownlint, tests)
./beadle-email serve                    # Start MCP server (stdio transport)
./beadle-email version                  # Print version
./beadle-email doctor                   # Check installation health
./beadle-email status                   # Current config summary
```

## Scratch Files

Use `.tmp/` at the project root for scratch and temporary files — never `/tmp`. The `TMPDIR` environment variable is set via `.envrc` so that `tempfile` and subprocesses automatically use it. Contents are gitignored; only `.gitkeep` is tracked.

## Quality Gates

Run before every commit. The Makefile is the source of truth (`make help`).

```bash
make check                             # All gates: lint + docs + test
```

Expands to `make lint docs test`:

- `go vet ./...`
- `staticcheck ./...`
- `npx markdownlint-cli2 "**/*.md"`
- `go test -race -count=1 ./...`

## Architecture

### Package Map

| Package | Responsibility |
|---------|---------------|
| `cmd/beadle-email/` | CLI entry point: `serve`, `version`, `doctor`, `status` |
| `internal/channel/` | Channel interface — `Message`, `TrustLevel`, shared types |
| `internal/email/` | IMAP client (Proton Bridge), MIME parser, trust classifier, SMTP/Resend senders |
| `internal/pgp/` | GPG signature verification and signing via `gpg` CLI in isolated GNUPGHOME |
| `internal/mcp/` | MCP tool definitions and handlers (8 tools) |
| `internal/secret/` | Credential resolution: OS keychain → file → env var |

### Trust Model

Four levels based on sender identity and encryption:

| Level | Sender | Signature | Detection |
|-------|--------|-----------|-----------|
| `trusted` | Proton→Proton | E2E (Proton) | `X-Pm-Content-Encryption: end-to-end` + `X-Pm-Origin: internal` |
| `verified` | External | Valid PGP | `gpg --verify` returns 0 |
| `untrusted` | External | Invalid PGP | `gpg --verify` returns non-zero |
| `unverified` | External | None | No `multipart/signed` |

### Credentials

Resolved at runtime by name through a priority chain:

1. **macOS Keychain** (`security` CLI) — v0.1.0
2. **Linux libsecret** (`secret-tool` CLI) — v0.1.1
3. **Secret file** (`~/.punt-labs/beadle/secrets/<name>`, mode 600)
4. **Environment variable** (`BEADLE_IMAP_PASSWORD`, `BEADLE_RESEND_API_KEY`)

Config file (`~/.punt-labs/beadle/email.json`) stores only connection parameters, never secrets.

### Design Invariants

- **Zero agent authority.** Every action requires a GPG-signed instruction from the owner. The daemon has no independent decision-making.
- **Preflight before execute.** All permissions are validated before any command runs. No partial execution.
- **Isolated keychain.** PGP operations use temporary GNUPGHOME directories, never touching the user's system GPG keyring.
- **Non-expiring keys rejected.** All command-signing keys must have an expiration date. This is a security invariant.
- **Audit log is tamperproof.** Append-only, GPG-signed entries. Only the owner can clear the log.

## Go Coding Standards

- **Go 1.26+**. Module path: `github.com/punt-labs/beadle`.
- **`internal/` for everything.** Nothing is exported outside the module.
- **No `interface{}` or `any`** unless unavoidable.
- **Errors are values, not strings.** Wrap with `fmt.Errorf("context: %w", err)`.
- **No panics in library code.** Panics are for programmer bugs only.
- **Table-driven tests** with `testify/assert` and `testify/require`.
- **`-race` mandatory** for all test runs.
- **MCP server logs to stderr** (stdout reserved for stdio transport).
- **Never log secrets** — GPG key material, passwords, API keys, raw email content.
- **No `exec.Command` with shell=true** — always pass argument lists.

## Testing

### Test Pyramid

| Layer | What | Speed |
|-------|------|-------|
| Unit | Pure functions, table-driven, no I/O | < 5s |
| PGP integration | Ephemeral GPG keypair, sign/verify round-trip | < 5s |
| MCP smoke | Binary handshake, tool registration | < 2s |
| Live (manual) | Real Proton Bridge, iCloud, GPG Mail | Manual |

### Key Rules

- **All tests must pass.** If a test is failing, fix it. Do not skip, ignore, or work around it.
- GPG operations in tests use a temporary GNUPGHOME (ephemeral keyring per test).
- GPG test home directories must use short paths (`/tmp/bg-*`) to avoid the 108-byte Unix socket path limit.
- `-race` is mandatory for all test runs.

## Issue Tracking with Beads

This project uses **beads** (`bd`) for issue tracking.

### When to Use Beads vs TodoWrite

| Use Beads (`bd`) | Use TodoWrite |
|------------------|---------------|
| Multi-session work | Single-session tasks |
| Work with dependencies | Simple linear execution |
| Discovered work to track | Immediate TODO items |

### Essential Commands

```bash
bd ready                    # Show issues ready to work
bd list --status=open       # All open issues
bd show <id>                # View issue details
bd update <id> --status=in_progress   # Claim work
bd close <id>               # Mark complete
bd create --title="..." --type=task   # Create issue
bd sync                     # Sync with git remote
```

## Development Workflow

### Branch Discipline

All code changes go on feature branches. Never commit directly to main. **Pushing to main is blocked** by branch protection rules and will fail.

**Pre-PR review.** Before creating a GitHub PR, run the `code-reviewer` and `silent-failure-hunter` agents in parallel on the diff. Address any issues they find before opening the PR. This catches problems before they reach Copilot/Bugbot, reducing review cycles.

**Use worktrees by default.** Before creating a branch, check `/who` for other active sessions. If other sessions are active, use a worktree to avoid interfering with their working tree. If no other sessions are active, a regular branch is fine.

```bash
# Default: worktree (safe when other sessions are active)
# Use the EnterWorktree tool, then work normally inside the worktree

# Alternative: regular branch (only when /who shows no other sessions)
git checkout -b feat/short-description main
```

**One worktree per agent, many PRs within it.** Branch freely inside a worktree — each PR gets its own branch, not its own worktree.

| Prefix | Use |
|--------|-----|
| `feat/` | New features |
| `fix/` | Bug fixes |
| `refactor/` | Code improvements |
| `docs/` | Documentation only |

### Code Review

Copilot auto-reviews every push via branch ruleset. No manual review request needed.

**Every PR takes 2–6 review cycles.** Do not assume a clean CI run means the PR is ready. Reviewers (Copilot, Bugbot) post comments minutes after CI completes. You must read and address every comment before merging.

1. **Create PR** via `mcp__github__create_pull_request`. Include summary and test plan.
2. **Wait for CI + reviews** — run `sleep 5 && gh pr checks <number> --watch --fail-fast` in background. When CI completes, **keep waiting for reviewer comments**. Copilot and Bugbot can take 5–10 minutes after CI. Do not proceed until comments have arrived or you have confirmed the review cycle is complete.
3. **Read all feedback** using MCP tools:
   - `mcp__github__pull_request_read` with `get_review_comments` — read inline comments (primary)
   - `mcp__github__pull_request_read` with `get_reviews` — check review verdicts
   - `gh pr checks <number>` — verify all checks green
4. **Take every comment seriously.** If a reviewer flags it, fix it. No "pre-existing" or "out of scope" excuses.
5. **Fix, re-push, repeat.** Each push triggers a new review cycle. Run `make check` before each push. After pushing, go back to step 2.
6. **Merge only when the last cycle produces no actionable comments** — all checks green, and the remaining comments (if any) are suggestions you've already addressed or are genuinely not applicable. Use `mcp__github__merge_pull_request` (API-only, no local git side effects). Do not use `gh pr merge` — it has local side effects that break worktrees.
7. **Post-merge: check for late comments.** Read review comments one final time after merging. If new issues were raised, fix them in a follow-up PR immediately.

The entire PR cycle (create → review → fix → merge) should be autonomous. Do not require user intervention to land a clean PR.

```bash
# After merging via MCP:
git fetch origin main && git checkout origin/main  # Detached HEAD in worktree
git checkout -b feat/next-thing                     # New branch from latest main
```

### Documentation Discipline

Every PR must update the docs it affects. If a PR changes user-facing behavior and the diff is missing any of these updates, the PR is not ready to merge.

- **CHANGELOG**: Entries are written in the PR branch, before merge — not retroactively on main. Follow [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) format. Add entries under `## [Unreleased]`. Categories: Added, Changed, Deprecated, Removed, Fixed, Security.
- **README**: Update `README.md` when user-facing behavior changes — new flags, commands, defaults, or config.
- **PR/FAQ**: Update `prfaq.tex` when the change shifts product direction or validates/invalidates a risk assumption.

### Micro-Commits

- One logical change per commit. 1-5 files, under 100 lines.
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

### Release Workflow

1. **Bump version** in `cmd/beadle-email/main.go`
2. **Move `[Unreleased]`** entries in `CHANGELOG.md` to new version section
3. **Run all quality gates**: `make check`
4. **Build locally**: `make dist`
5. **Commit**: `chore: release vX.Y.Z`
6. **Tag**: `git tag vX.Y.Z`
7. **Push**: `git push origin main vX.Y.Z`
8. **GitHub release**: `gh release create vX.Y.Z --title "vX.Y.Z" --notes-file -`

### Distribution

Static binaries via GitHub Releases. Four platforms: darwin/arm64, darwin/amd64, linux/arm64, linux/amd64.

```bash
make dist    # Cross-compile all four targets into dist/
```

### Session Close Protocol

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

## Standards References

- [GitHub](https://github.com/punt-labs/punt-kit/blob/main/standards/github.md)
- [Workflow](https://github.com/punt-labs/punt-kit/blob/main/standards/workflow.md)
- [CLI](https://github.com/punt-labs/punt-kit/blob/main/standards/cli.md)
