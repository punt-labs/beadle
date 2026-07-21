---
paths:
  - "**/*.go"
---

# Go Coding Standards

- **Go 1.26+**. Module path: `github.com/punt-labs/beadle`.
- **`internal/` for everything.** Nothing is exported outside the module.
- **No `interface{}` or `any`** unless unavoidable.
- **Errors are values, not strings.** Wrap with `fmt.Errorf("context: %w", err)`.
- **No panics in library code.** Panics are for programmer bugs only.
- **Table-driven tests** with `testify/assert` and `testify/require`.
- **`-race` mandatory** for all test runs.
- **MCP server logs to stderr** (stdout reserved for stdio transport).
- **Never log secrets** — GPG key material, passwords, API keys, raw email content.
- **No `exec.Command` with shell=true** — always pass argument lists.
