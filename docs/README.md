# beadle docs map

What each document is, whether it is current, and which one wins on conflict.
Read this before treating any single doc as the source of truth.

**Conflict rule.** On any disagreement about *how the system is built*,
[`ARCHITECTURE.md`](ARCHITECTURE.md) wins for structure and invariants, and
[`WORKFLOW.md`](WORKFLOW.md) wins for process. A design doc under `docs/` that
disagrees with either is either historical or a proposal — never the blessed
current state. Code that disagrees with `ARCHITECTURE.md` is a bug in one of
them; reconcile, do not silently follow the code.

## Canonical — read before writing code

`@`-imported into `CLAUDE.md` as Mandatory Reading, so they load every
session.

| Doc | What it is |
|-----|-----------|
| [`ARCHITECTURE.md`](ARCHITECTURE.md) | Package map, four-level trust model, credential resolution, design invariants. Source of truth for structure. |
| [`WORKFLOW.md`](WORKFLOW.md) | The three nested loops (backlog → PR → mission), the merge gate, the verification gate, z-spec classes. Source of truth for process. |
| [`TESTING.md`](TESTING.md) | Test pyramid, GPG ephemeral-keyring rules, Fastmail signing-test config. |

## Reference — current, consult as needed

| Doc | What it is |
|-----|-----------|
| [`setup-guide.md`](setup-guide.md) | New-machine setup for `beadle-email` end to end. |
| [`cli-mcp-cmd-analysis.md`](cli-mcp-cmd-analysis.md) | Gap analysis across the CLI / MCP / slash surfaces (baselined at v0.7.0). |
| [`process.md`](process.md) | How the COO runs multi-task epics with ethos pipelines. `WORKFLOW.md` is authoritative for the loop itself; this is the epic-execution narrative around it. |

## Design specs (LaTeX)

Formal design artifacts. The `.tex` is the source; the built `.pdf` is
gitignored. Rebuild and commit the `.tex` when the design changes.

| Doc | What it is |
|-----|-----------|
| [`architecture.tex`](architecture.tex) | Long-form architecture spec behind `ARCHITECTURE.md`. |
| [`beadle-identity.tex`](beadle-identity.tex) | The two-party identity trust model (DES-012). |
| [`audit-beadle.tex`](audit-beadle.tex) | Audit-log design. |
| [`checklist.tex`](checklist.tex) | Release / conformance checklist. |

## Design docs — implemented or historical

Kept for rationale, not as current instructions. Do not infer current
architecture from these; cross-check against `ARCHITECTURE.md`.

| Doc | Status |
|-----|--------|
| [`orchestrator-design.md`](orchestrator-design.md) | Design behind the shipped daemon/orchestrator. |
| [`pipeline-v2-design.md`](pipeline-v2-design.md) | Pipeline v2 design — implemented (see the pipeline-v2 release). |
| [`pipeline-v2.md`](pipeline-v2.md) | Pipeline v2 implementation notes — implemented. |
| [`email-channel-plan.md`](email-channel-plan.md) | Email-channel implementation plan — implemented. |

## Archive

[`archive/`](archive/) holds ephemeral session and build artifacts kept only
for history — per-bead build plans, session-resume files, and point-in-time
status snapshots. Nothing here describes current behavior.
