# Beadle Orchestrator Design

> Beadle as autonomous agent daemon with ethos missions as the control plane.
> ADR: DES-027 in DESIGN.md.

## Architecture

```text
beadle-daemon (Go, always-on)
    │
    ├── Poller ──── IMAP ──── IMAP server (Proton Bridge, Fastmail, ...)
    │   (detect new mail, same code as MCP server poller)
    │
    ├── x-bit Authorization Gate
    │   (Phase 1: x-bit → instruction, no classification needed)
    │   (Phase 2 future: LLM classification within x-bit contacts)
    │
    ├── Mission Manager
    │   ├── ethos mission create --file contract.yaml
    │   ├── ethos mission show/results/close
    │   └── pipeline state (serial queue, future: semaphore)
    │
    └── Worker Spawner
        └── exec.Command("claude", "-p", "--bare", ...)
            ├── --mcp-config (per-mission tool stack)
            ├── --append-system-prompt-file (mission prompt)
            ├── --output-format json (result capture)
            ├── --max-turns 50 --max-budget-usd 5.00
            └── --permission-mode auto
```

## The x Bit: Mission Authorization

DES-012 defines per-contact permissions as `rwx`:

| Bit | Meaning | Enforcement |
|-----|---------|-------------|
| `r` | Read and surface messages | `list_messages`, `read_message` |
| `w` | Compose and send replies | `send_email` |
| `x` | Trigger autonomous missions | Daemon, before `ethos mission create` |

**Enforcement chain:**

1. Poller detects new mail (unseen > prev)
2. Daemon reads email body
3. Daemon checks sender's x-bit for active identity
4. x granted → create ethos mission with email as trigger
5. x denied → skip (attended mode handles via `/inbox`)

Phase 1: all x-bit emails are instructions. The x-bit IS the authorization.
If a contact has execute permission and sends a message, the daemon acts.

## Worker Spawn Contract

The daemon spawns Claude Code in print mode. Print mode is non-interactive,
returns structured JSON, and exits when done.

### Invocation

```bash
claude -p \
  --bare \
  --mcp-config /path/to/mission-mcp.json \
  --append-system-prompt-file /path/to/mission-prompt.md \
  --output-format json \
  --max-turns 50 \
  --max-budget-usd 5.00 \
  --permission-mode auto \
  --allowedTools "Bash,Read,Edit,Write,Glob,Grep,Agent" \
  "Execute mission <id>. Read contract: ethos mission show <id>"
```

### Why `--bare`

Bare mode skips hooks, plugins, MCP servers, CLAUDE.md, and auto memory.
The daemon provides exactly the context each mission needs. Benefits:

- **Security**: worker sees only what the daemon authorizes
- **Reproducibility**: same inputs → same behavior on any machine
- **Speed**: ~2s startup vs ~5-8s with full discovery
- **Isolation**: no ambient state from the host leaks in

### Authentication

Two options, both stored in beadle's secret store:

| Method | Billing | Secret name |
|--------|---------|-------------|
| `ANTHROPIC_API_KEY` | API usage | `claude-api-key` |
| `claude setup-token` | Subscription | `claude-token` |

The daemon sets the env var before `exec.Command`.

### MCP Config (per mission)

Each mission gets a tailored MCP config:

```json
{
  "mcpServers": {
    "ethos": {"command": "ethos", "args": ["mcp"]},
    "beadle-email": {"command": "beadle-email", "args": ["serve"]}
  }
}
```

Inbox missions need beadle-email + ethos. Code tasks need only ethos +
filesystem tools. The daemon builds the config from the mission archetype.

### Mission Prompt

Written to a temp file, injected via `--append-system-prompt-file`:

```text
You are a beadle mission worker. Your mission contract is {mission_id}.
Read it: ethos mission show {mission_id}
Execute within the write_set and budget constraints.
When done, submit your result: ethos mission result {mission_id} --file <path>
Do not commit, push, or merge unless the contract explicitly says to.
```

### Result Collection

Two channels, both must succeed:

1. **stdout JSON**: `{result: string, session_id: string, ...}`
2. **Ethos result artifact**: `ethos mission results <id> --json`

