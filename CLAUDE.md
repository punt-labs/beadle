# beadle

Autonomous agent daemon with cryptographic owner control. GPG-signed instructions, declared permissions, tamperproof audit trail. Runs on the owner's machine as a background daemon. Written in Go.

The shipping component is `beadle-email` — an MCP server for email communication via Proton Bridge with a four-level PGP trust model.

## Identity

You are **Claude Agento** (`claude`), an agent in the Punt Labs org. Your identity is managed by ethos (`ethos show claude`). Beadle is your email system — you read, send, and manage email as `claude@punt-labs.com`.

## No "Pre-existing" Excuse

There is no such thing as a "pre-existing" issue. If you see a problem — in code you wrote, code a reviewer flagged, or code you happen to be reading — you fix it. Do not classify issues as "pre-existing" to justify ignoring them.

## Standards

This project follows [Punt Labs standards](https://github.com/punt-labs/punt-kit). When this CLAUDE.md conflicts with punt-kit standards, this file wins.

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

## Quality Gates

Run before every commit. The Makefile is the source of truth (`make help`).

```bash
make check                             # All gates: lint + docs + test
```

## Scratch Files

Use `.tmp/` for scratch and temporary files — never `/tmp`. `TMPDIR` is set via `.envrc`.

## Mandatory Reading

Source-of-truth documents, `@`-imported so they stay in context. Read them
before writing code. On conflict, `ARCHITECTURE.md` wins for structure and
`WORKFLOW.md` wins for process; [`docs/README.md`](docs/README.md) is the docs
map and conflict-triage guide.

@docs/README.md
@docs/ARCHITECTURE.md
@docs/WORKFLOW.md
@docs/TESTING.md

These org-wide standards from the `punt-kit` sibling repo are the merged source
of truth for how Punt Labs tools are built, `@`-imported so they load at
session start. `go.md` is the Go standard beadle's code answers to; `github.md`
and `workflow.md` are the PR and org-workflow standards. These are cross-repo
(external) imports, so the first load may ask for approval.

@../punt-kit/standards/go.md
@../punt-kit/standards/github.md
@../punt-kit/standards/workflow.md

## Key Documents

- [`docs/README.md`](docs/README.md) — docs map and conflict triage; start here.
- [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) — package map, trust model, invariants.
- [`docs/WORKFLOW.md`](docs/WORKFLOW.md) — the three-loop development workflow.
- [`docs/TESTING.md`](docs/TESTING.md) — test pyramid and GPG/Fastmail test config.
- [`.claude/rules/delegation.md`](.claude/rules/delegation.md) — mission pipelines, worker/evaluator table.
- [CLI standard](https://github.com/punt-labs/punt-kit/blob/main/standards/cli.md) — command-design reference for `beadle-email`.
