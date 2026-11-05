# Beads (this repo)

Issue tracking via **bd** (beads), pointed at the punt-labs central
Hosted DoltDB instance. There is **no per-repo issue store** — all
beads live in one shared database, distinguished by `issue_prefix` set
in `.beads/metadata.json`.

## Source of truth

- Issues live in central Hosted DoltDB (`punt-labs-beads-1.dbs.hosted.doltdb.com`).
- `.beads/metadata.json` holds the connection mode + this repo's
  `issue_prefix`.
- `.beads/issues.jsonl` and `.beads/interactions.jsonl` are local
  export views. Auto-export is disabled (`export.auto: false`);
  run `bd export` manually if needed. They are **gitignored** and
  not authoritative.
- `.beads/embeddeddolt/`, `backup/`, etc. are unused in server mode and
  gitignored.

## Connection

Connection details (host, port, user, password, TLS) come from the
org-root `.envrc` plus `~/.config/beads/server.json`. Cross-platform
secret retrieval via `security` (macOS Keychain) or `pass` (Linux GPG
password store). See punt-labs/`.envrc` for the canonical setup.

## Daily commands

```bash
bd list               # issues for THIS repo (auto-scoped via directory.labels)
bd ready              # ready work — NOTE: bd 1.0.4 does NOT auto-scope bd ready;
                      #   use: bd ready --label-any repo:beadle
bd create "title"     # creates a new bead with this repo's prefix
bd close <id>         # close an existing bead
```

## bd version

This repo needs `bd` built from the punt-labs fork, branch
`factory-fixes`, at or after punt-labs/beads#8 (commit `769f8a1`).
Build it from the meta-repo with `.bin/install-bd.sh install`, which
clones the fork into `.tmp/beads-fork/` and installs `~/.local/bin/bd`.

Upstream `bd` and older fork builds ignore `linear.pull_label` in
`config.yaml`, or let a bulk pull refresh other repos' linked beads.
Their `bd linear sync --pull` can turn other repos' Linear issues into
`beadle-*` beads. Before pulling, check that `bd version` shows a
`factory-fixes` commit at or after `769f8a1`; if it doesn't, rebuild.

Upstream bd: <https://github.com/gastownhall/beads>
