# beadle-o1w: Docker Image — Implementation Resume

> Resume file for continuing implementation on macOS.
> Bead: beadle-o1w (in_progress). Epic: beadle-9zh.

## What's Done

### Design (3 rounds, security-reviewed)

Design document: `.tmp/missions/results/o1w-docker-design.yaml`
Security evaluation: `.tmp/missions/results/o1w-docker-design-eval.yaml`
Mission: m-2026-04-12-006 (closed, pass)

The design went through 3 review rounds:

1. **Round 1** (adb): Architecture covering 6 design questions
2. **Round 1 eval** (djb): 6 findings — 2 HIGH (GPG key exposure, no MCP auth),
   1 MEDIUM (missing threat), 1 blocking LOW (tmpfs glob)
3. **Round 2** (adb): Incorporated all 6 findings
4. **Round 3** (leader revision): Major transport change — WebSocket + mcp-proxy
   + Docker Sandbox replaces Streamable HTTP
5. **Round 3 eval** (djb): 4 new findings — 1 HIGH (host.docker.internal TLS
   bypass), 1 MEDIUM (mcp-proxy in threat model), 2 LOW (compose token, WS
   message limit). All addressed in the design document.

### Code shipped this session (PRs #136-#138)

+ **PR #136**: `SendResult.Method` generic label + `intParam` bounds guard
  (beadle-j25, beadle-1tk)
+ **PR #137**: Outbound PGP encryption + `gofmt -s` lint gate (beadle-oep)
+ **PR #138**: Gitignore `scheduled_tasks.json`, remove dead `.beadle/` dir
  (beadle-z58)
+ **DES-022 through DES-025**: ADRs for PGP signing, TLS auto-detection,
  inbound decryption, recursive MIME parsing

### Beads closed

+ beadle-j25, beadle-1tk, beadle-oep, beadle-z58, beadle-3to, beadle-ssx

## Architecture Summary

```text
Claude Code <--stdio--> mcp-proxy <--WebSocket--> [Sandbox: beadle-email]
                                                          |
                                                          +-+ IMAP --> Bridge (host)
                                                          +-+ SMTP --> Bridge (host)
```

+ **Transport**: WebSocket (`--transport ws --port 8420`)
+ **Client bridge**: mcp-proxy (punt-labs/mcp-proxy, already installed)
+ **Isolation**: Docker Sandbox (microVM) via `sbx` CLI
+ **Auth**: Sandbox network isolation (no host port). For traditional Docker:
  `MCP_PROXY_TOKEN` on mcp-proxy
+ **GPG keyring**: Entrypoint-copy from `/mnt/gpg-source:ro` to tmpfs `~/.gnupg`
+ **Base image**: `debian:bookworm-slim` + gnupg, digest-pinned, ~80 MB
+ **Process model**: Single process (`beadle-email serve --transport ws`)
+ **Proton Bridge**: Runs on host, sandbox connects via `host.docker.internal`
  with `tls_skip_verify: true` in email.json
+ **Multi-session**: N Claude Code sessions share one daemon via mcp-proxy
  `?session_key=<pid>`
+ **One sandbox per identity**

## What Needs to Be Implemented

### Prerequisites (macOS)

```bash
# Docker Sandbox CLI
brew install docker/tap/sbx
sbx login

# mcp-proxy (if not already installed)
curl -fsSL https://raw.githubusercontent.com/punt-labs/mcp-proxy/bdca3a6/install.sh | sh

# Verify
docker --version    # Docker Desktop required for sbx
sbx --version
mcp-proxy --version
```

### Code changes (~110 lines)

| # | File | Change | Lines |
|---|------|--------|-------|
| 1 | `cmd/beadle-email/admin_cmd.go` | `--transport ws` and `--port 8420` flags on serve | ~15 |
| 2 | `internal/mcp/ws.go` (new) | WebSocket server: upgrade handler, MCP session bridge, `/health` endpoint. `conn.SetReadLimit(16MB)`. | ~60 |
| 3 | `cmd/beadle-email/health_cmd.go` (new) | `beadle-email health --port <port>` for HEALTHCHECK | ~20 |
| 4 | `internal/email/config.go` | `TLSSkipVerify bool` field in Config | ~3 |
| 5 | `internal/email/imap.go` + `smtp.go` | Honor `cfg.TLSSkipVerify` in TLS config | ~6 |
| 6 | `Makefile` | `docker` and `docker-push` targets | ~10 |

### New files (not Go)

| File | Purpose |
|------|---------|
| `Dockerfile` | Multi-stage build (golang builder + debian-slim runtime + gnupg) |
| `entrypoint.sh` | Copies GPG keyring from `/mnt/gpg-source:ro` to tmpfs `~/.gnupg` |
| `docker-compose.yml` | Reference compose for traditional Docker deployment |
| `.dockerignore` | Exclude `.tmp/`, `.git/`, `dist/`, etc. |

### Dependencies

+ `go.mod`: add `github.com/gorilla/websocket` (or `nhooyr.io/websocket`)

### Documentation

+ DESIGN.md: DES-026 (Docker architecture ADR)
+ CHANGELOG.md: entries under [Unreleased]
+ README.md: Docker section with sandbox and compose instructions

### Tests

+ WebSocket transport: connect, send tool call, receive response
+ Health endpoint: HTTP GET returns 200
+ `tls_skip_verify` config: verify TLS config honors the field
+ Dockerfile: `docker build` succeeds, `docker run` + health check passes

## Threat Model (8 threats)

1. Secret exposure via `docker inspect` (LOW)
2. GPG private key — tmpfs only, rated HIGH residual
3. Secret file permission bypass (LOW)
4. Container escape via gpg subprocess (LOW)
5. Network exposure — sandbox eliminates CSRF/rebinding (LOW sandbox, MEDIUM traditional)
6. mcp-proxy as trusted computing base (LOW)
7. Image supply chain — digest-pinned (LOW)
8. Persistent compromise via writable data volume (MEDIUM)

## Key Design Decisions

+ **`tls_skip_verify` over `isLoopback()` extension**: `host.docker.internal`
  resolves via DNS — extending `isLoopback()` to match it would let an attacker
  who controls `/etc/hosts` redirect IMAP/SMTP and capture passwords. Explicit
  config field instead.
+ **WebSocket over Streamable HTTP**: Bidirectional (server push for inbox
  notifications), session-oriented, mcp-proxy speaks it natively, matches
  quarry's pattern.
+ **Docker Sandbox over traditional container**: Hypervisor isolation, no host
  port, eliminates auth middleware. Traditional Docker as fallback with
  `MCP_PROXY_TOKEN`.
+ **No custom auth code in beadle**: Sandbox isolation + mcp-proxy's existing
  token auth handle it. Zero auth middleware to write or maintain.

## Registration (after build)

```bash
# Sandbox deployment
sbx run claude   # inside sandbox, beadle-email serve --transport ws

# Traditional Docker deployment
docker compose up -d
claude mcp add -s user beadle-email mcp-proxy ws://localhost:8420/mcp

# With mcp-proxy token (non-sandbox)
MCP_PROXY_TOKEN=<token> claude mcp add -s user beadle-email \
  mcp-proxy ws://localhost:8420/mcp
```

## Open Items

+ Docker Sandbox (`sbx`) is experimental and macOS/Windows only. Linux users
  use traditional Docker + compose + `MCP_PROXY_TOKEN`.
+ Proton Bridge sidecar container is documented but not recommended. Host
  Bridge is the primary topology.
+ Per-repo message filtering via `session_key` is future work (not in o1w scope).
