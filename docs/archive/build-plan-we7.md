# beadle-we7: Mid-Session Identity Switching

## Context

Beadle operates as a single identity per session — the agent identity from repo-local ethos config (`agent: claude`). When the human user wants to send email as themselves (e.g., `sam@example.com`), there's no way to switch without editing config files. This feature adds a `switch_identity` MCP tool that lets the caller switch between session participants on the fly. The ethos session roster is the source of truth for who's in the session.

Designed in coordination with the ethos agent (tty76/tty83) — roster format and sidecar contract confirmed.

## Approach

**Session-scoped override on the handler struct + session roster reader.**

- Add `identityOverride` field to handler (mutex-guarded, survives across tool calls)
- `resolveIdentityAndConfig` checks override before calling `Resolver.Resolve()`
- New `switch_identity` tool validates handle via `Resolver.ResolveHandle()` then sets override
- New `internal/session` package reads ethos session roster (process tree walk + YAML parse)
- Enhanced `whoami` shows active identity + session participants when available

One new tool, one new package, surgical changes to two existing files.

## Production Changes

### 1. `internal/session/roster.go` (new, ~150 lines)

Session roster reader using ethos sidecar pattern (file reads, no import dependency).

```go
type Roster struct {
    Session      string        `yaml:"session"`
    Started      string        `yaml:"started"`
    Participants []Participant `yaml:"participants"`
}

type Participant struct {
    AgentID string `yaml:"agent_id"`
    Persona string `yaml:"persona,omitempty"`
    Parent  string `yaml:"parent,omitempty"` // empty = human
}

func ReadRoster(ethosDir string) (*Roster, error) // nil,nil if not found
func (r *Roster) HumanParticipants() []Participant
func (r *Roster) AgentParticipants() []Participant
```

Process tree walk (copied from mcp-proxy, ~50 lines): `ps -eo pid=,ppid=,comm=`, walks ancestors until topmost `claude` binary. Cached via `sync.Once`. Returns PID → reads `~/.punt-labs/ethos/sessions/current/<pid>` → session ID → reads `~/.punt-labs/ethos/sessions/<id>.yaml`.

Returns `(nil, nil)` when no session exists (tests, non-Claude environments).

### 2. `internal/identity/resolve.go` (modify, +5 lines)

Export a handle-resolution method:

```go
func (r *Resolver) ResolveHandle(handle string) (*Identity, error) {
    if err := ValidateHandle(handle); err != nil {
        return nil, err
    }
    return r.fromEthos(handle)
}
```

Thin wrapper over existing private `fromEthos(handle)`. No other changes.

### 3. `internal/mcp/tools.go` (modify, ~20 lines)

Handler struct gains override fields:

```go
type handler struct {
    resolver         *identity.Resolver
    logger           *slog.Logger
    dialer           email.Dialer
    ethosDir         string              // for session roster reads
    overrideMu       sync.RWMutex
    identityOverride *identity.Identity  // nil = use resolver
}
```

New `HandlerOption`:

```go
func WithEthosDir(dir string) HandlerOption {
    return func(h *handler) { h.ethosDir = dir }
}
```

`resolveIdentityAndConfig` gains 4-line prefix:

```go
h.overrideMu.RLock()
override := h.identityOverride
h.overrideMu.RUnlock()
if override != nil {
    id = override
} else {
    id, err = h.resolver.Resolve()
    // ...
}
```

`RegisterTools` adds: `s.AddTool(switchIdentityTool(), h.switchIdentity)`

### 4. `internal/mcp/identity_tools.go` (new, ~120 lines)

`switchIdentity` tool:

- Param: `handle` (string, optional)
- Empty handle → clear override, return "identity reset to default"
- Non-empty → `ValidateHandle(handle)` → `h.resolver.ResolveHandle(handle)` → set override → return "switched to \<handle\> (\<email\>)"

`whoami` (moved from tools.go, enhanced):

- Shows active identity (email, source, handle, name, contacts)
- If override active: shows "source: override (switched from \<default\>)"
- If `h.ethosDir != ""`: reads session roster, appends participant table

### 5. `cmd/beadle-email/admin_cmd.go` (modify, 1 line)

Pass ethosDir to RegisterTools:

```go
mcptools.RegisterTools(s, resolver, logger, mcptools.WithEthosDir(ethosDir))
```

## Test Changes

### 6. `internal/session/roster_test.go` (new)

- `TestReadRoster_Valid` — fake file tree with session + roster YAML
- `TestReadRoster_NoSession` — empty dir returns nil, nil
- `TestHumanParticipants` — filters by absent `parent` field
- `TestAgentParticipants` — filters by present `parent` field

### 7. `internal/identity/resolve_test.go` (modify)

