# Pipeline v2 Implementation Design

**Status:** DRAFT (2026-04-18)

**Source:** `docs/pipeline-v2-design.md` (architecture document)

**Purpose:** Concrete implementation spec for each task. A fresh agent
can implement any task mechanically from this document without making
architectural decisions.

## Dependency Order

```text
T1 (struct + validator) ── blocking everything
  |
  ├── T2 (Runner interface) ── blocks T3
  |     |
  |     └── T3 (CLIRunner single binary)
  |
  ├── T4 (compound steps) ── depends on T3
  |
  ├── T5 (pipe variable) ── depends on T2
  |
  ├── T6 (output schema validation) ── depends on T5
  |
  └── T7 (auto-reply wiring) ── depends on T5
```

T2 and T5 can proceed in parallel after T1. T3 depends on T2. T4
depends on T3. T6 and T7 depend on T5. All seven tasks touch
`internal/daemon/`.

---

## T1: Command Struct v2 + Validator

### Files touched

- `internal/daemon/command.go`
- `internal/daemon/command_test.go`
- `internal/daemon/pipeline_test.go` (testCommands)
- `internal/daemon/pipeline.go` (buildStageContract references)

### Struct changes

**Add to `CommandArg`:**

```go
type CommandArg struct {
    Name      string   `yaml:"name"`
    Type      string   `yaml:"type"`
    Values    []string `yaml:"values"`
    MaxLength int      `yaml:"max_length"`
    Required  bool     `yaml:"required"`
    Default   string   `yaml:"default"`
    Position  int      `yaml:"position"` // NEW: positional index for CLI arg assembly
}
```

`Position` is zero-valued by default. A zero value means "named flag"
(`--name=value`). A positive value means "positional at that index."
Validation: if `Position > 0`, no two args in the same command may
share the same `Position` value (reject at load time).

**Add `Step` struct (new):**

```go
// Step is one binary in a compound CLI command chain.
type Step struct {
    Binary    string   `yaml:"binary"`
    FixedArgs []string `yaml:"fixed_args"`
    Stdin     string   `yaml:"stdin"` // "pipe" or "stdout"
}
```

**Replace fields in `Command`:**

Remove:

- `Input  string \`yaml:"input"\`` (entire field)
- `Output string \`yaml:"output"\`` (entire field)

Add:

- `Runner       string   \`yaml:"runner"\``       -- "claude" or "cli", default "claude"
- `Mode         string   \`yaml:"mode"\``         -- "process" or "passthrough", default "process"
- `OutputSchema any      \`yaml:"output_schema"\`` -- string "text" or map[string]any (JSON Schema)
- `Binary       string   \`yaml:"binary"\``       -- for single-binary CLI commands
- `FixedArgs    []string \`yaml:"fixed_args"\``   -- for single-binary CLI commands
- `Steps        []Step   \`yaml:"steps"\``        -- for compound CLI commands

The full `Command` struct after changes:

```go
type Command struct {
    Name         string       `yaml:"name"`
    Description  string       `yaml:"description"`
    Signature    string       `yaml:"signature"`
    Runner       string       `yaml:"runner"`        // claude | cli
    Mode         string       `yaml:"mode"`          // process | passthrough
    Args         []CommandArg `yaml:"args"`
    OutputSchema any          `yaml:"output_schema"` // "text" or map[string]any
    Binary       string       `yaml:"binary"`        // cli runner: single-binary
    FixedArgs    []string     `yaml:"fixed_args"`    // cli runner: single-binary args
    Steps        []Step       `yaml:"steps"`         // cli runner: compound steps
    WriteSet     []string     `yaml:"write_set"`
    Budget       struct {
        Rounds              int  `yaml:"rounds"`
        ReflectionAfterEach bool `yaml:"reflection_after_each"`
    } `yaml:"budget"`
    Timeout    string   `yaml:"timeout"`
    Prompt     string   `yaml:"prompt"`
    Tools      []string `yaml:"tools"`
    MCPServers []string `yaml:"mcp_servers"`
    EnvVars    []string `yaml:"env_vars"`
}
```

### Remove package-level vars

Delete `validInputModes` and `validOutputModes`. They are replaced by
inline checks in `validateCommand`.

### New `validateCommand` logic

The function applies defaults first, then validates per runner.

