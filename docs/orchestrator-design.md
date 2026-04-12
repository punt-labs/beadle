# Beadle Orchestrator Design

> Beadle as autonomous agent daemon with ethos missions as the control plane.

## Architecture

```text
                        ┌─────────────────────────────────────────┐
                        │           Docker Sandbox / Compose       │
                        │                                         │
                        │  beadle-daemon (Go, always-on)          │
                        │    ├── IMAP poller (detection)          │
                        │    ├── ethos mission create/close       │
                        │    ├── pipeline orchestrator            │
                        │    └── spawns Claude Code sessions      │
                        │                                         │
                        │  Claude Code (worker, ephemeral)        │
                        │    ├── beadle-email (MCP, WebSocket)    │
                        │    ├── quarry (MCP, knowledge)          │
                        │    ├── vox (MCP, voice)                 │
                        │    ├── biff (MCP, team coordination)    │
                        │    ├── ethos (identity, missions)       │
                        │    └── Anthropic MCP tools (external)   │
                        │                                         │
                        └─────────────────────────────────────────┘
                                          │
                            IMAP/SMTP via Proton Bridge (host)
```

## The x Bit: Mission Authorization

DES-012 defines per-contact permissions as `rwx`:

| Bit | Meaning | Enforcement |
|-----|---------|-------------|
| `r` | Read and surface messages | Enforced in `list_messages`, `read_message` |
| `w` | Compose and send replies | Enforced in `send_email` |
| `x` | Trigger autonomous missions | Enforced by the daemon before `ethos mission create` |

The `x` bit gates mission creation. An email from a contact with `rwx`
that contains an instruction ("schedule a meeting", "update the website",
"run the test suite") triggers a mission. An email from a contact with
`rw-` gets a reply but never triggers autonomous multi-step action.

**Enforcement chain:**

1. Beadle reads email (`r` check)
2. Beadle classifies the email as containing an instruction
3. Beadle checks `x` permission on the sender for the active identity
4. If `x` granted: beadle creates an ethos mission with the instruction
5. If `x` denied: beadle replies acknowledging the message but does not act

The mission contract IS the audit trail for `x`. Every autonomous action
has a typed contract, bounded budget, frozen evaluator, and append-only
log. The email that triggered it is the provenance, recorded in
`inputs.trigger`.

## Ethos Missions as Control Plane

The daemon uses ethos missions (DES-031) as typed delegation contracts:

+ **Daemon = leader**: creates missions, monitors progress, chains outputs
+ **Claude Code session = worker**: executes the mission, bounded by write_set
+ **Daemon or designated agent = evaluator**: reviews results

### Mission lifecycle (daemon-driven)

1. Daemon detects actionable email (poller + x-bit check)
2. Daemon creates mission: `ethos mission create --file <contract.yaml>`
3. Daemon spawns Claude Code: `claude --prompt-file <mission-prompt.md>`
4. Claude Code reads mission: `ethos mission show <id>`
5. Claude Code executes within write_set and budget constraints
6. Claude Code submits result: `ethos mission result <id> --file <result.yaml>`
7. Daemon reads result: `ethos mission results <id> --json`
8. Daemon closes mission: `ethos mission close <id>`
9. If pipeline: daemon creates next mission with previous result as input

### Contract example (email-triggered)

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
  files: []

write_set:
  - calendar integration output

success_criteria:
  - meeting scheduled for Thursday with all team members
  - confirmation email sent to jim@punt-labs.com

budget:
  rounds: 1
  reflection_after_each: false
```

## Piped Missions

The PR/FAQ describes commands that pipe together like Unix:

```text
check-schedule | find-free-slot | reserve-slot | notify-attendees
```

Each stage is an independent mission. The daemon orchestrates the pipeline:

1. Create mission 1 (check-schedule), wait for completion
2. Read mission 1's result artifact
3. Create mission 2 (find-free-slot) with `inputs.files` pointing to
   mission 1's output
4. Repeat until pipeline completes or a stage fails

The pipeline state lives in the daemon, not in ethos. Ethos provides
the individual mission primitive. Beadle provides pipeline orchestration.
This is the right layering — a pipeline contract in ethos would
over-engineer the primitive for one consumer.

**Pipeline failure**: if any stage fails, the daemon stops the pipeline,
logs the failure, and notifies the original requester via email. The
partial results are preserved in each mission's result artifact.

## Deployment Modes

| Mode | Detection | Processing | Mission Control |
|------|-----------|------------|-----------------|
| Bare metal (today) | Server poller | CronCreate `/loop` | Manual via Claude Code |
| Bare metal + channels | Server poller | Channel notification | Manual via Claude Code |
| Docker daemon (beadle-vyv) | Server poller | Daemon spawns Claude Code | Daemon creates missions |

The Docker daemon mode is the target architecture. Channels and CronCreate
are interim mechanisms for the bare-metal case.

## Full Agent Stack (beadle-juk)

| Service | Runtime | Capability |
|---------|---------|------------|
| beadle-email | Go + gnupg | Email + PGP (sign, encrypt, decrypt, verify) |
| quarry | Python + ONNX | Semantic search, knowledge recall |
| vox | Python + TTS | Voice synthesis, audio email attachments |
| biff | Go + Python | Team messaging, presence, coordination |
| ethos | Go | Identity, missions, session roster |
| Anthropic MCP | External | Google Docs, Linear, Notion, Slack |

Each service runs as its own container or daemon. Claude Code connects
to all of them via mcp-proxy over WebSocket.

## inputs.trigger Field

Proposed addition to the ethos mission contract (confirmed with ethos
agent): an `inputs.trigger` field that records what caused the mission.

```yaml
inputs:
  trigger:
    type: email          # email | cron | manual | pipeline
    message_id: "1205"   # for email triggers
    from: jim@punt-labs.com
    subject: "Schedule a team meeting"
```

Ethos stores it as metadata without validating the contents. The audit
value is tracing a mission back to its trigger without reading the
daemon's logs.

## Security Model

+ **x-bit is the authorization gate**: only contacts with `x` permission
  can trigger missions. This is enforced by beadle, not ethos.
+ **Mission contract is the scope boundary**: write_set limits what the
  worker can modify. Budget limits how long it runs.
+ **Frozen evaluator ensures consistent review**: the same reviewer
  across all rounds of a mission.
+ **Append-only event log is the audit trail**: every state transition
  recorded, tamperproof.
+ **Email provenance**: the triggering email's message-id is recorded in
  `inputs.trigger`, linking the mission to its authorization.

## Relationship to PR/FAQ

The PR/FAQ describes "GPG-signed command documents" with "declared
permissions" and a "tamperproof audit trail." The orchestrator
architecture implements this vision with better primitives:

| PR/FAQ Concept | Implementation |
|----------------|----------------|
| GPG-signed command | Email from x-permitted contact + mission contract |
| Declared permissions | Mission write_set + contact rwx model |
| Tamperproof audit trail | Ethos append-only JSONL event log |
| Command pipeline | Daemon-orchestrated mission chain |
| Three interpreters | Claude Code with MCP tool stack |

## Open Beads

+ **beadle-vyv** (P2): Orchestrator — daemon spawns Claude Code with prompt file
+ **beadle-9rb** (P1): Channels — push inbox notifications as prompts
+ **beadle-juk** (P2): Full agent stack in compose
+ **x-bit enforcement**: New bead needed — implement x check in inbox processing
+ **inputs.trigger**: New bead needed — propose schema addition to ethos
