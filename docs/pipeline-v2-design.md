# Pipeline v2: Multi-Runner Commands with Process/Passthrough Data Flow

**Status:** PROPOSED (2026-04-18)

**Authors:** Jim Freeman, Claude Agento

**Predecessors:** DES-028 (pipeline orchestrator), DES-029 (reply as stage),
DES-030 (multi-runner)

## Problem

The current pipeline implementation has three limitations:

1. **Every command spawns a Claude session.** Deterministic operations
   (biff wall, ethos mission list, git log) take 45-60 seconds through
   `claude -p --bare` when the actual work is a 10ms subprocess.

2. **Side-effect commands destroy the pipe.** A linear pipe passes
   output from stage N to stage N+1. If stage 1 is `biff wall` (a
   notification), its stdout ("ok") overwrites the summary from stage 0.
   Stage 2 (reply) receives "ok" instead of the summary.

3. **CLI commands need data extraction.** A `biff wall` command needs
   to extract a title from a JSON summary, not broadcast 500 words of
   prose. This requires chaining tools (jq | biff) within a single
   command.

## Design

### The Pipe

A single JSON value flows through the pipeline. Every command receives
the pipe as input. What happens to the pipe after a command runs depends
on the command's `mode`:

- **`mode: process`** (default) — command's output replaces the pipe.
- **`mode: passthrough`** — pipe carries forward unchanged. The command
  reads the pipe for reference but is a side-effect.

There is no accumulator. No stage-indexed history. A `process` command
overwrites the pipe and the prior value is gone. This is intentional —
it limits the data surface available to any given stage.

**Initial pipe value:** At stage 0, the pipe contains the triggering
email metadata serialized as JSON:

```json
{
  "message_id": "1829",
  "from": "jim@punt-labs.com",
  "subject": "summarize this",
  "trust_level": "trusted"
}
```

This is the same data available in `EmailMeta` today. Stage 0 commands
use this metadata to look up the full message via MCP tools (e.g.,
`beadle-email read`). The pipe is never empty.

### Runners

Each command declares a `runner` that determines how the daemon
executes it:

**`claude` runner (default):**

- Spawns `claude -p --bare` with per-command MCP config
- Creates an ethos mission with typed args, write-set, budget
- Adversarial system prompt for prompt injection resistance
- Collects output from mission result
- Closes mission after stage completes
- Requires: `prompt`, `budget` (mission contract fields)
- Use for: reasoning, analysis, summarization, composition

**`cli` runner:**

- Execs binaries directly via `exec.Command` — no LLM, no mission
- Binary resolved against a whitelist (not system PATH)
- Typed args assembled into explicit arg list — no shell expansion
- Stdout captured as stage output, stderr to daemon log
- Exit code 0 = success, nonzero = else clause
- Does not use: `prompt`, `budget`, `mcp_servers`, `write_set`
  (these fields are ignored if present, not required)
- Use for: notifications, status checks, data transformation, any
  deterministic operation

**Runner-conditional validation.** The `validateCommand` function
applies different rules per runner:

| Field | `claude` | `cli` |
|-------|----------|-------|
| `prompt` | Required | Ignored |
| `budget` | Required (rounds > 0) | Ignored |
| `mcp_servers` | Optional | Ignored |
| `write_set` | Optional | Ignored |
| `binary` | Ignored | Required (unless `steps`) |
| `steps` | Invalid | Optional |
| `output_schema` | Required | Required |
| `mode` | Required (default: process) | Required (default: process) |

### Compound CLI Commands

A CLI command can declare `steps` — a mini-pipeline of binaries where
each step's stdout feeds the next step's stdin. The daemon chains them
via explicit `exec.Command` calls with `io.Pipe` connecting stdout to
stdin. No shell is ever invoked.

```yaml
name: wall
runner: cli
mode: passthrough
description: Broadcast a notification to all active agents
steps:
  - binary: jq
    fixed_args: ["-r", ".title"]
    stdin: pipe           # receives the main pipe data
  - binary: biff
    fixed_args: ["wall"]
    stdin: stdout         # receives previous step's stdout
timeout: 30s
```

Single-binary commands omit `steps` and use `binary` + `fixed_args`
directly:

```yaml
name: format
runner: cli
mode: process
binary: jq
fixed_args: ["-r", ".summary"]
timeout: 10s
```

**Step stdin rules:**