```text
func validateCommand(cmd *Command) error:

1. if cmd.Name == ""          → error "missing required field: name"

2. Default runner:
   if cmd.Runner == ""        → cmd.Runner = "claude"
   if cmd.Runner not in {"claude", "cli"} → error

3. Default mode:
   if cmd.Mode == ""          → cmd.Mode = "process"
   if cmd.Mode not in {"process", "passthrough"} → error

4. Runner-conditional:

   switch cmd.Runner {
   case "claude":
       if cmd.Prompt == ""    → error "claude runner requires prompt"
       if cmd.Budget.Rounds <= 0 → error "claude runner requires budget.rounds > 0"
       if cmd.Binary != ""    → error "binary is not valid for claude runner"
       if len(cmd.Steps) > 0  → error "steps is not valid for claude runner"
       if len(cmd.FixedArgs) > 0 → error "fixed_args is not valid for claude runner"

   case "cli":
       if cmd.Binary == "" && len(cmd.Steps) == 0
           → error "cli runner requires binary or steps"
       if cmd.Binary != "" && len(cmd.Steps) > 0
           → error "cli runner: set binary or steps, not both"
       // prompt, budget, mcp_servers, write_set, tools are ignored (not errors)

5. OutputSchema type-switch:
   if cmd.OutputSchema == nil → error "missing required field: output_schema"

   switch v := cmd.OutputSchema.(type) {
   case string:
       if v != "text" → error "output_schema string must be \"text\", got %q"
   case map[string]any:
       // valid JSON Schema object — accepted
   default:
       → error "output_schema must be \"text\" or a JSON Schema object, got %T"

6. Timeout validation (unchanged):
   if cmd.Timeout != "" → time.ParseDuration check

7. Arg validation (unchanged, plus position uniqueness):
   seen positions map[int]string
   for each arg:
     - existing checks (name, type, enum values)
     - if arg.Position > 0 and seen[arg.Position] exists
         → error "arg %q: duplicate position %d (conflicts with %q)"
       seen[arg.Position] = arg.Name

8. Compound step validation (cli runner with steps):
   if len(cmd.Steps) > 0:
     for i, step := range cmd.Steps:
       if step.Binary == "" → error "step[%d]: missing binary"
       if i == 0 && step.Stdin != "pipe"
           → error "step[0]: stdin must be \"pipe\", got %q"
       if i > 0 && step.Stdin != "stdout"
           → error "step[%d]: stdin must be \"stdout\", got %q"
```

### YAML migration

The struct uses `KnownFields(true)`, so YAMLs with `input:` or
`output:` will fail to parse after `Input` and `Output` are removed
from the struct. The struct change and YAML changes must be in the
same commit.

**`summarize.yaml` migration:**

Before:

```yaml
input: required
output: json
```

After:

```yaml
runner: claude
mode: process
output_schema:
  type: object
  properties:
    title: { type: string }
    summary: { type: string }
    key_points: { type: array, items: { type: string } }
```

**`reply.yaml` migration:**

Before:

```yaml
input: required
output: prose
```

After:

```yaml
runner: claude
mode: process
output_schema: text
```

Note: the `reply` command's `message` arg is retired in T7. For T1,
keep the arg list as-is to avoid breaking the executor. T7 removes it.

### `validCommandYAML` in command_test.go

The test constant must be updated:

Before:

```yaml
input: none
output: prose
```

After:

```yaml
runner: claude
mode: passthrough
output_schema: text
```

Also update `TestLoadCommands_FieldValues` assertions: replace
`cmd.Input` / `cmd.Output` with `cmd.Runner` / `cmd.Mode` /
`cmd.OutputSchema`.

Update `TestLoadCommands_DefaultInputOutput` to
`TestLoadCommands_DefaultRunnerMode`:

- Write a minimal YAML without `runner` or `mode`
- Assert defaults: `cmd.Runner == "claude"`, `cmd.Mode == "process"`

Add new `validCommandYAML` with `output_schema: text` (the string
form). Add a second test constant for a CLI command YAML.

Add new test cases to `TestLoadCommands`:

- "skip cli runner missing binary and steps" (runner: cli, no binary,
  no steps)
- "skip claude runner with binary" (runner: claude, binary: jq)
- "skip output_schema number" (output_schema: 42)
- "skip output_schema invalid string" (output_schema: json)
- "valid cli runner single binary" (runner: cli, binary: jq,
  output_schema: text)
- "valid claude runner with schema object" (output_schema as map)
- "skip step[0] stdin not pipe"
- "skip step[1] stdin not stdout"
- "skip duplicate arg positions"

### `testCommands()` in pipeline_test.go

Every `Command` struct literal uses `Input` and `Output`. Replace with
`Runner`, `Mode`, and `OutputSchema`. Since all existing test commands
are claude-runner commands:

