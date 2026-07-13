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

@docs/WORKFLOW.md
@docs/ARCHITECTURE.md
@docs/TESTING.md

## Standards References

- [GitHub](https://github.com/punt-labs/punt-kit/blob/main/standards/github.md)
- [Workflow](https://github.com/punt-labs/punt-kit/blob/main/standards/workflow.md)
- [CLI](https://github.com/punt-labs/punt-kit/blob/main/standards/cli.md)