- `TestResolveHandle_Valid` — resolves known handle
- `TestResolveHandle_PathTraversal` — rejects `../etc/passwd`
- `TestResolveHandle_Missing` — returns error for nonexistent handle

### 8. `internal/testenv/env.go` (modify)

Add helpers:

- `AddIdentity(handle, name, email string)` — writes ethos identity YAML
- `WriteSessionRoster(participants []session.Participant)` — writes roster YAML + current/\<pid\> sidecar

### 9. `internal/mcp/handler_test.go` (modify)

- `TestHandler_SwitchIdentity_Valid` — switch to human, verify whoami shows new identity
- `TestHandler_SwitchIdentity_Reset` — switch then reset, verify default restored
- `TestHandler_SwitchIdentity_UnknownHandle` — error result, identity unchanged
- `TestHandler_SwitchIdentity_WithMailOps` — switch to human, send email, verify from address
- `TestHandler_Whoami_WithOverride` — shows override source

## Build Sequence

| Phase | What | Files | Gate |
|-------|------|-------|------|
| 1 | Session roster reader | `session/roster.go`, `session/roster_test.go` | `make check` |
| 2 | Resolver.ResolveHandle | `identity/resolve.go`, `identity/resolve_test.go` | `make check` |
| 3 | Handler override + ethosDir | `mcp/tools.go` | `make check` |
| 4 | switch_identity + enhanced whoami | `mcp/identity_tools.go`, `mcp/tools.go` (move whoami) | `make check` |
| 5 | Testenv helpers + handler tests | `testenv/env.go`, `mcp/handler_test.go` | `make check` |
| 6 | Wire ethosDir in serve cmd | `cmd/beadle-email/admin_cmd.go` | `make check` |
| 7 | Docs | `CHANGELOG.md`, `README.md` | `make check` |

## Dependencies

**`switch_identity` requires ethos.** Two ethos components are used:

| Component | Required by | Hard/Soft | What happens without it |
|-----------|------------|-----------|------------------------|
| Identity files (`~/.punt-labs/ethos/identities/*.yaml`) | `switch_identity` | **Hard** | Tool returns error: "ethos identity not found" |
| Session roster (`~/.punt-labs/ethos/sessions/`) | `whoami` participant list | **Soft** | Participant table omitted, tool still works |

Existing MCP tools (list_messages, send_email, etc.) are unaffected — they continue to work with or without ethos, using the existing resolution chain (ethos → default-identity).

The `switch_identity` tool itself does not require the session roster — it validates handles via `ResolveHandle` against identity files. The roster is only used by `whoami` to enumerate available identities. A caller who knows the handle can switch without the roster being present.

## Key Design Decisions

1. **Explicit handles, not roles.** `switch_identity(handle: "sam")` not `switch_identity(role: "user")`. Handles are auditable, testable, and don't assume session structure.

2. **Handler field, not context.** Override lives on `handler` struct with `sync.RWMutex`. mcp-go doesn't expose session context. Field access is the established pattern (see `handler.dialer`).

3. **Nil override = default.** When `identityOverride` is nil, `resolveIdentityAndConfig` falls through to `Resolver.Resolve()`. All existing behavior unchanged.

4. **Session roster is optional.** `ReadRoster` returns `(nil, nil)` when no session exists. `whoami` omits participant table. `switch_identity` works without roster (handle is validated via `ResolveHandle`, not roster membership). This means switch_identity works in tests and non-Claude environments.

5. **Copy process-tree walk, don't import.** mcp-proxy has the implementation but we don't share modules across repos. 50 lines of copied code is preferable to a shared dependency.

6. **Move whoami to identity_tools.go.** Keeps tools.go focused on email operations. Both identity-related tools live together.

## Verification

- `make check` — all existing + new tests pass
- `beadle-email doctor` — still resolves identity correctly
- Manual MCP test: call `switch_identity` with `{"handle": "sam"}`, then `whoami` — should show `sam@example.com`
- Manual MCP test: call `switch_identity` with `{"handle": ""}` to reset, then `whoami` — should show `claude@punt-labs.com`
- Manual MCP test: call `list_messages` after switch — should use switched identity's config

## Files Summary

| Action | File |
|--------|------|
| Create | `internal/session/roster.go` |
| Create | `internal/session/roster_test.go` |
| Create | `internal/mcp/identity_tools.go` |
| Modify | `internal/identity/resolve.go` (+5 lines) |
| Modify | `internal/identity/resolve_test.go` (+3 tests) |
| Modify | `internal/mcp/tools.go` (~20 lines: struct fields, override check, move whoami out) |
| Modify | `internal/testenv/env.go` (+2 helpers) |
| Modify | `internal/mcp/handler_test.go` (+5 tests) |
| Modify | `cmd/beadle-email/admin_cmd.go` (+1 line) |
| Modify | `CHANGELOG.md`, `README.md` |