```go
func testCommands() map[string]*Command {
    return map[string]*Command{
        "greet": {
            Name:         "greet",
            Runner:       "claude",
            Mode:         "passthrough",
            Prompt:       "Greet the user",
            OutputSchema: "text",
            Budget: struct {
                Rounds              int  `yaml:"rounds"`
                ReflectionAfterEach bool `yaml:"reflection_after_each"`
            }{Rounds: 1},
            WriteSet:   []string{"output/greet.txt"},
            MCPServers: []string{"ethos"},
        },
        "summarize": {
            Name:         "summarize",
            Runner:       "claude",
            Mode:         "process",
            Prompt:       "Summarize the input",
            OutputSchema: map[string]any{
                "type": "object",
                "properties": map[string]any{
                    "title":   map[string]any{"type": "string"},
                    "summary": map[string]any{"type": "string"},
                },
            },
            Budget: struct {
                Rounds              int  `yaml:"rounds"`
                ReflectionAfterEach bool `yaml:"reflection_after_each"`
            }{Rounds: 1},
            WriteSet:   []string{"output/summary.txt"},
            MCPServers: []string{"ethos", "beadle-email"},
        },
        "deploy": {
            Name:         "deploy",
            Runner:       "claude",
            Mode:         "process",
            Prompt:       "Deploy to production",
            OutputSchema: "text",
            Args: []CommandArg{
                {Name: "env", Type: "enum", Values: []string{"prod", "staging"}, Required: true},
            },
            Budget: struct {
                Rounds              int  `yaml:"rounds"`
                ReflectionAfterEach bool `yaml:"reflection_after_each"`
            }{Rounds: 1},
            WriteSet:   []string{"deploy/manifest.yaml"},
            MCPServers: []string{"ethos"},
        },
        "reply": {
            Name:         "reply",
            Runner:       "claude",
            Mode:         "process",
            Prompt:       "Reply to the sender with the pipeline output",
            OutputSchema: "text",
            Args: []CommandArg{
                {Name: "to", Type: "string", Required: true},
                {Name: "message", Type: "string", Required: true},
            },
            Budget: struct {
                Rounds              int  `yaml:"rounds"`
                ReflectionAfterEach bool `yaml:"reflection_after_each"`
            }{Rounds: 1},
            WriteSet:   []string{"daemon output"},
            MCPServers: []string{"ethos", "beadle-email"},
        },
    }
}
```

Note: `greet` was `input: "none"` which maps to `mode: "passthrough"`.
`summarize` was `input: "required"` which maps to `mode: "process"`.
`deploy` was `input: "optional"` which maps to `mode: "process"` (the
default). `reply` was `input: "required"` which maps to
`mode: "process"`.

---

## T2: Runner Interface

### Files touched

- `internal/daemon/runner.go` (new file)
- `internal/daemon/pipeline.go` (executor dispatch)

### Interface

```go
// Runner executes a single pipeline command and returns its output.
type Runner interface {
    Run(ctx context.Context, e *Executor, p *Pipeline, idx int, cmd *Command, call CommandCall, pipe string) (string, error)
}
```

The `pipe` parameter is the current pipe value (JSON string or text).
The runner is responsible for passing the pipe to the command in the
appropriate way (mission contract for claude, stdin for cli).

### ClaudeRunner

```go
// ClaudeRunner executes a command via a Claude Code worker session.
type ClaudeRunner struct{}
```

`ClaudeRunner.Run` extracts the body of the current `executeStage`
function — everything from `buildStageContract` through
`exec.CommandContext(ctx, "ethos", ...)`. The key change: instead of
`prevOutput`, it receives `pipe` (the current pipe value).

The method signature:

```go
func (r *ClaudeRunner) Run(ctx context.Context, e *Executor, p *Pipeline, idx int, cmd *Command, call CommandCall, pipe string) (string, error)
```

Body is the current `executeStage` body with these substitutions:

- `prevOutput` parameter replaced by `pipe` parameter
- `buildStageContract(p.Email, cmd, call, prevOutput)` becomes
  `buildStageContract(p.Email, cmd, call, pipe)` (T7 changes this
  further to use `pipeline_output` instead of `previous_output`)

### Executor changes

Add a `Runners` field to `Executor`:

```go
type Executor struct {
    // ... existing fields ...
    Runners map[string]Runner // keyed by runner name: "claude", "cli"
}
```

Replace `executeStage` call in `Run` loop:

```go
// Before:
result, err := e.executeStage(ctx, p, i, cmd, call)

// After:
runner, ok := e.Runners[cmd.Runner]
if !ok {
    // error: unknown runner
}
result, err := runner.Run(ctx, e, p, i, cmd, call, pipe)
```

The old `executeStage` method is removed. Its body moves to
`ClaudeRunner.Run`.

### What stays in pipeline.go

- `Executor.Run` (pipeline orchestration loop)
- `buildStageContract` (used by ClaudeRunner)
- `fireElse` (pipeline error handling)
- `resolveEnvVars`, `unionWriteSets`, `save`, `truncateLog`
- `writeSetYAML`, `escapeYAMLValue` (used by buildStageContract)

`escapeYAMLValue` and `writeSetYAML` are in `mission.go`. They stay
there.

### Registration

In `NewMailHandler` and test setup, initialize the Runners map:

```go
Runners: map[string]Runner{
    "claude": &ClaudeRunner{},
    // "cli" added in T3
},
```

---

## T3: CLIRunner Single Binary

### Files touched

- `internal/daemon/runner.go` (add CLIRunner)
- `internal/daemon/runner_test.go` (new file)

### Whitelist

```go
// BinaryWhitelist resolves and validates binary paths.
type BinaryWhitelist struct {
    Dirs []string // allowed directories (absolute paths)
}
```

Methods:

```go
// Resolve finds binary in the whitelist directories and returns the
// resolved absolute path. Returns an error if the binary is not found
// or resolves outside the whitelist.
func (w *BinaryWhitelist) Resolve(name string) (string, error)
```

