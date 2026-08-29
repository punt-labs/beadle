# Dependency Management

Disciplined dependency hygiene.

- Pin transitive deps; reproducible builds across machines and time
- Version-selection algorithms: minimum-version vs. latest-compatible
- Auditing the diff: a new dep's transitive footprint is the dep's real cost
- Vendoring vs. lockfiles: when each pays its keep
- Knowing why each dep is in `go.mod` / `pyproject.toml` / `package.json`