If stdout parses but no ethos artifact exists, the mission is incomplete.

## Safety Bounds

| Bound | Mechanism | Default |
|-------|-----------|---------|
| Turns | `--max-turns` | 50 |
| Cost | `--max-budget-usd` | 5.00 |
| Time | `exec.CommandContext` | 30 min |
| Permissions | `--permission-mode auto` | No human |
| Tools | `--allowedTools` | Per-mission whitelist |

## Failure Handling

| Failure | Detection | Response |
|---------|-----------|----------|
| Non-zero exit | `cmd.Wait()` error | Mark failed, email requester |
| Max turns exceeded | JSON `is_error: true` | Mark failed, retriable |
| Budget exceeded | Exit error | Mark failed, not retriable |
| Process timeout | Context deadline | Kill, mark failed |
| Malformed output | JSON parse error | Mark failed, log raw stdout |
| IMAP error | Poller `recordFailure` | Retry next cycle |

## Concurrency

Serial first. One mission at a time, `sync.Mutex` on the mission queue.

Rationale: concurrent missions introduce file-edit races, resource
contention, and interleaved debug output. These are harder to debug than
serial bottlenecks. Add `chan struct{}` semaphore after serial works.

## Pipeline Orchestration

Piped missions chain sequentially:

```text
mission-1 (check-schedule)
    → daemon reads result artifact
    → mission-2 (find-free-slot) with input from mission-1
    → daemon reads result
    → mission-3 (reserve-slot)
    → ...
```

Pipeline state lives in the daemon, not in ethos. Ethos provides individual
mission primitives. Beadle provides pipeline orchestration.

Pipeline failure stops the chain, preserves partial results in each mission's
artifact, and emails the requester.

## Ethos Missions as Control Plane

| Role | Entity |
|------|--------|
| Leader | beadle-daemon |
| Worker | Claude Code session (ephemeral) |
| Evaluator | beadle-daemon or designated agent |

### Contract Example

```yaml
leader: beadle-daemon
worker: claude-session
evaluator:
  handle: beadle-daemon

inputs:
  trigger:
    type: email
    message_id: "1205"
    from: jim@punt-labs.com
    subject: "Schedule a team meeting for Thursday"

write_set:
  - calendar integration output

success_criteria:
  - meeting scheduled for Thursday with all team members
  - confirmation email sent to jim@punt-labs.com

budget:
  rounds: 1
  reflection_after_each: false
```

## Deployment Modes

| Mode | Poller | Processing | Human required |
|------|--------|------------|----------------|
| Attended (today) | MCP goroutine | CronCreate `/loop` | Yes |
| Daemon (vyv) | Daemon goroutine | `claude -p` spawn | No |
| Docker daemon (9zh) | Same, in container | Same, in container | No |

Attended and daemon modes are independent, not competing:

- Attended: "Claude is my assistant, I'm at the terminal"
- Daemon: "beadle runs autonomously, I'm not home"

## Relationship to Channels (beadle-9rb)

Channels (when unblocked) provides a third mode: attended + channels =
autonomous inbox inside an interactive session. Channels and daemon are
complementary. Channels works when a human has Claude Code open. Daemon
works when no one is home.

## Security Model

- **x-bit is the authorization gate**: only x-permitted contacts trigger
  missions. Enforced by daemon, not ethos.
- **`--bare` isolates the worker**: no ambient state, no host config.
- **Mission contract is the scope boundary**: write_set + budget.
- **Frozen evaluator**: consistent review across mission rounds.
- **Append-only event log**: tamperproof audit trail in ethos.
- **Email provenance**: `inputs.trigger.message_id` links mission to email.

## Open Beads

- **beadle-vyv** (P2): This design. Implementation after design settles.
- **beadle-z16** (P1): x-bit enforcement. Depends on vyv.
- **beadle-9rb** (closed): Channels testing. Blocked on Claude Code gate.
- **beadle-juk** (P2): Full agent stack in compose.
- **beadle-40k** (P2): Propose `inputs.trigger` field to ethos.