Resolution logic:

1. For each dir in `w.Dirs`, check `filepath.Join(dir, name)`.
2. If the file exists and is executable, call
   `filepath.EvalSymlinks` on the path.
3. Verify the resolved path's directory is still in `w.Dirs`.
4. Return the resolved absolute path.
5. If no dir contains the binary, return error.

Default whitelist dirs (configured on Executor or passed to
CLIRunner):

- `~/.local/bin/` (expanded at startup)
- The directory containing the beadle binary (from `os.Executable()`)
- Additional dirs from daemon config (future)

### CLIRunner struct

```go
type CLIRunner struct {
    Whitelist *BinaryWhitelist
}
```

### Run method

```go
func (r *CLIRunner) Run(ctx context.Context, e *Executor, p *Pipeline, idx int, cmd *Command, call CommandCall, pipe string) (string, error)
```

Logic:

1. Resolve `cmd.Binary` via `r.Whitelist.Resolve(cmd.Binary)`.
   Error returns immediately (enters else clause in executor).

2. Build arg list:

   ```go
   args := cmd.FixedArgs (copy, not alias)

   // Collect positional args, sorted by Position.
   type posArg struct{ pos int; val string }
   var positional []posArg
   var named []string

   for _, decl := range cmd.Args {
       val, ok := call.Args[decl.Name]
       if !ok { continue }
       s := fmt.Sprint(val)
       if decl.Position > 0 {
           positional = append(positional, posArg{decl.Position, s})
       } else {
           named = append(named, fmt.Sprintf("--%s=%s", decl.Name, s))
       }
   }
   sort.Slice(positional, func(i, j int) bool {
       return positional[i].pos < positional[j].pos
   })

   args = append(args, named...)
   for _, pa := range positional {
       args = append(args, pa.val)
   }
   ```

3. Parse timeout from `cmd.Timeout` (default 30s for CLI).
   Create `context.WithTimeout(ctx, timeout)`.

4. Build `exec.CommandContext(ctx, resolvedPath, args...)`.

5. Set stdin to `strings.NewReader(pipe)` — all CLI commands receive
   the pipe on stdin.

6. Capture stdout via `io.LimitReader`. The architecture doc says to
   use `io.LimitReader` on the read side. For a single binary (not
   compound steps), use `c.StdoutPipe()` + `c.Start()` +
   `io.ReadAll(io.LimitReader(pipe, 1<<20))` + `c.Wait()`:

   ```go
   stdoutPipe, err := c.StdoutPipe()
   if err != nil { return "", err }
   c.Stderr = &stderrBuf
   if err := c.Start(); err != nil { return "", err }
   output, _ := io.ReadAll(io.LimitReader(stdoutPipe, 1<<20))
   if err := c.Wait(); err != nil { /* handle exit code */ }
   ```

7. Log stderr with command name:

   ```go
   e.Logger.Info("cli command stderr", "command", cmd.Name, "stderr", stderrBuf.String())
   ```

8. Check exit code. Nonzero returns an error. The executor's dispatch
   loop treats any error from `Runner.Run` as stage failure and enters
   the else clause.

9. Return `string(output), nil`.

### Tests (runner_test.go)

Table-driven tests using real binaries that exist on any Linux/macOS:

- `echo` for basic stdout capture
- `cat` for stdin passthrough
- `false` for nonzero exit code
- Binary not in whitelist returns error

Create a temp dir, symlink allowed binaries into it, use that dir as
the whitelist. This avoids depending on system PATH.

Test the 1MB cap with a binary that produces large output (write a
small Go test helper or use `dd` with `if=/dev/zero`).

Test arg assembly: create a wrapper script (or use `printf`) to
verify that fixed_args, positional args, and named flags arrive in
the correct order.

---

## T4: Compound Steps

### Files touched

- `internal/daemon/runner.go` (extend CLIRunner)
- `internal/daemon/runner_test.go` (compound step tests)

### Execution in CLIRunner.Run

When `len(cmd.Steps) > 0`, CLIRunner uses compound execution instead
of single-binary mode.

```go
func (r *CLIRunner) runCompound(ctx context.Context, e *Executor, cmd *Command, pipe string) (string, error)
```

Logic:

1. Parse timeout from `cmd.Timeout` (default 30s).
   `ctx, cancel := context.WithTimeout(ctx, timeout)`.
   `defer cancel()`

2. Create `io.Pipe` pairs. For N steps, create N-1 pipes:

   ```go
   pipes := make([]*io.PipeWriter, len(cmd.Steps)-1)
   readers := make([]*io.PipeReader, len(cmd.Steps)-1)
   for i := 0; i < len(cmd.Steps)-1; i++ {
       readers[i], pipes[i] = io.Pipe()
   }
   ```

3. Resolve all binaries via whitelist BEFORE starting any goroutine.
   If any resolution fails, return error immediately.

