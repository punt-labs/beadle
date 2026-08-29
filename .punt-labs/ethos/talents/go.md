# Go

Idiomatic Go implementation.

- Simplicity, clarity, generality — Kernighan / Pike discipline
- Errors are values; wrap with `fmt.Errorf("context: %w", err)`
- Goroutines + channels as concurrency primitives; race detection mandatory
- Table-driven tests with testify; no test helpers that hide the logic
- `internal/` for everything; nothing exported without a stated reason
