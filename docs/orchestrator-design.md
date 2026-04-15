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
2. Daemon reads full message (not just headers)
3. Daemon classifies transport trust via `ClassifyTrust` + `pgp.Verify`
4. **Reject if trust < verified** — unverified messages from x-bit contacts
   must NOT execute (DES-012 mandate: "an unverified message from an rwx
   contact should NOT be executed")
5. Daemon checks sender's x-bit for active identity
6. x granted + trust verified → create pipeline
7. x denied → skip (attended mode handles via `/inbox`)

Phase 1: all x-bit emails with verified transport trust are instructions.

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

## Missions as Typed Commands

Missions are not just "tasks for Claude to figure out." They are the command
abstraction for the daemon. Any operation — `biff wall`, `git log`,
`make deploy` — wrapped in a mission gets x-bit gating, audit trail, and
bounded execution for free.

### Command Definition (typed args, GPG-signed)

Commands are GPG-signed YAML files in `~/.punt-labs/beadle/commands/`:

```yaml
# wall.yaml
name: wall
description: Broadcast a message to all active agents via biff
signature: <owner GPG signature>
args:
  - name: message
    type: string
    max_length: 500
    required: true
input: none              # none | optional | required
output: prose            # prose | json | files
write_set: []
budget:
  rounds: 1
  reflection_after_each: false
timeout: 2m
prompt: |
  You have a structured argument "message" in the mission contract's
  inputs.args field. Read it with: ethos mission show {mission_id}
  Call biff wall with the message value as a direct argument.
tools:
  - Bash
mcp_servers:
  - ethos
  - biff
env_vars:
  - BIFF_TOKEN
```

**No string interpolation.** Args flow as structured data in the mission
contract's `inputs.args` field. The worker reads them from the contract.
When calling Bash, the worker passes args as direct arguments, never via
template string substitution. This prevents shell injection.

**GPG-signed.** Command files are signed by the owner's key. The daemon
verifies signatures at startup and rejects unsigned or tampered files.
Prevents filesystem write → command injection escalation.

**Validated at load time.** Required fields, arg types, write_set format,
budget, timeout. Malformed files logged and excluded from available set.

### Pipeline Composition

Pipelines are Unix pipes for an agent daemon:

```text
Email: "deploy the website and tell the team"
  ↓ planner decomposes
  [deploy | wall "deployed to production"]
  ↓ daemon executes sequentially
  mission-1 (deploy) → result → mission-2 (wall) → done
```

**Three layers:**

```text
Email instruction (natural language)
  ↓ decomposition (planner mission, read-only)
Pipeline definition [cmd1 | cmd2 | cmd3]
  ↓ execution (daemon, sequential)
Mission sequence [m-001 → m-002 → m-003]
```

### Planner (behind interface)

```go
type Planner interface {
    Plan(ctx context.Context, meta EmailMeta, body string) ([]CommandCall, error)
}
```

Two implementations:

- **LLMPlanner** — ethos mission, read-only (empty write_set), 1 round.
  Returns JSON array. For natural language decomposition.
- **RulePlanner** — regex/keyword config file. Fast-path for known
  patterns. Deterministic, testable, no LLM cost.

Planner output is **validated before execution**: each command name must
exist in the loaded (signed) command set, each arg must match the
command's declared schema (type, required, constraints). Unknown args
rejected. Validation is the security boundary — planner output is
untrusted LLM text influenced by the email.

### Input/Output Flow

Each command's output is the ethos result artifact's `prose` field or
structured JSON per the command's `output` declaration. The daemon reads
it via `ethos mission results <id> --json` and passes it as
`inputs.previous_output` in the next mission contract. Files flow via
the result artifact's `files_changed` paths.

### Pipeline State (persisted)

```go
type Pipeline struct {
    Version   int          // schema version
    ID        string       // unique pipeline ID
    CreatedAt time.Time    // creation time
    Email     EmailMeta    // triggering email
    Commands  []Command    // try: planned sequence
    ElseCmd   *Command     // else: error handler
    Current   int          // index of current command
    Results   []string     // collected outputs
    Status    string       // running, completed, failed
    Error     string       // internal failure reason (not sent to user)
    WriteSet  []string     // union of command write_sets (for locking)
}
```

Persisted to `~/.punt-labs/beadle/pipelines/<id>.json` at each state
transition via atomic rename. On daemon startup, scan for `running`
pipelines and send failure notifications. Silent loss on crash is
unacceptable.

### Pipeline Error Handling (try/else, fixed-text errors)

```text
try:  [plan | deploy | wall "deployed"]
else: [reply <fixed-text error with reference ID>]
```

The else clause fires when any try stage fails — including the planner.
The requester gets a fixed-text reply:

> Your request could not be completed. Reference: pipeline-abc123.

Internal details (exit codes, stderr, budget amounts, command names) go
to the daemon log only. This prevents information leakage to attackers
who trigger specific failures as an oracle. The owner looks up the
reference in the log.

Partial results from completed stages are preserved in ethos.

### Ethos Dependency

Beadle's extensibility is hard-dependent on ethos. Commands, pipelines,
and audit all flow through ethos missions. This is intentional — identity,
authorization, delegation, and audit are ethos primitives. Building a
parallel system would be worse than the coupling.

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

- **Transport trust required**: unverified messages from x-bit contacts
  are rejected (DES-012). Spoofed From headers cannot trigger missions.
- **x-bit is the authorization gate**: only x-permitted contacts trigger
  missions. Enforced by daemon, not ethos.
- **Command files GPG-signed**: prevents filesystem → code execution.
- **Typed args, no string interpolation**: prevents shell injection via
  attacker-influenced planner output.
- **Planner output validated against schemas**: untrusted LLM output is
  the security boundary.
- **`--bare` isolates the worker**: no ambient state, no host config.
- **Per-command env vars from secret store**: never full env passthrough.
- **Pipeline write_set union locked**: prevents concurrent overlap.
- **Fixed-text error replies**: no internal state leakage to requester.
- **Pipeline state persisted**: no silent loss on crash.
- **Mission contract is the scope boundary**: write_set + budget + timeout.
- **Append-only event log**: tamperproof audit trail in ethos.

## Open Beads

- **beadle-88g** (P1): Pipeline orchestrator — command composition layer.
- **beadle-vyv** (closed): Orchestrator design. Settled.
- **beadle-z16** (closed): x-bit enforcement. Shipped.
- **beadle-9rb** (closed): Channels testing. Blocked on Claude Code gate.
- **beadle-juk** (P2): Full agent stack in compose.
- **beadle-40k** (P2): Propose `inputs.trigger` field to ethos.