4. For each step, configure `exec.CommandContext`:
   - Step 0: stdin = `strings.NewReader(pipe)`,
     stdout = `pipes[0]` (the first pipe writer)
   - Step N (0 < N < last): stdin = `readers[N-1]`,
     stdout = `pipes[N]`
   - Last step: stdin = `readers[N-1]`,
     stdout = captured via `io.LimitReader`
   - All steps: stderr = per-step `bytes.Buffer`

5. Start all steps concurrently. Use a `sync.WaitGroup` with a shared
   `context.CancelFunc` (no `errgroup` dependency):

   ```go
   var mu sync.Mutex
   var firstErr error
   var wg sync.WaitGroup

   for i, step := range cmd.Steps {
       wg.Add(1)
       go func(i int, step Step) {
           defer wg.Done()
           err := cmds[i].Run()
           if err != nil {
               mu.Lock()
               if firstErr == nil {
                   firstErr = fmt.Errorf("step[%d] (%s): %w", i, step.Binary, err)
                   cancel() // cancel shared context
               }
               mu.Unlock()
           }
           // Close the write end so the next step's read unblocks.
           if i < len(pipes) {
               pipes[i].Close()
           }
       }(i, step)
   }
   ```

   After all goroutines start, read the final step's output:

   ```go
   output, _ := io.ReadAll(io.LimitReader(finalStdoutReader, 1<<20))
   wg.Wait()
   ```

6. After `wg.Wait()`, log each step's stderr:

   ```go
   for i, buf := range stderrBufs {
       if buf.Len() > 0 {
           e.Logger.Info("compound step stderr",
               "command", cmd.Name,
               "step", i,
               "binary", cmd.Steps[i].Binary,
               "stderr", truncateLog(buf.String(), 500))
       }
   }
   ```

7. If `firstErr != nil`, return `"", firstErr`.
8. Return `string(output), nil`.

### Pipe writer close discipline

Each step's goroutine must close its pipe writer when it finishes
(success or failure). If step[0] fails and does not close its pipe
writer, step[1] blocks forever on `stdin.Read`. The goroutine function
must `defer pipes[i].Close()` if applicable, or
`defer pipes[i].CloseWithError(err)` on failure.

Similarly, if the context is cancelled (from another step's failure),
`exec.CommandContext` kills the process, which closes its stdout. The
pipe reader sees EOF and the downstream step exits.

### Dependency: no new external packages

The architecture doc does not mandate `errgroup`. Use `sync.WaitGroup`
and a mutex-guarded first-error pattern. This avoids adding
`golang.org/x/sync` as a dependency.

### Tests

- Two-step pipe: `echo "hello" | cat` (using actual echo and cat
  binaries resolved from a temp whitelist dir with symlinks)
- Three-step pipe: verify data flows through all three
- First step fails: verify second step is cancelled, output is empty,
  error returned
- Last step fails: verify error returned
- Timeout: use `sleep 60` as a step with a 1s timeout, verify context
  cancellation
- 1MB output cap on final step

---

## T5: Process/Passthrough Pipe

### Files touched

- `internal/daemon/pipeline.go` (Executor.Run)

### Pipe variable

Add a `pipe` local variable to `Executor.Run`, initialized from the
email metadata:

```go
func (e *Executor) Run(ctx context.Context, meta EmailMeta, body string) (*Pipeline, error) {
    // ... existing Pipeline init ...

    // Initialize the pipe with email metadata JSON.
    pipeData, err := json.Marshal(map[string]string{
        "message_id":  meta.MessageID,
        "from":        meta.From,
        "subject":     meta.Subject,
        "trust_level": "trusted", // trust was verified before reaching executor
    })
    if err != nil {
        // ... handle (should not happen with string map)
    }
    pipe := string(pipeData)

    // ... plan, validate ...

    for i, call := range calls {
        // ...
        runner := e.Runners[cmd.Runner]
        result, err := runner.Run(ctx, e, p, i, cmd, call, pipe)
        if err != nil {
            // ... fail, fireElse ...
        }

        if cmd.Mode == "process" {
            // T6 adds schema validation here, between result and assignment
            pipe = result
        }
        // passthrough: pipe unchanged, result logged only

        p.Results = append(p.Results, result)
        e.save(p)
    }

    // Auto-reply receives current pipe value (T7)
    // ...
}
```

### How pipe is passed to each runner

**ClaudeRunner:** pipe value goes into the mission contract as
`inputs.pipeline_output`. See T7 for the contract format change.
The ClaudeRunner.Run signature already receives `pipe string`.

**CLIRunner:** pipe value is written to the command's stdin
(`strings.NewReader(pipe)`). For compound steps, step[0] receives the
pipe on stdin (per T4). The CLIRunner.Run signature already receives
`pipe string`.

### Pipeline.Results

`p.Results` continues to record every stage's output (both process and
passthrough). This is the audit trail. The pipe is the data flow; Results
is the log.

### trust_level field

The initial pipe sets `trust_level` to `"trusted"`. By the time the
executor runs, the MailHandler has already verified the sender's trust
level (PGP verified or Proton E2E trusted). The pipe value does not
need to carry the actual trust level enum — it is a hint for the
Claude worker to understand the email's provenance. If a more precise
value is needed later, the MailHandler can pass it through `EmailMeta`.

