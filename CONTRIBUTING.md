# Contributing

## Prerequisites

Go 1.26+. `go.mod` declares `toolchain go1.26.6`; if your installed `go` is
older, `make build` (or any `go` command) downloads the pinned toolchain
automatically.

## Getting Started

```bash
git clone https://github.com/punt-labs/beadle.git
```

```bash
cd beadle
```

```bash
make build
```

Produces the `beadle-email` binary in the repo root. `make install` builds
and copies it to `~/.local/bin`.

## Testing

```bash
make test
```

Runs the full suite with the race detector (`go test -race`). See
[`docs/TESTING.md`](docs/TESTING.md) for the test pyramid and the
GPG ephemeral-keyring conventions integration tests rely on.

## Quality Gates

```bash
make check
```

`make check` runs the full gate: `gofmt -s`, `shellcheck`, `go vet`,
`staticcheck`, `golangci-lint`, `govulncheck`, `markdownlint`, and
`go test -race`. Every commit must pass it before it lands — see `make help`
for the individual targets it composes, and `make lint` (gofmt + go vet +
staticcheck + shellcheck) / `make lint-strict` / `make vulncheck` / `make docs`
/ `make test` to run any one of them alone. `make format` fixes gofmt and
golangci-lint formatting issues in place.

## Branching and Commits

Branch from `main` with a prefix that matches the change:

| Prefix | Use |
|--------|-----|
| `feat/` | New features |
| `fix/` | Bug fixes |
| `refactor/` | Code improvements, no behavior change |
| `docs/` | Documentation only |
| `chore/` | Build, dependencies, CI, config |

Commit messages follow `type(scope): description`, using the prefixes above.

## Submitting a Change

- `make check` passes before every commit, not just before the PR.
- Add or update tests for the package you changed — the test count should
  never go down.
- Add a `CHANGELOG.md` entry under `## [Unreleased]` if the change affects
  user-facing behavior.
- Open a pull request against `main`.

## Cross-Compiling

```bash
make dist
```

Cross-compiles `beadle-email` for darwin/linux, arm64/amd64, into `dist/`.
