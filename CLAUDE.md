# CLAUDE.md

## Project Overview

Autonomous agent daemon with cryptographic owner control. GPG-signed instructions, declared permissions, tamperproof audit trail. Runs on the owner's machine as a background daemon.

- **Package**: `punt-beadle`
- **CLI**: `beadle`
- **MCP server**: `beadle-server`
- **Python**: 3.13+, managed with `uv`

## Standards

This project follows [Punt Labs standards](https://github.com/punt-labs/punt-kit). When this CLAUDE.md conflicts with punt-kit standards, this file wins (most specific wins).

## Build & Run

```bash
# Install with dev dependencies
uv sync --all-extras

# CLI
uv run beadle --help
uv run beadle init
uv run beadle run my-task.md

# MCP server (stdio transport)
uv run beadle-server
```

## Scratch Files

Use `.tmp/` at the project root for scratch and temporary files — never `/tmp`. The `TMPDIR` environment variable is set via `.envrc` so that `tempfile` and subprocesses automatically use it. Contents are gitignored; only `.gitkeep` is tracked.

## Quality Gates

Run after every code change. All must pass with zero violations.

```bash
uv run ruff check src/ tests/         # Lint
uv run ruff format --check src/ tests/ # Format check
uv run mypy src/ tests/                # Type check (strict)
uv run pyright src/ tests/             # Type check (strict)
uv run pytest tests/ -v                # All tests pass
```

Build validation:

```bash
uv build
uvx twine check dist/*
```

## Architecture

Module structure under `src/punt_beadle/`:

| Module | Responsibility |
|--------|---------------|
| `types.py` | Domain types: `CommandDocument`, `Pipeline`, `Permission`, `AuditEntry`, `Identity`, `SignedPayload` |
| `identity.py` | Identity management: GPG key generation, isolated `GNUPGHOME`, `beadle.yml` production |
| `signing.py` | GPG signing and verification: `sign()`, `verify()`, wraps `gpg` CLI |
| `audit.py` | Append-only signed audit log: write entries, verify chain integrity |
| `pipeline.py` | Pipeline execution: preflight permission checks, two-level try-catch, stage composition |
| `interpreters/` | Command interpreters: `bash.py`, `claude.py`, `python.py` |
| `daemon.py` | Background daemon: IMAP polling, cron scheduling, health monitoring |
| `email.py` | Proton Bridge integration: IMAP/SMTP client, GPG envelope handling |
| `cli.py` | Typer CLI: `init`, `sign`, `verify`, `run`, `status`, `log`, `version` |
| `server.py` | FastMCP server: MCP tools mirroring CLI commands |

### Design Invariants

- **Zero agent authority.** Every action requires a GPG-signed instruction from the owner. The daemon has no independent decision-making.
- **Preflight before execute.** All permissions are validated before any command runs. No partial execution.
- **Isolated keychain.** Beadle stores keys in its own `GNUPGHOME`, never touching the user's system GPG keyring.
- **Non-expiring keys rejected.** All command-signing keys must have an expiration date. This is a security invariant.
- **Audit log is tamperproof.** Append-only, GPG-signed entries. Only the owner can clear the log.

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

- **All tests must pass.** No exceptions for "pre-existing failures."
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
2. Push and create PR: `gh pr create --title "type: description" --body "..."`
3. **Block until CI and Copilot finish** — do not proceed until these complete:

```bash
gh pr checks <number> --watch          # BLOCKING: polls until all checks resolve
gh pr view <number> --comments         # Read Copilot feedback — address before merging
```

1. Address Copilot feedback if any
2. **Merge via MCP, not `gh`.** Use `mcp__github__merge_pull_request` (API-only, no local git side effects). `gh pr merge` tries to checkout main locally, which fails inside a worktree.

```bash
# After merging via MCP:
git fetch origin main && git checkout origin/main  # Detached HEAD in worktree
git checkout -b feat/next-thing                     # New branch from latest main
```

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

1. **Bump version** in `pyproject.toml` and `src/punt_beadle/__init__.py`
2. **Move `[Unreleased]`** entries in `CHANGELOG.md` to new version section with date
3. **Run all quality gates** — ruff, mypy, pyright, pytest
4. **Commit**: `chore: release vX.Y.Z`
5. **Build locally**: `rm -rf dist/ && uv build && uvx twine check dist/*` (validation only — do NOT upload)
6. **Tag**: `git tag vX.Y.Z`
7. **Push**: `git push origin main vX.Y.Z` (triggers GH Actions release workflow)
8. **Wait for GH Actions**: `gh run watch` — workflow builds, publishes to TestPyPI, verifies install, then publishes to PyPI
9. **GitHub release**: `gh release create vX.Y.Z --title "vX.Y.Z" --notes-file -` (use CHANGELOG entry)
10. **Verify**: `uv tool install --force --refresh punt-beadle==X.Y.Z && beadle version`
11. **Restore editable**: `uv tool install --force --editable .`

A release is not complete until all steps are done. PyPI publishing is owned by GH Actions — never upload manually.

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

## PR/FAQ Document

The `prfaq.tex` and `prfaq.bib` files contain the Working Backwards PR/FAQ document for Beadle at hypothesis stage. Use `/prfaq:feedback` to apply revisions and `/prfaq:meeting-hive` to run autonomous review meetings.

## Standards References

- [Python](https://github.com/punt-labs/punt-kit/blob/main/standards/python.md)
- [GitHub](https://github.com/punt-labs/punt-kit/blob/main/standards/github.md)
- [Workflow](https://github.com/punt-labs/punt-kit/blob/main/standards/workflow.md)
- [CLI](https://github.com/punt-labs/punt-kit/blob/main/standards/cli.md)