---

## T6: Output Schema Validation

### Files touched

- `go.mod` (add `github.com/santhosh-tekuri/jsonschema/v6`)
- `internal/daemon/pipeline.go` (validation after process-mode stages)
- `internal/daemon/schema.go` (new file: schema compilation + validation)
- `internal/daemon/schema_test.go` (new file)

### New dependency

```bash
go get github.com/santhosh-tekuri/jsonschema/v6
```

### schema.go

```go
// CompileSchema compiles an output_schema map into a *jsonschema.Schema.
// Returns nil if schema is the string "text" (no validation needed).
// Returns an error if compilation fails.
func CompileSchema(outputSchema any) (*jsonschema.Schema, error)
```

Logic:

```go
switch v := outputSchema.(type) {
case string:
    if v == "text" { return nil, nil }
    return nil, fmt.Errorf("unexpected output_schema string %q", v)
case map[string]any:
    // Marshal the map to JSON, then compile.
    data, err := json.Marshal(v)
    if err != nil { return nil, err }
    c := jsonschema.NewCompiler()
    // Add the schema as an in-memory resource.
    if err := c.AddResource("schema.json", bytes.NewReader(data)); err != nil {
        return nil, fmt.Errorf("compile output_schema: %w", err)
    }
    return c.Compile("schema.json")
default:
    return nil, fmt.Errorf("output_schema has unexpected type %T", v)
}
```

```go
// ValidateOutput checks that output conforms to schema.
// If schema is nil (text mode), validation is skipped.
// Returns nil on success, a descriptive error on failure.
func ValidateOutput(schema *jsonschema.Schema, output string) error
```

Logic:

```go
if schema == nil { return nil }

// First: is it valid JSON?
var v any
if err := json.Unmarshal([]byte(output), &v); err != nil {
    return fmt.Errorf("output is not valid JSON: %w", err)
}

// Validate against schema.
if err := schema.Validate(v); err != nil {
    return fmt.Errorf("output does not match schema: %w", err)
}
return nil
```

### Integration in Executor.Run

Pre-compile all schemas at the start of `Executor.Run`, after plan
validation:

```go
schemas := make(map[string]*jsonschema.Schema, len(calls))
for _, call := range calls {
    cmd := e.Commands[call.Command]
    schema, err := CompileSchema(cmd.OutputSchema)
    if err != nil {
        // fail pipeline
    }
    schemas[call.Command] = schema
}
```

After each stage, if `cmd.Mode == "process"`:

```go
if cmd.Mode == "process" {
    if err := ValidateOutput(schemas[call.Command], result); err != nil {
        e.Logger.Warn("output schema validation failed",
            "pipeline", p.ID, "stage", i,
            "command", call.Command, "error", err)
        p.Status = "failed"
        p.Error = fmt.Sprintf("stage %d (%s): output validation: %v", i, call.Command, err)
        e.save(p)
        e.fireElse(p)
        return p, fmt.Errorf(...)
    }
    pipe = result
}
```

Passthrough-mode stages skip validation entirely — their output never
enters the pipe.

### Tests (schema_test.go)

Table-driven:

- Valid JSON matching schema returns no error
- Valid JSON not matching schema (missing required field) returns error
- Invalid JSON returns error
- Text mode (nil schema) returns no error regardless of output
- Schema compilation from map[string]any succeeds
- Schema compilation from "text" returns nil schema
- Schema compilation from invalid type returns error

### Rejected: validating at load time

Compiling schemas at load time (`LoadCommands`) would catch schema
errors earlier. But the current `LoadCommands` returns loaded commands
without schema objects. Changing the return type to carry compiled
schemas adds complexity to the loader's API. Instead, compile at
execution time (start of `Executor.Run`) — schema errors fail the
pipeline early, before any stage runs.

---

## T7: Auto-Reply Wiring

### Files touched

- `internal/daemon/pipeline.go` (buildStageContract, auto-reply block,
  fireElse)
- `internal/daemon/pipeline_test.go` (test assertions)

### buildStageContract changes

Replace `previous_output` with `pipeline_output`:

```go
func buildStageContract(meta EmailMeta, cmd *Command, call CommandCall, pipe string) string {
    pipeValue := "none"
    if pipe != "" {
        pipeValue = escapeYAMLValue(pipe)
    }

    argsYAML := ""
    for k, v := range call.Args {
        argsYAML += fmt.Sprintf("      %s: %s\n", k, escapeYAMLValue(fmt.Sprint(v)))
    }

    return fmt.Sprintf(`leader: claude
worker: bwk
evaluator:
  handle: mdm
inputs:
  trigger:
    type: email
    message_id: %s
    from: %s
    subject: %s
  args:
%s  pipeline_output: %s
write_set:
  - %s
success_criteria:
  - %s
budget:
  rounds: %d
  reflection_after_each: %v