- Step 0 must declare `stdin: pipe` (receives the main pipeline data).
- Step N (N > 0) must declare `stdin: stdout` (receives the previous
  step's stdout).
- Any other value or combination is rejected at command load time.
- `stdin: pipe` on step N > 0 is invalid — there is no mechanism to
  re-inject the original pipe mid-chain.

**Compound step execution model:**

All steps in a compound command run concurrently as goroutines,
connected by `io.Pipe`. This is required to avoid deadlock — a
sequential model where step[0] runs to completion before step[1]
starts will block if step[0]'s stdout buffer fills before any
reader drains it.

The execution sequence:

1. Create `io.Pipe` pairs connecting adjacent steps.
2. Start all steps as goroutines under a shared `context.WithTimeout`.
3. Step[0] receives the pipe data on stdin. Step[N] receives
   step[N-1]'s stdout via the `io.Pipe`.
4. The final step's stdout is collected into a capped buffer (1 MB).
5. Wait for all goroutines to complete.

**Error handling:**

- The `timeout` applies to the entire step chain, not per step. A
  single `context.WithTimeout` wraps all goroutines.
- The first nonzero exit code cancels the shared context, which
  signals all other goroutines to abort.
- The command's output is empty on abort. The pipeline enters the
  else clause.
- Each step's stderr is independently captured and logged to the
  daemon log with a `step[N]` label for diagnostics.

**Argument handling for CLI commands:**

CLI commands support typed args from the planner's `CommandCall.Args`,
assembled into the exec argument list:

- `fixed_args` are prepended (e.g., `["wall"]` for `biff wall`)
- Args with a `position` field are appended in position order
- Args without `position` become `--name=value` flags
- No shell expansion, no globbing — `exec.Command(binary, args...)`

This is consistent with DES-030. The `position` field is not yet
implemented in `command.go`'s `CommandArg` struct — it must be added
as part of v2 implementation.

### Pipe Payload Format

JSON is the standard pipe format. Rationale:

1. **CLI tools need field extraction.** `jq -r ".title"` requires JSON
   input. Prose input forces an LLM step for extraction, defeating the
   purpose of CLI runners.
2. **Validation between stages.** The daemon validates output after each
   `process` stage. Malformed output is caught before it corrupts
   downstream stages.
3. **Cross-runner compatibility.** Claude stages producing JSON feed
   cleanly into CLI stages consuming via jq, and vice versa.

**Output schema replaces the existing `output` and `input` fields.**
The current `Command` struct has `Output string` (`prose | json | files`)
and `Input string` (`none | optional | required`) from DES-028. Both
are retired:

- `output` → replaced by `output_schema` (see below).
- `input` → removed. All commands receive the pipe. The `mode` field
  (`process | passthrough`) replaces the read/write semantics that
  `input` partially covered. `input: none` maps to `mode: passthrough`.
  `input: required` maps to `mode: process`.

`output_schema` replaces `output`:

- **Structured output:** `output_schema` contains an inline JSON Schema
  object. The daemon validates process-mode output against the schema
  after the command completes. Validation failure enters the else clause.

  ```yaml
  output_schema:
    type: object
    properties:
      title: { type: string }
      summary: { type: string }
      key_points: { type: array, items: { type: string } }
  ```

- **Freeform text:** `output_schema: text` is an explicit opt-out from
  JSON validation. The pipe carries the raw string. `json.Valid()` is
  NOT called — the output bypasses all JSON validation. Downstream
  commands that expect JSON will fail at their own parsing step, which
  is the correct behavior (the type mismatch is between the producing
  and consuming command, not a validation bug).

The schema is part of the GPG-signed YAML — tamperproof.

**Type validation:** `output_schema` decodes from YAML as `any`. The
validator must type-switch and accept exactly two forms:

- `string` with value `"text"` — freeform text opt-out
- `map[string]any` — inline JSON Schema object

Any other type (number, list, boolean, or string other than `"text"`)
is rejected at load time with a descriptive error.

**Migration:** Existing command YAMLs using `output: prose` migrate to
`output_schema: text`. `output: json` migrates to an inline schema.
`output: files` is not yet used and can be removed. `input: none`
migrates to `mode: passthrough`. `input: required` migrates to
`mode: process` (the default, so it can be omitted).

The struct changes (`Output` → `OutputSchema`, `Input` removed) and
the YAML migrations must happen atomically in one commit. The YAML
decoder uses `KnownFields(true)`, which rejects unknown fields — if
`Output` is removed from the struct but existing YAMLs still contain
`output:`, the loader will fail. Both the struct and the YAMLs must
change together.

`validOutputModes` and `validInputModes` are retired.

**Schema validation library:** Use `github.com/santhosh-tekuri/jsonschema/v6`
(JSON Schema Draft 2020-12). No external `$ref` support — schemas must
be self-contained within the YAML file. This prevents network-dependent
validation and keeps the signing boundary clean.

### Command YAML Examples

**Claude runner, process mode (transforms the pipe):**

```yaml
name: summarize
runner: claude
mode: process
description: Summarize the triggering email
mcp_servers: [ethos, beadle-email]
output_schema:
  type: object
  properties:
    title: { type: string }
    summary: { type: string }
    key_points: { type: array, items: { type: string } }
timeout: 5m
prompt: |
  Read the triggering message via beadle-email and produce a JSON
  summary with title, summary, and key_points fields.
```

**CLI runner, passthrough mode, compound steps (side-effect):**

```yaml
name: wall
runner: cli
mode: passthrough
description: Broadcast a notification to all active agents via biff
steps:
  - binary: jq
    fixed_args: ["-r", "\"Pipeline: \" + .title"]
    stdin: pipe
  - binary: biff
    fixed_args: ["wall"]
    stdin: stdout
timeout: 30s
```

**Claude runner, process mode, auto-appended by executor:**

```yaml
name: reply
runner: claude
mode: process
description: Send the pipe contents back to the originator
mcp_servers: [ethos, beadle-email]
output_schema: text
timeout: 5m
prompt: |
  Send the pipeline output as a reply to the originator.
  The pipe value is a JSON object. Format it as a readable
  email body before sending.
```

### Example Pipeline

```text
Email: "summarize this" from jim@punt-labs.com

Initial pipe: {"message_id":"1829","from":"jim@punt-labs.com",
               "subject":"summarize this","trust_level":"trusted"}

Planner output: [summarize, wall]
Executor appends: [reply]

stage 0: summarize (claude, process)
  → reads pipe.message_id via beadle-email, produces JSON summary
  → pipe = {"title": "Deploy plan", "summary": "...", "key_points": [...]}

stage 1: wall (cli, passthrough)
  → reads pipe, jq extracts title, biff broadcasts "Pipeline: Deploy plan"
  → pipe = {"title": "Deploy plan", "summary": "...", "key_points": [...]}
  (unchanged — passthrough)

stage 2: reply (claude, process)
  → reads pipe (full summary JSON), formats and sends to jim@punt-labs.com
  → pipe = "Reply sent"
```

### Executor Dispatch

```text
pipe = serialize(email_meta)   # initial pipe value

for each stage in pipeline:
  pass pipe to command

  switch command.runner:
    case "claude":
      create mission with pipe as inputs.pipeline_output (JSON string)
      → build MCP config → spawn claude -p --bare
      → collect output from mission result → close mission

    case "cli":
      if command has steps:
        step[0] receives pipe on stdin
        step[N] receives step[N-1] stdout on stdin
        first nonzero exit → abort, enter else clause
        final step's stdout = command output
      else:
        resolve binary (whitelist check at execution time)
        → build arg list from fixed_args + typed args
        → exec with timeout → capture stdout
        nonzero exit → enter else clause

  if command.mode == "process":
    if command.output_schema != "text":
      validate json.Valid(output)
      validate output against output_schema
      failure → enter else clause (pipe remains unchanged)
    pipe = command output

  # passthrough: pipe unchanged, command output logged only
```

### Auto-Reply and the Pipe

The executor auto-appends a `reply` command as the terminal stage
(DES-029). The `reply` command receives the current pipe value — which
is a JSON object (or a text string if the last process stage used
`output_schema: text`).

The reply command's prompt instructs the Claude worker to format the
JSON into a readable email body before sending. The pipe value is
passed as `inputs.pipeline_output` in the mission contract (a JSON string
field), not as the `message` arg. This avoids the type mismatch
between a JSON pipe value and a string arg.

**Implementation change:** `buildStageContract()` in `pipeline.go`
currently puts `previous_output` in the mission contract and the
auto-reply block constructs `Args: {"to": ..., "message": ...}`.
Under v2, this function must be updated: for all stages, the pipe
value goes into `inputs.pipeline_output` (not `args`). The `reply`
command reads it from the contract, not from args. The `to` field
remains an arg (it's the recipient address, not pipe data).

**Else-path reply:** When the else clause fires, the reply command
still executes to notify the originator. The `inputs.pipeline_output`
value in the else path is the fixed-text error string (e.g., "Your
request could not be completed. Reference: pipeline-abc123."). The
pipe state at failure time is not passed — internal pipeline state
must not leak to the originator via error replies.

## Security Model

### By Runner

| Concern | claude runner | cli runner |
|---------|------------|-----------|
| Binary trust | Fixed `claude` binary | Whitelist-only resolution |
| Arg injection | Typed args in mission contract | Typed args in exec.Command |
| Shell injection | No shell involved | No shell involved |
| Compound steps | N/A (single Claude session) | Each step is explicit exec |
| Pipe data access | All commands read the pipe | All commands read the pipe |
| Isolation | --bare mode, adversarial prompt | No LLM, no prompt injection surface |
| Env leakage | Minimal env + declared vars | Minimal env + declared vars |
| Audit | Ethos mission trail | Pipeline state log |
| Signing | GPG-signed command YAML | Same — covers binary, steps, mode |

### Compound Step Security

Compound steps use `jq` expressions defined in `fixed_args` within the
GPG-signed command YAML. The `jq` expression is code (signed, immutable);
the pipe data is data (attacker-influenced via email content). `jq`
treats these as separate domains — the expression operates on the data
but the data cannot become code. This is the same security model as
parameterized SQL queries: the query is fixed, the parameters are data.

An attacker who controls the email content can influence field values in
the pipe JSON (e.g., `.title` contains malicious text). This text may
appear in `biff wall` output or email replies. This is a content
injection risk, not a code execution risk — mitigated by the same
content-sanitization practices as any user-facing output.

### Binary Whitelist

The `binary` field names an executable. The daemon resolves it against
a whitelist of allowed paths, not the system PATH:

- `~/.local/bin/` (user-installed tools)
- The beadle install directory
- Paths declared in daemon config

Binaries are validated against the whitelist at two points:

1. **Load time** (`LoadCommands`) — reject commands that reference
   binaries outside the whitelist. Prevents malformed commands from
   entering the available set.
2. **Execution time** (before `exec.Command`) — resolve the binary
   path with `filepath.EvalSymlinks` to follow symlinks, then verify
   the resolved absolute path is in the whitelist. Catches symlink
   redirection or binary substitution between load and execution.
   Comparison is against resolved absolute paths, not name strings.

### GPG Signing

Command YAML files are GPG-signed by the owner's key. The signature
covers all fields: `runner`, `binary`, `steps`, `mode`, `output_schema`,
`prompt`, and `fixed_args`. The daemon verifies signatures at startup
and rejects unsigned or tampered files.

Signing + whitelist = defense in depth. Even a validly signed command
can only exec whitelisted binaries.

### Information Hiding

The pipe carries one value. There is no accumulator and no
stage-indexed history. A `process` command overwrites the pipe — the
prior value is gone. A `passthrough` command cannot modify the pipe.

This means:

- Stage N can only see the current pipe value, not outputs from
  arbitrary prior stages
- A `passthrough` command's output is captured in the pipeline log
  but never enters the pipe
- No mechanism exists for a later stage to retrieve data that a
  `process` stage overwrote

If a future use case requires cross-stage data access, that is a
redesign with a dedicated security review — not an incremental feature.

### Output Size Cap

Both runners cap captured output at **1 MB**. Output exceeding this
limit is truncated and the stage fails (enters the else clause). This
prevents a malicious or runaway command from exhausting daemon memory.

The cap applies to:

- Claude runner: mission result prose field
- CLI runner: stdout from the final step (or single binary)
- Compound steps: each intermediate step's stdout is piped directly
  to the next step's stdin (no buffering), but the final step's
  output is capped on the read side

**Implementation note:** Use `io.LimitReader(finalStdout, 1<<20)` on
the read side — not a write-side buffer check. `io.LimitReader` stops
reading after 1 MB and the goroutine's write unblocks immediately
because the read side closes. A write-side `bytes.Buffer` with a size
check has a race: the goroutine writes the full output before the
check fires. The one-liner:
`output, _ = io.ReadAll(io.LimitReader(finalStep.stdout, 1<<20))`

## Design Boundaries

- **One pipe value.** No accumulator, no history.
- **No cross-references.** No `input_from: stage-N`.
- **No branching.** Pipelines are strictly linear.
- **No shell.** Compound commands chain binaries explicitly.
- **JSON default.** `output_schema: text` is the explicit opt-out.
- **Two runners.** `claude` and `cli`. A third (e.g., `http` for
  webhooks) can be added without changing the model.

## Implementation Plan

The following changes are required to the existing codebase:

### Phase 1: Struct and Validator (blocking)

1. **`command.go`:** Add `Runner string` (default `"claude"`),
   `Mode string` (default `"process"`), and `OutputSchema any`
   fields to `Command`. Add `Steps []Step` struct. Add `Position int`
   to `CommandArg`. Retire `Output string`, `Input string`,
   `validOutputModes`, and `validInputModes`.

2. **`command.go` (`validateCommand`):** Runner-conditional validation:
   - `runner` must be `claude | cli`. Default `claude`.
   - `mode` must be `process | passthrough`. Default `process`.
   - `prompt`: required for `claude`, ignored for `cli`.
   - `budget`: required (rounds > 0) for `claude`, ignored for `cli`.
   - `binary`: required for `cli` (unless `steps`), ignored for `claude`.
   - `output_schema`: type-switch — accept `string("text")` or
     `map[string]any`. Reject number, list, boolean, other strings.
   - Compound step `stdin` rules: step 0 = `pipe`, step N > 0 = `stdout`.
   - Binaries validated against whitelist at load time.

3. **Migrate existing command YAMLs** (`summarize.yaml`, `reply.yaml`)
   to use `output_schema` instead of `output` and remove `input`.
   This commit must be atomic with the struct changes — `KnownFields(true)`
   rejects unknown fields, so the struct and YAMLs must change together.

4. **Migrate test fixtures.** `pipeline_test.go:testCommands()` and
   any `Command` struct literals in `command_test.go` use the retired
   `Input` and `Output` fields. These must be updated to `Mode` and
   `OutputSchema` in the same commit — otherwise `make check` fails
   at compile time.

### Phase 2: CLI Runner (unblocked by Phase 1)

1. **`runner.go` (new):** `Runner` interface with `Run(ctx, pipe, cmd) (output, error)`.
   `ClaudeRunner` wraps existing spawner + mission logic.
   `CLIRunner` implements single-binary exec with whitelist check,
   arg assembly, timeout, stdout capture, and 1 MB output cap.

2. **`executor` dispatch:** Replace direct spawner call with
   `Runner.Run()` dispatch based on `command.Runner`.

### Phase 3: Compound Steps (unblocked by Phase 2)

1. **`CLIRunner` compound execution:** Goroutine-per-step with
   `io.Pipe` chaining. All steps start concurrently under a shared
   `context.WithTimeout`. First nonzero exit cancels the context.
   Per-step stderr logging with `step[N]` labels. Final step's
   stdout buffered and capped at 1 MB.

### Phase 4: Process/Passthrough + Validation (unblocked by Phase 1)

1. **Executor pipe logic:** Track `pipe` variable. After each stage:
   if `mode == "process"`, replace pipe with output. If `passthrough`,
   leave pipe unchanged.

2. **JSON + schema validation:** `json.Valid()` for process-mode
   stages with non-text schemas. Schema validation via
   `santhosh-tekuri/jsonschema/v6`. Text-mode stages bypass all
   JSON validation.

3. **Auto-reply wiring:** Pass pipe as `inputs.pipeline_output` in the
   reply mission contract instead of raw `message` arg. Migrate
   `reply.yaml` to remove the `message` required arg and document
   that the reply command reads `inputs.pipeline_output` from the
   mission contract instead.

### Post-Implementation

1. **Update DES-030** in `DESIGN.md` — its YAML examples use the
   retired `output` field and pre-v2 command schema. Update examples
   to match the v2 format after this design is settled.

## Rejected Alternatives

- **Shell execution (`sh -c`) for compound commands.** Shell injection
  surface. The daemon must never invoke a shell.

- **Accumulator model (all outputs stored, any stage can reference any
  prior output).** Violates information hiding. Every stage can read
  every prior stage's output, including data it has no business seeing.
  Expands the data surface with every stage.

- **Everything through Claude (current implementation).** 45-60 second
  overhead for a 10ms `biff wall`. CLI tools that already exist should
  execute directly.

- **Missions for CLI commands.** 2-3 seconds of ethos overhead per CLI
  call. For a 10ms operation, that is 300x overhead. Pipeline audit
  trail is sufficient for deterministic commands.

- **Dynamic binary resolution via system PATH.** Attacker who controls
  PATH controls execution. Whitelist-only resolution.

- **Runner as daemon-level config, not per-command.** Some commands are
  inherently LLM tasks (summarize), others are inherently CLI (wall).
  The command author knows which. Per-command declaration.

- **Separate command directories per runner.** Unnecessary complexity.
  The `runner` field is sufficient. Both types coexist in
  `~/.punt-labs/beadle/commands/`.

- **Plugin/extension model instead of runners.** Over-engineers the
  dispatch. Two runners cover the space: reasoning (claude) and
  deterministic (cli). A third runner can be added later without
  changing the model.

- **Unstructured (text) pipe by default.** CLI tools need field
  extraction. Text pipes force LLM steps for extraction, defeating
  the purpose of CLI runners. JSON enables `jq` at every boundary.

- **External `$ref` in output schemas.** Network-dependent validation
  breaks offline operation and moves the trust boundary outside the
  signed YAML. Schemas must be self-contained.
