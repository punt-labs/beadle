# beadle

Autonomous agent daemon with cryptographic owner control. GPG-signed instructions, declared permissions, tamperproof audit trail. Runs on the owner's machine as a background daemon. Written in Go.

The shipping component is `beadle-email` — an MCP server for email communication via Proton Bridge with a four-level PGP trust model.

## What Beadle Is

Beadle gives an **AI agent** its own email address and mailbox — it is not a tool for managing a human's personal inbox. Once an agent has a mailbox, email becomes a channel other parties can use to instruct it, with no live session required. Everything in the trust model exists to answer one question for that channel: who is allowed to direct this agent, and how much authority does a given message actually carry.

Two independent mechanisms decide that. **There is no privileged "owner" role sitting above them** — do not model one when reasoning about this system:

1. **Message verification** — the four-level trust classification (`trusted`/`verified`/`untrusted`/`unverified`, see `docs/ARCHITECTURE.md`'s "Trust Model" section), proving how hard a given message would be to forge. `verifyTrust` itself only classifies; the one place that result is actually **enforced as a hard requirement** is `internal/daemon/handler.go`'s `OnNewMail` call site (`if trust != Verified && trust != Trusted { continue }`) — no mission is created below that bar. Elsewhere, `beadle-email`'s `list_messages`/`read_message`/`check_trust` compute and show the same classification, and `reply_message` still checks only permission before sending — nothing in any of these tools refuses to act because a message sits on a lower trust rung; it's a signal for the agent's own judgment.
2. **Per-contact permission grants (`rwx`)** — set independently per sender, per identity (`internal/contacts.CheckPermission`). This is the check that's **enforced everywhere it applies**: `internal/mcp/tools.go`'s `perm.Read`/`enforceWritePermission` gate reading and replying, `internal/daemon/handler.go`'s `perm.Execute` gates mission creation. Any number of different people or systems can each hold their own combination; nobody is special by default, and a perfectly verified message from an ungranted sender still unlocks nothing.

The one place "owner" shows up in code today (`daemon.json`'s `owner_handle`/`owner_gpg_key_id`, `internal/daemon.VerifySignature`'s `ownerKeyID`) is much narrower than the word implies: it names the single GPG key that must have signed a `beadle-daemon` recipe (command) file before the daemon will load it at all — an **authorizer** for recipe files, not a general position of authority, and unrelated to anyone's `rwx` grants. That naming is wrong and is being corrected (tracked as `beadle-4ul`); until it lands, read every "owner" in `internal/daemon` as "recipe authorizer," nothing more.

**"Is a human watching" is never a security boundary in this system — do not reach for it.** Both `beadle-email` (its own poller, `cmd/beadle-email/admin_cmd.go`) and `beadle-daemon` can run unattended, on a timer, with nobody present. Whether trust verification is enforced or advisory does not depend on attendance either — it depends on which binary's code path is running, per point 1 above. What actually differs between the two binaries is **what a permission grant unlocks**, not who triggered the check:

- `beadle-email`'s `r`/`w` unlocks a message becoming a genuine prompt — the agent reads it and responds using judgment, whether that happened live or via the `/inbox` skill running on a schedule.
- `beadle-daemon`'s `x` unlocks selecting among a fixed set of pre-signed recipes (`internal/daemon.Command` files) — the email never gets to freely instruct anything, it only picks.

That distinction is also the real reason only recipes get the extra authorizer-signature lock: reading a message and applying judgment has a safety net built in — the agent can notice something's off. Running a fixed recipe has none; it just executes exactly as written. The lock exists because of that difference in action type, never because of who's watching.

## Identity

You are **Claude Agento** (`claude`), an agent in the Punt Labs org. Your identity is managed by ethos (`ethos show claude`). Beadle is your email system — you read, send, and manage email as `claude@punt-labs.com`.

**No git submodules in this repo — but a plain vendored copy, per org policy.** The org-wide rule "every project adds `punt-labs/team` as a submodule at `.punt-labs/ethos/`" does not apply here: a `git-subdir` install clones with `--recurse-submodules`, so a submodule's gitlink is fetched into every user's plugin cache regardless of path. Plain committed files carry no such risk — `git-subdir` with `path: "plugin"` only ever materializes `plugin/`'s contents (see below), so `.punt-labs/ethos/` never reaches a plugin consumer either way. Per org policy (`punt-labs/CLAUDE.md` "Team Registry"), `.punt-labs/ethos/` is an inline vendored copy — plain committed files, matching lux, cryptd, public-website, and punt-labs itself. This repo-local copy supplies the `ethos` CLI's agent personas (`.claude/agents/<handle>.md` generation, `ethos identity list`, session roster) under ethos's default layered resolution — repo-local first, global `~/.punt-labs/ethos/` as the fallback tail if a handle is missing here. **`beadle-email` itself is unaffected by any of this vendoring:** its own identity resolution (`internal/identity/resolve.go`) reads the operating identity's YAML exclusively from the global `~/.punt-labs/ethos/identities/<handle>.yaml` — the repo-local copy only ever supplies a *handle* (`.punt-labs/ethos.yaml`'s `agent:` field), never the identity data itself. See `README.md` "Identity" for beadle's resolution chain.

**The plugin payload is `plugin/`, and nothing else.** The shippable surface lives under `plugin/` and the marketplace fetches it with Claude Code's `git-subdir` source — a blobless partial clone plus a cone-mode sparse checkout scoped to `path: "plugin"`. Verified by direct inspection of the installed plugin cache (`~/.claude/plugins/cache/punt-labs/<plugin>/<version>/`): it contains only `plugin/`'s contents, no `.git`, no root-level files, no other top-level directories. Before tracking a new file, ask whether a plugin user has a reason to receive it, and put it in a directory rather than at the root when the answer is no — not because the root leaks (it doesn't), but because a small root stays easy to reason about. `.gitignore` records what has already been trimmed and why. See `docs/ARCHITECTURE.md` for the layout.

## No "Pre-existing" Excuse

There is no such thing as a "pre-existing" issue. If you see a problem — in code you wrote, code a reviewer flagged, or code you happen to be reading — you fix it. Do not classify issues as "pre-existing" to justify ignoring them.

## Standards

This project follows [Punt Labs standards](https://github.com/punt-labs/punt-kit). When this CLAUDE.md conflicts with punt-kit standards, this file wins.

## Build & Run

```bash
make build                              # Build beadle-email binary
make install                            # Build and install to ~/.local/bin
make check                              # All quality gates (golangci-lint, govulncheck, staticcheck, markdownlint, tests)
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

Use `.tmp/` for scratch and temporary files — never `/tmp`. `TMPDIR` is set via `.envrc`. Exception: GPG test home directories use short `/tmp/bg-*` paths to stay under the 108-byte Unix-socket path limit (see [`docs/TESTING.md`](docs/TESTING.md)); the deep `.tmp/` path would overflow it.

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
@.punt-labs/vox/CLAUDE.md
@.punt-labs/ethos/CLAUDE.md
@.punt-labs/beadle/CLAUDE.md
