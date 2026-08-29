# Contributing

```bash
make build
```

```bash
make check
```

`make check` runs the full quality gate: `gofmt -s`, `shellcheck`, `go vet`,
`staticcheck`, `golangci-lint`, `govulncheck`, `markdownlint`, and
`go test -race`. Every commit must pass it before it lands — see `make help`
for the individual targets it composes, and `make lint` (gofmt + go vet +
staticcheck + shellcheck) / `make lint-strict` / `make vulncheck` / `make docs`
/ `make test` to run any one of them alone. `make format` fixes gofmt and
golangci-lint formatting issues in place.

```bash
make dist
```

Cross-compiles `beadle-email` for darwin/linux, arm64/amd64, into `dist/`.
