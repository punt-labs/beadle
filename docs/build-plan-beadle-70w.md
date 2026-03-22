# beadle-70w: Integration Test Layer

## Context

Beadle's test suite covers individual functions well (contacts, permissions, PGP, MIME parsing) but has zero integration coverage. The MCP package is at 14.8% (only permission helpers tested), the email package is at 34% (composition/parsing only — no IMAP/SMTP). Every tool handler, every IMAP operation, and every SMTP send path is untested. The "live (manual)" test layer requires a running Proton Bridge and manual verification. This bead replaces that with automated integration tests using in-process mail servers.

## Approach

**One small production change + three test packages.**

Extract a `Dialer` interface at the single seam where MCP handlers create IMAP connections (`withClient` in tools.go). Use the already-vendored `go-imap/v2/imapserver` package and add `go-smtp` for in-process test servers. No mocking framework — real protocol behavior.

## Production Changes

### 1. `internal/email/dialer.go` (new, ~20 lines)

```go
type Dialer interface {
    Dial(cfg *Config, logger *slog.Logger) (*Client, error)
}

type DefaultDialer struct{}
func (DefaultDialer) Dial(cfg *Config, logger *slog.Logger) (*Client, error) {
    return Dial(cfg, logger)
}
```

### 2. `internal/mcp/tools.go` (modify, ~15 lines changed)

- Add `dialer email.Dialer` field to `handler` struct
- Add `HandlerOption` type + `WithDialer(d email.Dialer) HandlerOption`
- `RegisterTools` gains variadic `...HandlerOption`, defaults dialer to `email.DefaultDialer{}`
- `withClient` calls `h.dialer.Dial(cfg, h.logger)` instead of `email.Dial(cfg, h.logger)`

Production callers (`admin_cmd.go`) are unchanged — default dialer is the existing behavior.

## Test Infrastructure

### 3. `internal/testserver/` (new package, ~400 lines)

In-process IMAP and SMTP servers for tests. Shared by both `internal/email` and `internal/mcp` test packages.

**`imap.go`** — Memory-backed IMAP server

- `MemMessage{UID, Flags, Raw}`, `MemMailbox{name, messages, uidNext, mu}`
- `MemBackend` implementing `imapserver.Session` interface
- Required ops: Login, Select, List, Status, Fetch, Search, Store, Copy, Expunge, Idle, Append
- Stubbed ops return `imapserver.ErrRequestFailed`
- TLS with self-signed cert (required — `Dial()` always calls `NewStartTLS`)
- `NewIMAPServer(t, user, pass) (*IMAPServer, string)` — starts on `:0`, returns addr, t.Cleanup stops it

**`smtp.go`** — Memory-backed SMTP server

- `SentMessage{From, To, Raw}`, `MemSMTPBackend`, `MemSMTPSession`
- Implements `smtp.Backend` + `smtp.Session` from `go-smtp`
- `NewSMTPServer(t) (*SMTPServer, string)` — starts on `:0`, `AllowInsecureAuth: true`

**`fixture.go`** — Combined test fixture

- `Fixture{Config, IMAP, SMTP}`
- `NewFixture(t)` — starts both servers, creates `email.Config` pointing at them, sets `BEADLE_IMAP_PASSWORD` via `t.Setenv`
- `AddMessage(folder, from, subject, body) uint32` — seeds IMAP mailbox, returns UID
- `SentMessages() []SentMessage` — what SMTP received

### 4. `internal/testenv/env.go` (new package, ~80 lines)

Creates a complete identity environment using temp dirs.

- `Env{EthosDir, BeadleDir, RepoDir, Resolver, Identity}`
- `New(t, email)` — writes ethos identity YAML + active file, creates Resolver
- `AddContact(name, addr, permissions)` — writes contacts.json in identity dir
- `WriteConfig(cfg)` — writes email.json to identity dir

## Test Files

### Level 1: MCP Smoke — `internal/mcp/smoke_test.go` (no build tag)

Uses `mcp-go`'s `server.NewStdioServer` + `io.Pipe()` — no subprocess, no mail server.

| Test | What it verifies |
|------|-----------------|
| `TestMCPSmoke_Initialize` | JSON-RPC initialize response has server name + version |
| `TestMCPSmoke_ToolRegistration` | `tools/list` returns all 15 tool names |
| `TestMCPSmoke_IdentityError` | Tool call with no identity configured returns error (not panic) |

### Level 2a: IMAP/SMTP Integration — `internal/email/integration_test.go` (build tag: `integration`)

Uses `testserver.NewFixture(t)` directly against `email.Client`.