`,
        escapeYAMLValue(meta.MessageID),
        escapeYAMLValue(meta.From),
        escapeYAMLValue(meta.Subject),
        argsYAML,
        pipeValue,
        writeSetYAML(cmd.WriteSet),
        escapeYAMLValue(cmd.Prompt),
        cmd.Budget.Rounds,
        cmd.Budget.ReflectionAfterEach,
    )
}
```

Key change: `previous_output` becomes `pipeline_output`. The parameter
name changes from `prevOutput` to `pipe` to reflect that it is the
current pipe value, not the previous stage's output.

### Auto-reply block changes

In `Executor.Run`, the auto-reply block changes from:

```go
replyCall := CommandCall{
    Command: "reply",
    Args: map[string]any{
        "to":      email.ExtractEmailAddress(p.Email.From),
        "message": p.Results[len(p.Results)-1],
    },
}
```

To:

```go
replyCall := CommandCall{
    Command: "reply",
    Args: map[string]any{
        "to": email.ExtractEmailAddress(p.Email.From),
    },
}
```

The `message` arg is removed. The pipe value is passed via the
`pipe` parameter to `Runner.Run`, which puts it in
`inputs.pipeline_output` in the mission contract. The reply command's
prompt instructs the Claude worker to read `inputs.pipeline_output`
from the mission contract and format it as a reply.

The runner dispatch in the auto-reply block:

```go
if replyCmd, ok := e.Commands["reply"]; ok {
    replyCall := CommandCall{
        Command: "reply",
        Args: map[string]any{
            "to": email.ExtractEmailAddress(p.Email.From),
        },
    }
    if err := ValidateArgs(replyCmd, replyCall.Args); err == nil {
        runner := e.Runners[replyCmd.Runner]
        replyResult, err := runner.Run(ctx, e, p, len(p.Commands), replyCmd, replyCall, pipe)
        // ... error handling unchanged ...
    }
}
```

### reply command migration

The `reply` command definition loses the `message` required arg:

```yaml
name: reply
runner: claude
mode: process
description: Send the pipe contents back to the originator
mcp_servers: [ethos, beadle-email]
output_schema: text
timeout: 5m
args:
  - name: to
    type: string
    required: true
prompt: |
  Send the pipeline output as a reply to the originator.
  Read inputs.pipeline_output from the mission contract — it contains
  the data to send. Format it as a readable email body before sending.
  Use beadle-email tools to send the reply.
```

### fireElse changes

The else handler currently constructs:

```go
replyCall := CommandCall{
    Command: "reply",
    Args: map[string]any{
        "to":      email.ExtractEmailAddress(p.Email.From),
        "message": "Your request could not be completed. Reference: pipeline-" + p.ID,
    },
}
```

Under v2, the error message goes through the pipe parameter:

```go
func (e *Executor) fireElse(p *Pipeline) {
    // ... logging unchanged ...

    replyCmd, ok := e.Commands["reply"]
    if !ok { return }

    elsePipe := "Your request could not be completed. Reference: pipeline-" + p.ID

    replyCall := CommandCall{
        Command: "reply",
        Args: map[string]any{
            "to": email.ExtractEmailAddress(p.Email.From),
        },
    }
    if err := ValidateArgs(replyCmd, replyCall.Args); err != nil { return }

    runner, ok := e.Runners[replyCmd.Runner]
    if !ok { return }

    ctx := context.Background()
    _, err := runner.Run(ctx, e, p, -1, replyCmd, replyCall, elsePipe)
    if err != nil {
        e.Logger.Warn("else reply failed", "pipeline", p.ID, "error", err)
    }
}
```

The fixed-text error string is the pipe value. Internal pipeline state
(the actual pipe at failure time) is not leaked.

### testCommands() reply entry

Update the reply command in `testCommands()` to remove the `message`
arg:

```go
"reply": {
    Name:         "reply",
    Runner:       "claude",
    Mode:         "process",
    Prompt:       "Reply to the sender with the pipeline output",
    OutputSchema: "text",
    Args: []CommandArg{
        {Name: "to", Type: "string", Required: true},
    },
    Budget: struct {
        Rounds              int  `yaml:"rounds"`
        ReflectionAfterEach bool `yaml:"reflection_after_each"`
    }{Rounds: 1},
    WriteSet:   []string{"daemon output"},
    MCPServers: []string{"ethos", "beadle-email"},
},
```

### Test updates

`TestExecutor_AutoReplyArgs`: update to not expect `message` in
the reply args. Instead verify that the mockSpawner received
the correct pipe value.

