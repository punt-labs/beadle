# CLAUDE.md

## No "Pre-existing" Excuse

There is no such thing as a "pre-existing" issue. If you see a problem — in code you wrote, code a reviewer flagged, or code you happen to be reading — you fix it. Do not classify issues as "pre-existing" to justify ignoring them. Do not suggest that something is "outside the scope of this change." If it is broken and you can see it, it is your problem now.

## Project Overview

Autonomous agent daemon with cryptographic owner control. GPG-signed instructions, declared permissions, tamperproof audit trail. Runs on the owner's machine as a background daemon.

Two codebases:

- **Python core** — daemon, pipeline execution, signing, audit log (`punt-beadle`, managed with `uv`)
- **Go email channel** — MCP server for email communication (`beadle-email`, standalone binary)

## Standards

This project follows [Punt Labs standards](https://github.com/punt-labs/punt-kit). When this CLAUDE.md conflicts with punt-kit standards, this file wins (most specific wins).

## Build & Run

### Go email channel

```bash
make build                              # Build beadle-email binary
make check                              # All quality gates (vet, staticcheck, markdownlint, tests)
./beadle-email serve                    # Start MCP server (stdio transport)
./beadle-email version                  # Print version
./beadle-email doctor                   # Check installation health
./beadle-email status                   # Current config summary
```

### Python core (future)

```bash
uv sync --all-extras
uv run beadle --help
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

### Go email channel (`cmd/beadle-email/`)

| Package | Responsibility |
|---------|---------------|
| `cmd/beadle-email/` | CLI entry point: `serve`, `version`, `doctor`, `status` |
| `internal/channel/` | Channel interface — `Message`, `TrustLevel`, shared types |
| `internal/email/` | IMAP client (Proton Bridge), MIME parser, trust classifier, Resend sender, config |
| `internal/pgp/` | GPG signature verification via `gpg` CLI in isolated GNUPGHOME |
| `internal/mcp/` | MCP tool definitions and handlers (7 tools) |
| `internal/secret/` | Credential resolution: OS keychain → file → env var |

### Trust model

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
3. **Secret file** (`~/.config/beadle/<name>`, mode 600)
4. **Environment variable** (`BEADLE_IMAP_PASSWORD`, `BEADLE_RESEND_API_KEY`)

Config file (`~/.config/beadle/email.json`) stores only connection parameters, never secrets.

### Python core (`src/punt_beadle/`)

| Module | Responsibility |
|--------|---------------|
| `types.py` | Domain types: `CommandDocument`, `Pipeline`, `Permission`, `AuditEntry`, `Identity`, `SignedPayload` |
| `identity.py` | Identity management: GPG key generation, isolated `GNUPGHOME`, `beadle.yml` production |
| `signing.py` | GPG signing and verification: `sign()`, `verify()`, wraps `gpg` CLI |
| `audit.py` | Append-only signed audit log: write entries, verify chain integrity |
| `pipeline.py` | Pipeline execution: preflight permission checks, two-level try-catch, stage composition |
| `interpreters/` | Command interpreters: `bash.py`, `claude.py`, `python.py` |
| `daemon.py` | Background daemon: IMAP polling, cron scheduling, health monitoring |
| `cli.py` | Typer CLI: `init`, `sign`, `verify`, `run`, `status`, `log`, `version` |
| `server.py` | FastMCP server: MCP tools mirroring CLI commands |

### Design Invariants

- **Zero agent authority.** Every action requires a GPG-signed instruction from the owner. The daemon has no independent decision-making.
- **Preflight before execute.** All permissions are validated before any command runs. No partial execution.
- **Isolated keychain.** Beadle stores keys in its own `GNUPGHOME`, never touching the user's system GPG keyring.
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

## Python Coding Standards

### Types

- `from __future__ import annotations` in every file.
- Full type annotations on every function signature and return type.
- mypy strict mode and pyright strict mode. Zero errors.
- Never `Any` unless interfacing with untyped libraries. Document why with inline ignores.
- `@dataclass(frozen=True)` for immutable value types.
- Use Protocol classes for abstractions. Never `hasattr()` or duck typing.
- `cast()` in string form for ruff TC006: `cast("list[str]", x)`.

### Exceptions and Error Handling

- Fail fast. Raise exceptions on invalid input. No defensive fallbacks.
- `ValueError` for domain violations. `typer.BadParameter` for CLI user errors.
- Never catch broad `Exception` unless re-raising or at a boundary (CLI entry point, MCP tool handler).
- Security-sensitive operations (signing, verification, key management) must never silently fall back. A failed signature check is a hard stop, not a warning.

### Logging

- `logger = logging.getLogger(__name__)` per module.
- `logging.basicConfig()` configured once in CLI and server entry points.
- `logger.debug()` for pipeline execution details. `logger.info()` for audit log writes.
- MCP server logs to stderr only (stdout reserved for stdio transport).
- Never log GPG key material, passphrases, or raw email content at any level.

### Imports and Style

- All imports at top of file, grouped per PEP 8 (stdlib, third-party, local).
- Double quotes. 88-character line limit. Enforced by ruff.
- No inline imports. No backwards-compatibility shims.

### Prohibited Patterns

- No `hasattr()` — use protocols.
- No mock objects in production code.
- No defensive coding or fallback logic unless explicitly requested.
- No `Any` without a documented reason and inline type-ignore comment.
- No `subprocess.run(shell=True)` — always pass argument lists. Beadle runs user-authored commands through interpreters, never through shell expansion.
- No hardcoded paths to `gpg` — resolve via `shutil.which()` at init time.

## Testing

- **All tests must pass.** If a test is failing, fix it.
- If a test fails, fix it. Do not skip, ignore, or work around it.
- GPG operations in tests use a temporary `GNUPGHOME` (ephemeral keyring per test session).
- Integration tests requiring Proton Bridge or Anthropic API are marked `@pytest.mark.integration`.
- Use `side_effect=lambda` instead of `return_value` for fresh mocks per call.

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

All code changes go on feature branches. Never commit directly to main.

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

### PR Workflow

1. Create branch, make changes, commit
2. Push and create PR. Prefer `mcp__github__create_pull_request` over `gh pr create` where possible.
3. **Watch CI and reviews without blocking your main shell** — do not stop waiting. Run `gh pr checks <number> --watch` in a background task or separate session to block until all checks resolve.
4. **Expect 2-6 review cycles before merging.** Copilot and Bugbot may take 1-3 minutes to post after CI completes. Read feedback using MCP GitHub tools: `mcp__github__pull_request_read` with `get_reviews` and `get_review_comments`.
5. **Take every comment seriously.** There is no such thing as "pre-existing" or "unrelated to this change" — if you can see it, you own it. Fix the issue, re-push, and wait for the next review cycle.
6. **Repeat until the last review cycle is uneventful** — zero new comments, all checks green.
7. **Merge via MCP, not `gh`.** Use `mcp__github__merge_pull_request` (API-only, no local git side effects). `gh pr merge` tries to checkout main locally, which fails inside a worktree.

```bash
# After merging via MCP:
git fetch origin main && git checkout origin/main  # Detached HEAD in worktree
git checkout -b feat/next-thing                     # New branch from latest main
```

### Documentation Discipline

Every PR must update the docs it affects. If a PR changes user-facing behavior and the diff is missing any of these updates, the PR is not ready to merge.

- **CHANGELOG**: Entries are written in the PR branch, before merge — not retroactively on main. Follow [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) format. Add entries under `## [Unreleased]`. Categories: Added, Changed, Deprecated, Removed, Fixed, Security.
- **README**: Update `README.md` when user-facing behavior changes — new flags, commands, defaults, or config.
- **PR/FAQ**: Update `prfaq.tex` when the change shifts product direction or validates/invalidates a risk assumption. Use `/prfaq:feedback` to apply revisions and `/prfaq:meeting-hive` to run autonomous review meetings.

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

### Release Workflow (Go email channel)

1. **Bump version** in `cmd/beadle-email/main.go`
2. **Move `[Unreleased]`** entries in `CHANGELOG.md` to new version section
3. **Run all quality gates**: `make check`
4. **Build locally**: `make dist`
5. **Commit**: `chore: release vX.Y.Z`
6. **Tag**: `git tag vX.Y.Z`
7. **Push**: `git push origin main vX.Y.Z`
8. **GitHub release**: `gh release create vX.Y.Z --title "vX.Y.Z" --notes-file -`

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

- [Python](https://github.com/punt-labs/punt-kit/blob/main/standards/python.md)
- [GitHub](https://github.com/punt-labs/punt-kit/blob/main/standards/github.md)
- [Workflow](https://github.com/punt-labs/punt-kit/blob/main/standards/workflow.md)
- [CLI](https://github.com/punt-labs/punt-kit/blob/main/standards/cli.md)