| Test | What it verifies |
|------|-----------------|
| `TestDial_Connect` | `Dial()` connects to in-process IMAP server |
| `TestListFolders` | Returns seeded folders |
| `TestListMessages_Basic` | Seed 3, list returns 3 with correct subjects |
| `TestListMessages_UnreadOnly` | Seed 2 read + 1 unread, returns 1 |
| `TestFetchMessage` | Full message retrieval (from, subject, body) |
| `TestMoveMessage` | Move from INBOX to Archive, verify source empty |
| `TestSMTPSend` | `SMTPSend()` delivers to in-process SMTP, verify via `SentMessages()` |
| `TestSMTPAvailable` | Returns true when server up, false when down |
| `TestTrySendChain_SMTP` | Chain picks SMTP when available |
| `TestTrySendChain_Fallback` | Chain falls to Resend when SMTP down (httptest mock) |

### Level 2b: MCP Handler Tests — `internal/mcp/handler_test.go` (no build tag)

Uses `testenv.New(t)` + `testserver.NewFixture(t)` + `s.HandleMessage()` directly.

| Test | What it verifies |
|------|-----------------|
| `TestHandler_ListFolders` | Tool returns folder list |
| `TestHandler_ListMessages` | Seed 2 messages, tool returns both |
| `TestHandler_ReadMessage_Permitted` | Contact with `r` perm → body returned |
| `TestHandler_ReadMessage_Denied` | Unknown sender → permission error |
| `TestHandler_SendEmail_OK` | Contact with `w` perm → message in SMTP |
| `TestHandler_SendEmail_Denied` | No `w` perm → error |
| `TestHandler_MoveMessage` | Seed + move → verify IMAP state |
| `TestHandler_Contacts_CRUD` | add → find → list → remove sequence |
| `TestHandler_Whoami` | Returns identity email, handle, source |

## Dependency Changes

- **Add**: `github.com/emersion/go-smtp` (same vendor as go-imap, SMTP server for tests)
- **Already present**: `github.com/emersion/go-imap/v2` (includes `imapserver` sub-package)

## Build Sequence

| Phase | What | Files | Gate |
|-------|------|-------|------|
| 1 | Dialer interface extraction | `email/dialer.go`, `mcp/tools.go` | `make check` |
| 2 | In-process test servers | `testserver/{imap,smtp,fixture}.go` | `make check` |
| 3 | Test environment helper | `testenv/env.go` | `make check` |
| 4 | MCP smoke tests | `mcp/smoke_test.go` | `make check` |
| 5 | IMAP/SMTP integration tests | `email/integration_test.go` | `make check` + `make test-integration` |
| 6 | MCP handler tests | `mcp/handler_test.go` | `make check`, verify mcp >60% |
| 7 | Makefile + docs | `Makefile`, `CHANGELOG.md`, `CLAUDE.md` | `make check` |

## Key Design Decisions

1. **Interface at one seam only** — `Dialer` in `withClient`. Not extracting interfaces for SMTP or Resend — those are tested via the real send chain hitting in-process servers.

2. **No build tag for MCP tests** — handler tests use in-process servers with no external deps. They run in `make check` for CI visibility. Only raw IMAP/SMTP tests (Level 2a) get the `integration` tag since they test `email.Client` directly.

3. **`internal/testserver/`** not `_test.go` — both `email` and `mcp` packages import it. Must be a real package, but `internal/` keeps it module-private.

4. **TLS required** — `Dial()` always calls `NewStartTLS`. The test IMAP server must present a self-signed cert. Use `tls.X509KeyPair` generated at test time.

5. **Secrets via env** — `t.Setenv("BEADLE_IMAP_PASSWORD", "testpass")` so `cfg.IMAPPassword()` resolves without keychain.

6. **Level 3 (subprocess E2E) deferred** — L1 (smoke) covers MCP framing, L2b covers handler logic with real servers. A subprocess test adds process lifecycle complexity for marginal coverage gain. Revisit if CI evidence shows gaps.

## Verification

- `make check` — all existing + new non-tagged tests pass
- `make test-integration` — new target, runs `go test -race -count=1 -tags=integration ./...`
- `go tool cover -func=... | grep internal/mcp` — verify >60%
- `go tool cover -func=... | grep internal/email` — verify >60%
- Manual: `beadle-email doctor` still works (production paths unchanged)

## Files Summary

| Action | File |
|--------|------|
| Create | `internal/email/dialer.go` |
| Create | `internal/testserver/imap.go` |
| Create | `internal/testserver/smtp.go` |
| Create | `internal/testserver/fixture.go` |
| Create | `internal/testenv/env.go` |
| Create | `internal/mcp/smoke_test.go` |
| Create | `internal/mcp/handler_test.go` |
| Create | `internal/email/integration_test.go` |
| Modify | `internal/mcp/tools.go` (~15 lines) |
| Modify | `go.mod` (add go-smtp) |
| Modify | `Makefile` (add test-integration target) |
| Modify | `CHANGELOG.md` |
| Modify | `CLAUDE.md` (update test pyramid) |
