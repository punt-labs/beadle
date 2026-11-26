# REVIEW.md

Instructions for code reviewers of this repository. Qodo loads this file
on every review. Any other reviewer, human or automated, that reads the
repository should read this before raising a finding.

beadle gives an AI agent its own mailbox. `beadle-email` reads, sends and
classifies mail; `beadle-daemon` turns verified mail into pre-signed
missions. The product is its trust model, so findings about that model are
always welcome.

## What we want flagged

- Logic errors a real caller of the changed code can reach.
- Trust-model defects: a message classified at the wrong trust level, an
  external sender classified `trusted`, a signature check that can be
  skipped, an expired or non-expiring signing key accepted.
- Per-contact `rwx` grant defects: a read, reply or mission that proceeds
  without the grant `internal/contacts.CheckPermission` requires.
- Daemon defects: a mission created below `verified`, a command file loaded
  without the authorizer's signature, email-derived text written into a
  tracked mission contract.
- Credentials or mail content leaking into logs, telemetry, errors or
  output.
- Errors that are swallowed or logged without the caller ever finding out.
- Resource leaks and races in the IMAP client, the poller and the daemon.
- Changed behavior with no test, or a test that cannot fail.

## Out of scope: Claude Code permission rules are a safety net

`.claude/settings.json` holds Claude Code permission rules: the `allow`,
`ask` and `deny` lists, and hooks. They are a safety net against an agent's
honest mistakes. They are not a security boundary, and this repository does
not rely on them as one. Containment comes from agent-image, the container
the agents run in. Enforcement inside beadle comes from its own code: trust
classification, per-contact `rwx` grants and command-file signatures.

Do not raise findings that treat those rules as security controls:

- A rule is "too broad", "too permissive" or "allows a dangerous command".
- An `allow` rule could be used to bypass a `deny` or `ask` rule.
- A command is missing from the `deny` or `ask` list.
- A hook can be evaded, or does not cover some spelling of a command.

A rule that breaks a working command, or a hook that fails on valid input,
is still a bug. Report it as one, not as a security finding.

This does not cover `internal/permissions`, the Go code that writes
beadle's own rules. Logic errors there are in scope like any other code.

## Out of scope: what a Punt Labs tool writes belongs to that tool

Punt Labs tools configure themselves in each repository that uses them.
`biff enable`, `vox enable`, `ethos enable`, `z-spec enable` and the rest
each write their own entries and files here. This repository is the tool's
user, not its author. A hand edit here is overwritten the next time the
tool runs.

Do not ask this repository to change any of the following. If one looks
wrong, the defect is in the tool, and the finding belongs in the tool's own
repository under `punt-labs/`:

- A tool's permission rules in `.claude/settings.json`: which of its
  commands are allowed, which prompt, and how each rule is spelled.
- Generated agent files under `.claude/agents/`, including their hook
  commands. `ethos` writes them.
- Deposited guides and markers under `.punt-labs/<tool>/`, and the
  `@`-import lines for them in `CLAUDE.md`.
- Workflow files a tool writes, such as
  `.github/workflows/biff-notify.yml` from `biff enable`, including
  whether the tool pins its own version there.

These files are committed exactly as the tool wrote them. A problem with
one is a bug for the tool. It does not block this repository from using
the tool.

beadle's own rules, written by `beadle-email enable`, are the one exception:
this is the repository that owns them.

## Also out of scope

- Anything CI enforces: gofmt, gofumpt, goimports, go vet, staticcheck,
  golangci-lint, govulncheck, shellcheck, markdownlint, `go test -race`.
- Lines the pull request did not change.
- Hypothetical environments: exotic environment variables, control bytes in
  paths, tests run as root, platforms other than darwin and linux on arm64
  and amd64.
- Suppressions (`//nolint`, `_ =` to discard an error). Never suggest them.
- PR titles, descriptions, commit messages and CHANGELOG wording.
- Agent process rules from `CLAUDE.md`, `AGENTS.md` and `docs/WORKFLOW.md`.

## Severity

- Important: trust-model, grant and signature defects, leaked credentials
  or mail content, crashes, leaks and races.
- Nit: everything else. At most five per pull request.