`TestExecutor_TwoStagePipeline`: verify pipe flows through process
stages (stage output becomes next stage's pipe).

`TestExecutor_ElseReply`: verify the fixed error text is passed as
the pipe (requires mockSpawner to capture the pipe value — extend
the mock interface to record it).

To capture pipe values in tests, the mockSpawner must implement the
`Runner` interface (not just `Spawner`). Create a `mockClaudeRunner`
that records the pipe parameter:

```go
type mockClaudeRunner struct {
    calls []mockRunnerCall
    results []WorkerResult
    errs    []error
    idx     int
}

type mockRunnerCall struct {
    Idx   int
    Cmd   string
    Pipe  string
    Args  map[string]any
}

func (m *mockClaudeRunner) Run(ctx context.Context, e *Executor, p *Pipeline, idx int, cmd *Command, call CommandCall, pipe string) (string, error) {
    m.calls = append(m.calls, mockRunnerCall{Idx: idx, Cmd: cmd.Name, Pipe: pipe, Args: call.Args})
    i := m.idx
    m.idx++
    if i < len(m.errs) && m.errs[i] != nil {
        return "", m.errs[i]
    }
    if i < len(m.results) {
        return m.results[i].Output, nil
    }
    return "ok", nil
}
```

This replaces `mockSpawner` in pipeline tests. The existing
`mockSpawner` stays for `spawner_test.go` / `handler_test.go` tests
that exercise the Spawner interface directly.

---

## Migration Plan

### Commit order

All changes within T1 must be in a single commit (struct + validator +
YAML + test fixtures). Otherwise `KnownFields(true)` or missing struct
fields break compilation.

Recommended commit sequence:

1. **T1:** `feat(daemon): command struct v2 — runner, mode, output_schema`
   - command.go struct + validator
   - command_test.go YAML + assertions
   - pipeline_test.go testCommands()

2. **T2:** `feat(daemon): Runner interface and ClaudeRunner`
   - runner.go (new)
   - pipeline.go executor dispatch refactor

3. **T3:** `feat(daemon): CLIRunner with binary whitelist`
   - runner.go (CLIRunner)
   - runner_test.go (new)

4. **T5:** `feat(daemon): process/passthrough pipe in executor`
   - pipeline.go (pipe variable, mode dispatch)

5. **T7:** `feat(daemon): auto-reply reads pipe, not message arg`
   - pipeline.go (buildStageContract, auto-reply, fireElse)
   - pipeline_test.go (updated assertions)

6. **T6:** `feat(daemon): output schema validation with jsonschema/v6`
   - go.mod (new dependency)
   - schema.go (new)
   - schema_test.go (new)
   - pipeline.go (validation in executor loop)

7. **T4:** `feat(daemon): compound CLI steps with io.Pipe chaining`
   - runner.go (runCompound)
   - runner_test.go (compound tests)

T4 is last because it depends on T3 (CLIRunner) and is the most
complex. T6 can be done before or after T4 — no dependency between
them.

### Backward compatibility

There is no backward compatibility surface. Command YAML files are
local, GPG-signed, and not distributed. The struct change + YAML
migration is atomic within one commit. No external consumers of the
Go API (the package is `internal/`).

### Risk: YAML decode of output_schema

The `any` type in `OutputSchema any` decodes YAML values as:

- bare string `text` becomes Go `string`
- mapping becomes Go `map[string]any`
- bare number becomes Go `int` or `float64`
- bare boolean becomes Go `bool`
- sequence becomes Go `[]any`

The type-switch in `validateCommand` handles all these cases and
rejects anything other than `string("text")` or `map[string]any`.
This is safe with `gopkg.in/yaml.v3`.

### Risk: existing tests

All existing pipeline tests use `mockSpawner` which implements
`Spawner`, not `Runner`. After T2, the executor dispatches via
`Runner`, not `Spawner`. All pipeline tests must be updated to use
`mockClaudeRunner` (or a mock that implements `Runner`). The
`mockSpawner` type stays for handler_test.go tests that test
`WorkerSpawner` directly.

The `Executor.Spawner` field is removed after T2 (the ClaudeRunner
uses the Spawner internally). But handler_test.go creates Executor
directly with a `Spawner`. Two options:

**Option A:** `ClaudeRunner` holds a `Spawner` reference. The
`Executor` does not hold `Spawner` directly. Tests create a
`ClaudeRunner` wrapping a `mockSpawner`.

**Option B:** `Executor` holds both `Spawner` (for ClaudeRunner) and
`Runners` (for dispatch). ClaudeRunner reads Spawner from Executor.

Option A is cleaner. `ClaudeRunner` struct:

```go
type ClaudeRunner struct {
    Spawner   Spawner
    Missions  MissionCreator
    Templates *MissionTemplate
    Registry  map[string]MCPServerConfig
}
```

This moves `Spawner`, `Missions`, `Templates`, and `Registry` from
`Executor` to `ClaudeRunner`. The `Executor` holds only:

```go
type Executor struct {
    Planner  Planner
    Commands map[string]*Command
    Runners  map[string]Runner
    Store    *PipelineStore
    Logger   *slog.Logger
}
```

Tests construct:

```go
runner := &mockClaudeRunner{results: [...], errs: [...]}
exec := &Executor{
    Planner:  &StubPlanner{...},
    Commands: testCommands(),
    Runners:  map[string]Runner{"claude": runner},
    Logger:   testLogger(),
}
```

This is the recommended approach. It simplifies the Executor and makes
it agnostic to runner internals.
