package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestExecutor_HybridPipeline verifies a pipeline with both claude and cli
// stages. Stage 0 is a claude runner (mock), stage 1 is a real cli runner
// using a whitelisted binary. The pipe flows correctly across runner boundaries.
func TestExecutor_HybridPipeline(t *testing.T) {
	_, wl := setupWhitelist(t, "cat")
	claudeRunner := &mockClaudeRunner{
		results: []WorkerResult{
			{Output: `{"title":"hybrid test"}`},
			{Output: "reply sent"},
		},
	}

	cmds := map[string]*Command{
		"summarize": {
			Name:   "summarize",
			Runner: "claude",
			Mode:   "process",
			Prompt: "Summarize",
			OutputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"title": map[string]any{"type": "string"},
				},
			},
			Budget: struct {
				Rounds              int  `yaml:"rounds"`
				ReflectionAfterEach bool `yaml:"reflection_after_each"`
			}{Rounds: 1},
		},
		"passthru-cat": {
			Name:         "passthru-cat",
			Runner:       "cli",
			Mode:         "process",
			Binary:       "cat",
			OutputSchema: "text",
			Timeout:      "5s",
		},
		"reply": testCommands()["reply"],
	}

	runners := map[string]Runner{
		"claude": claudeRunner,
		"cli":    &CLIRunner{Whitelist: wl},
	}

	exec := &Executor{
		Planner: &StubPlanner{
			Result: []CommandCall{
				{Command: "summarize", Args: map[string]any{}},
				{Command: "passthru-cat", Args: map[string]any{}},
			},
		},
		Commands: cmds,
		Runners:  runners,
		Logger:   testLogger(),
	}

	meta := EmailMeta{MessageID: "h1", From: "jim@test.com", Subject: "Hybrid"}
	p, err := exec.Run(context.Background(), meta, "body")
	require.NoError(t, err)

	assert.Equal(t, "completed", p.Status)
	// Stage 0 (claude/process) produces JSON, stage 1 (cli/process) cats it through.
	require.Len(t, p.Results, 3) // 2 stages + auto-reply
	assert.Equal(t, `{"title":"hybrid test"}`, p.Results[0])
	assert.Equal(t, `{"title":"hybrid test"}`, p.Results[1])

	// Claude runner called for stage 0 and reply. CLI runner handled stage 1.
	assert.Len(t, claudeRunner.calls, 2)
	// The reply should receive the cli stage's output as pipe.
	assert.Equal(t, `{"title":"hybrid test"}`, claudeRunner.calls[1].Pipe)
}

// TestExecutor_PassthroughDataSurvival verifies that a passthrough stage
// does not alter the pipe. Stage 0 is process (produces JSON), stage 1 is
// passthrough (side-effect), stage 2 is process (receives stage 0's output).
func TestExecutor_PassthroughDataSurvival(t *testing.T) {
	stage0Output := `{"title":"preserved","summary":"intact"}`
	runner := &mockClaudeRunner{
		results: []WorkerResult{
			{Output: stage0Output},
			{Output: "side-effect done"},
			{Output: "final output"},
			{Output: "reply sent"},
		},
	}

	cmds := map[string]*Command{
		"summarize": {
			Name:   "summarize",
			Runner: "claude",
			Mode:   "process",
			Prompt: "Summarize",
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
		},
		"notify": {
			Name:         "notify",
			Runner:       "claude",
			Mode:         "passthrough",
			Prompt:       "Notify",
			OutputSchema: "text",
			Budget: struct {
				Rounds              int  `yaml:"rounds"`
				ReflectionAfterEach bool `yaml:"reflection_after_each"`
			}{Rounds: 1},
		},
		"transform": {
			Name:         "transform",
			Runner:       "claude",
			Mode:         "process",
			Prompt:       "Transform",
			OutputSchema: "text",
			Budget: struct {
				Rounds              int  `yaml:"rounds"`
				ReflectionAfterEach bool `yaml:"reflection_after_each"`
			}{Rounds: 1},
		},
		"reply": testCommands()["reply"],
	}

	exec := &Executor{
		Planner: &StubPlanner{
			Result: []CommandCall{
				{Command: "summarize", Args: map[string]any{}},
				{Command: "notify", Args: map[string]any{}},
				{Command: "transform", Args: map[string]any{}},
			},
		},
		Commands: cmds,
		Runners:  testRunners(runner),
		Logger:   testLogger(),
	}

	meta := EmailMeta{MessageID: "pt1", From: "jim@test.com", Subject: "Passthrough"}
	p, err := exec.Run(context.Background(), meta, "body")
	require.NoError(t, err)

	assert.Equal(t, "completed", p.Status)
	// 3 stages + auto-reply.
	require.Len(t, p.Results, 4)

	// Stage 2 (transform) should have received stage 0's output, not stage 1's,
	// because stage 1 is passthrough and does not modify the pipe.
	require.Len(t, runner.calls, 4)
	assert.Equal(t, stage0Output, runner.calls[2].Pipe, "stage 2 must receive stage 0 output, not stage 1")
}

// TestExecutor_SchemaValidationRejection verifies that when a process-mode
// stage produces output that does not match its declared schema, the pipeline
// fails and the pipe remains unchanged (fireElse fires).
func TestExecutor_SchemaValidationRejection(t *testing.T) {
	runner := &mockClaudeRunner{
		results: []WorkerResult{
			// Missing required "title" field.
			{Output: `{"summary":"no title"}`},
			{Output: "else reply sent"},
		},
	}

	cmds := map[string]*Command{
		"strict": {
			Name:   "strict",
			Runner: "claude",
			Mode:   "process",
			Prompt: "Strict schema",
			OutputSchema: map[string]any{
				"type":     "object",
				"required": []any{"title"},
				"properties": map[string]any{
					"title": map[string]any{"type": "string"},
				},
			},
			Budget: struct {
				Rounds              int  `yaml:"rounds"`
				ReflectionAfterEach bool `yaml:"reflection_after_each"`
			}{Rounds: 1},
		},
		"reply": testCommands()["reply"],
	}

	exec := &Executor{
		Planner: &StubPlanner{
			Result: []CommandCall{
				{Command: "strict", Args: map[string]any{}},
			},
		},
		Commands: cmds,
		Runners:  testRunners(runner),
		Logger:   testLogger(),
	}

	meta := EmailMeta{MessageID: "sv1", From: "jim@test.com", Subject: "Schema fail"}
	p, err := exec.Run(context.Background(), meta, "body")
	require.Error(t, err)

	assert.Equal(t, "failed", p.Status)
	assert.Contains(t, p.Error, "output validation")
	// Stage call + else reply.
	assert.Len(t, runner.calls, 2)
	assert.Equal(t, "reply", runner.calls[1].Cmd)
}

// TestExecutor_TextModeBypass verifies that a process-mode stage with
// output_schema: text passes arbitrary text without JSON validation.
func TestExecutor_TextModeBypass(t *testing.T) {
	arbitraryText := "This is not JSON at all. <html>whatever</html>"
	runner := &mockClaudeRunner{
		results: []WorkerResult{
			{Output: arbitraryText},
			{Output: "reply sent"},
		},
	}

	cmds := map[string]*Command{
		"freeform": {
			Name:         "freeform",
			Runner:       "claude",
			Mode:         "process",
			Prompt:       "Freeform output",
			OutputSchema: "text",
			Budget: struct {
				Rounds              int  `yaml:"rounds"`
				ReflectionAfterEach bool `yaml:"reflection_after_each"`
			}{Rounds: 1},
		},
		"reply": testCommands()["reply"],
	}

	exec := &Executor{
		Planner: &StubPlanner{
			Result: []CommandCall{
				{Command: "freeform", Args: map[string]any{}},
			},
		},
		Commands: cmds,
		Runners:  testRunners(runner),
		Logger:   testLogger(),
	}

	meta := EmailMeta{MessageID: "tm1", From: "jim@test.com", Subject: "Text mode"}
	p, err := exec.Run(context.Background(), meta, "body")
	require.NoError(t, err)

	assert.Equal(t, "completed", p.Status)
	require.Len(t, p.Results, 2)
	assert.Equal(t, arbitraryText, p.Results[0])
	// Reply receives the text as pipe.
	assert.Equal(t, arbitraryText, runner.calls[1].Pipe)
}

// TestCLIRunner_CompoundMidChainFailure verifies that in a 3-step compound
// command where step 1 fails, step 2 (the third step) never runs and the
// error references the failing step.
func TestCLIRunner_CompoundMidChainFailure(t *testing.T) {
	_, wl := setupWhitelist(t, "echo", "false", "cat")
	runner := &CLIRunner{Whitelist: wl}

	cmd := &Command{
		Name:   "test-mid-fail",
		Runner: "cli",
		Mode:   "process",
		Steps: []Step{
			{Binary: "echo", FixedArgs: []string{"-n", "hello"}, Stdin: "pipe"},
			{Binary: "false", Stdin: "stdout"},
			{Binary: "cat", Stdin: "stdout"},
		},
		OutputSchema: "text",
		Timeout:      "5s",
	}
	call := CommandCall{Command: "test-mid-fail", Args: map[string]any{}}
	p := testPipeline()

	_, err := runner.Run(context.Background(), &Executor{Logger: testLogger()}, p, 0, cmd, call, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "step[1]")
	assert.Contains(t, err.Error(), "false")
}

// TestCLIRunner_WhitelistDeletedBinary verifies that a binary that passes
// load-time check but is deleted before execution fails at execution time.
func TestCLIRunner_WhitelistDeletedBinary(t *testing.T) {
	dir := t.TempDir()
	binPath := filepath.Join(dir, "ephemeral")
	require.NoError(t, os.WriteFile(binPath, []byte("#!/bin/sh\necho ok"), 0o755))

	wl := &BinaryWhitelist{Dirs: []string{dir}}
	runner := &CLIRunner{Whitelist: wl}

	// Verify the binary resolves before deletion.
	_, err := wl.Resolve("ephemeral")
	require.NoError(t, err)

	// Delete the binary.
	require.NoError(t, os.Remove(binPath))

	cmd := &Command{
		Name:         "test-deleted",
		Runner:       "cli",
		Mode:         "process",
		Binary:       "ephemeral",
		OutputSchema: "text",
		Timeout:      "5s",
	}
	call := CommandCall{Command: "test-deleted", Args: map[string]any{}}
	p := testPipeline()

	_, err = runner.Run(context.Background(), &Executor{Logger: testLogger()}, p, 0, cmd, call, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found in whitelist")
}

// TestExecutor_PipeInitialValue verifies that the initial pipe contains
// correct EmailMeta JSON with the expected keys.
func TestExecutor_PipeInitialValue(t *testing.T) {
	runner := &mockClaudeRunner{
		results: []WorkerResult{
			{Output: "done"},
			{Output: "reply sent"},
		},
	}

	cmds := map[string]*Command{
		"inspect": {
			Name:         "inspect",
			Runner:       "claude",
			Mode:         "passthrough",
			Prompt:       "Inspect pipe",
			OutputSchema: "text",
			Budget: struct {
				Rounds              int  `yaml:"rounds"`
				ReflectionAfterEach bool `yaml:"reflection_after_each"`
			}{Rounds: 1},
		},
		"reply": testCommands()["reply"],
	}

	exec := &Executor{
		Planner: &StubPlanner{
			Result: []CommandCall{
				{Command: "inspect", Args: map[string]any{}},
			},
		},
		Commands: cmds,
		Runners:  testRunners(runner),
		Logger:   testLogger(),
	}

	meta := EmailMeta{
		MessageID: "msg-42",
		From:      "alice@example.com",
		Subject:   "Test initial pipe",
	}
	_, err := exec.Run(context.Background(), meta, "body")
	require.NoError(t, err)

	// The first stage receives the initial pipe.
	require.Len(t, runner.calls, 2)
	initialPipe := runner.calls[0].Pipe

	var pipeData map[string]string
	require.NoError(t, json.Unmarshal([]byte(initialPipe), &pipeData))
	assert.Equal(t, "msg-42", pipeData["message_id"])
	assert.Equal(t, "alice@example.com", pipeData["from"])
	assert.Equal(t, "Test initial pipe", pipeData["subject"])
	assert.Equal(t, "trusted", pipeData["trust_level"])
}

// TestExecutor_EmptyPlanFiresElse verifies that when the planner returns
// no commands, the else handler fires with the reply command.
func TestExecutor_EmptyPlanFiresElse(t *testing.T) {
	runner := &mockClaudeRunner{
		results: []WorkerResult{
			{Output: "else reply sent"},
		},
	}

	exec := &Executor{
		Planner:  &StubPlanner{Result: []CommandCall{}},
		Commands: testCommands(),
		Runners:  testRunners(runner),
		Logger:   testLogger(),
	}

	meta := EmailMeta{MessageID: "ep1", From: "carol@test.com", Subject: "Empty plan"}
	p, err := exec.Run(context.Background(), meta, "body")
	require.Error(t, err)

	assert.Equal(t, "failed", p.Status)
	assert.Contains(t, p.Error, "empty")
	// Else handler fires a reply.
	require.Len(t, runner.calls, 1)
	assert.Equal(t, "reply", runner.calls[0].Cmd)
}

// TestExecutor_FireElsePipeValue verifies that the else handler passes the
// fixed-text error string as the pipe to the reply command, not the
// current pipeline pipe state.
func TestExecutor_FireElsePipeValue(t *testing.T) {
	runner := &mockClaudeRunner{
		errs: []error{
			fmt.Errorf("stage blew up"),
		},
		results: []WorkerResult{
			{}, // stage fails
			{Output: "else reply"},
		},
	}

	cmds := testCommands()
	exec := &Executor{
		Planner: &StubPlanner{
			Result: []CommandCall{
				{Command: "greet", Args: map[string]any{}},
			},
		},
		Commands: cmds,
		Runners:  testRunners(runner),
		Logger:   testLogger(),
	}

	meta := EmailMeta{MessageID: "fe1", From: "dave@test.com", Subject: "Else pipe"}
	p, err := exec.Run(context.Background(), meta, "body")
	require.Error(t, err)

	assert.Equal(t, "failed", p.Status)
	// 1 stage call (failed) + 1 else reply call.
	require.Len(t, runner.calls, 2)
	assert.Equal(t, "reply", runner.calls[1].Cmd)

	// The else reply pipe must be the fixed-text error, not the email metadata pipe.
	elsePipe := runner.calls[1].Pipe
	assert.Contains(t, elsePipe, "Your request could not be completed")
	assert.Contains(t, elsePipe, "pipeline-"+p.ID)
	// It must NOT contain the email metadata JSON.
	assert.NotContains(t, elsePipe, "message_id")
}

// TestExecutor_UnknownRunner verifies that a command with an unregistered
// runner fails at execution time with a descriptive error.
func TestExecutor_UnknownRunner(t *testing.T) {
	runner := &mockClaudeRunner{
		results: []WorkerResult{
			{Output: "else reply"},
		},
	}

	cmds := map[string]*Command{
		"badrunner": {
			Name:         "badrunner",
			Runner:       "http",
			Mode:         "process",
			OutputSchema: "text",
		},
		"reply": testCommands()["reply"],
	}

	exec := &Executor{
		Planner: &StubPlanner{
			Result: []CommandCall{
				{Command: "badrunner", Args: map[string]any{}},
			},
		},
		Commands: cmds,
		Runners:  testRunners(runner),
		Logger:   testLogger(),
	}

	meta := EmailMeta{MessageID: "ur1", From: "x@test.com", Subject: "Unknown runner"}
	p, err := exec.Run(context.Background(), meta, "body")
	require.Error(t, err)

	assert.Equal(t, "failed", p.Status)
	assert.Contains(t, p.Error, "unknown runner")
	assert.Contains(t, p.Error, "http")
}

// TestExecutor_SchemaValidationInvalidJSON verifies that a process-mode stage
// producing invalid JSON (not just schema-mismatched) fails with a clear error.
func TestExecutor_SchemaValidationInvalidJSON(t *testing.T) {
	runner := &mockClaudeRunner{
		results: []WorkerResult{
			{Output: "this is not json {{{"},
			{Output: "else reply"},
		},
	}

	cmds := map[string]*Command{
		"strict": {
			Name:   "strict",
			Runner: "claude",
			Mode:   "process",
			Prompt: "Strict",
			OutputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"title": map[string]any{"type": "string"},
				},
			},
			Budget: struct {
				Rounds              int  `yaml:"rounds"`
				ReflectionAfterEach bool `yaml:"reflection_after_each"`
			}{Rounds: 1},
		},
		"reply": testCommands()["reply"],
	}

	exec := &Executor{
		Planner: &StubPlanner{
			Result: []CommandCall{
				{Command: "strict", Args: map[string]any{}},
			},
		},
		Commands: cmds,
		Runners:  testRunners(runner),
		Logger:   testLogger(),
	}

	meta := EmailMeta{MessageID: "ij1", From: "x@test.com", Subject: "Invalid JSON"}
	p, err := exec.Run(context.Background(), meta, "body")
	require.Error(t, err)

	assert.Equal(t, "failed", p.Status)
	assert.Contains(t, p.Error, "output validation")
}

// TestExecutor_ProcessModeUpdatedPipe verifies that after a process-mode
// stage, the pipe is updated to the stage's output for subsequent stages.
func TestExecutor_ProcessModeUpdatesPipe(t *testing.T) {
	runner := &mockClaudeRunner{
		results: []WorkerResult{
			{Output: `{"title":"first"}`},
			{Output: `{"title":"second"}`},
			{Output: "reply sent"},
		},
	}

	cmds := map[string]*Command{
		"stage-a": {
			Name:   "stage-a",
			Runner: "claude",
			Mode:   "process",
			Prompt: "A",
			OutputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"title": map[string]any{"type": "string"},
				},
			},
			Budget: struct {
				Rounds              int  `yaml:"rounds"`
				ReflectionAfterEach bool `yaml:"reflection_after_each"`
			}{Rounds: 1},
		},
		"stage-b": {
			Name:   "stage-b",
			Runner: "claude",
			Mode:   "process",
			Prompt: "B",
			OutputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"title": map[string]any{"type": "string"},
				},
			},
			Budget: struct {
				Rounds              int  `yaml:"rounds"`
				ReflectionAfterEach bool `yaml:"reflection_after_each"`
			}{Rounds: 1},
		},
		"reply": testCommands()["reply"],
	}

	exec := &Executor{
		Planner: &StubPlanner{
			Result: []CommandCall{
				{Command: "stage-a", Args: map[string]any{}},
				{Command: "stage-b", Args: map[string]any{}},
			},
		},
		Commands: cmds,
		Runners:  testRunners(runner),
		Logger:   testLogger(),
	}

	meta := EmailMeta{MessageID: "pu1", From: "x@test.com", Subject: "Pipe update"}
	p, err := exec.Run(context.Background(), meta, "body")
	require.NoError(t, err)

	assert.Equal(t, "completed", p.Status)
	require.Len(t, runner.calls, 3)

	// Stage-b receives stage-a's output as pipe.
	assert.Equal(t, `{"title":"first"}`, runner.calls[1].Pipe)
	// Reply receives stage-b's output as pipe.
	assert.Equal(t, `{"title":"second"}`, runner.calls[2].Pipe)
}

// TestCLIRunner_CompoundResolveFailure verifies that if a binary in a
// compound step fails whitelist resolution, the error is returned before
// any step starts.
func TestCLIRunner_CompoundResolveFailure(t *testing.T) {
	_, wl := setupWhitelist(t, "echo")
	runner := &CLIRunner{Whitelist: wl}

	cmd := &Command{
		Name:   "test-resolve-fail",
		Runner: "cli",
		Mode:   "process",
		Steps: []Step{
			{Binary: "echo", FixedArgs: []string{"hello"}, Stdin: "pipe"},
			{Binary: "nonexistent-xyz", Stdin: "stdout"},
		},
		OutputSchema: "text",
		Timeout:      "5s",
	}
	call := CommandCall{Command: "test-resolve-fail", Args: map[string]any{}}
	p := testPipeline()

	_, err := runner.Run(context.Background(), &Executor{Logger: testLogger()}, p, 0, cmd, call, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "step[1]")
	assert.Contains(t, err.Error(), "not found in whitelist")
}

// TestExecutor_PipelineStoreIntegration verifies that the pipeline state
// is persisted at each save point when a PipelineStore is configured.
func TestExecutor_PipelineStoreIntegration(t *testing.T) {
	dir := t.TempDir()
	store := &PipelineStore{Dir: dir}

	runner := &mockClaudeRunner{
		results: []WorkerResult{
			{Output: "done"},
			{Output: "reply sent"},
		},
	}

	cmds := map[string]*Command{
		"greet": testCommands()["greet"],
		"reply": testCommands()["reply"],
	}

	exec := &Executor{
		Planner: &StubPlanner{
			Result: []CommandCall{
				{Command: "greet", Args: map[string]any{}},
			},
		},
		Commands: cmds,
		Runners:  testRunners(runner),
		Store:    store,
		Logger:   testLogger(),
	}

	meta := EmailMeta{MessageID: "ps1", From: "x@test.com", Subject: "Store test"}
	p, err := exec.Run(context.Background(), meta, "body")
	require.NoError(t, err)

	assert.Equal(t, "completed", p.Status)

	// The pipeline file should exist.
	pipelinePath := filepath.Join(dir, p.ID+".json")
	_, statErr := os.Stat(pipelinePath)
	assert.NoError(t, statErr, "pipeline state file should be persisted")
}

// TestExecutor_MultiplePassthroughStages verifies that consecutive passthrough
// stages leave the pipe completely unchanged.
func TestExecutor_MultiplePassthroughStages(t *testing.T) {
	runner := &mockClaudeRunner{
		results: []WorkerResult{
			{Output: "side-effect-1"},
			{Output: "side-effect-2"},
			{Output: "side-effect-3"},
			{Output: "reply sent"},
		},
	}

	cmds := map[string]*Command{
		"notify1": {
			Name: "notify1", Runner: "claude", Mode: "passthrough",
			Prompt: "N1", OutputSchema: "text",
			Budget: struct {
				Rounds              int  `yaml:"rounds"`
				ReflectionAfterEach bool `yaml:"reflection_after_each"`
			}{Rounds: 1},
		},
		"notify2": {
			Name: "notify2", Runner: "claude", Mode: "passthrough",
			Prompt: "N2", OutputSchema: "text",
			Budget: struct {
				Rounds              int  `yaml:"rounds"`
				ReflectionAfterEach bool `yaml:"reflection_after_each"`
			}{Rounds: 1},
		},
		"notify3": {
			Name: "notify3", Runner: "claude", Mode: "passthrough",
			Prompt: "N3", OutputSchema: "text",
			Budget: struct {
				Rounds              int  `yaml:"rounds"`
				ReflectionAfterEach bool `yaml:"reflection_after_each"`
			}{Rounds: 1},
		},
		"reply": testCommands()["reply"],
	}

	exec := &Executor{
		Planner: &StubPlanner{
			Result: []CommandCall{
				{Command: "notify1", Args: map[string]any{}},
				{Command: "notify2", Args: map[string]any{}},
				{Command: "notify3", Args: map[string]any{}},
			},
		},
		Commands: cmds,
		Runners:  testRunners(runner),
		Logger:   testLogger(),
	}

	meta := EmailMeta{MessageID: "mp1", From: "x@test.com", Subject: "Multi passthrough"}
	p, err := exec.Run(context.Background(), meta, "body")
	require.NoError(t, err)

	assert.Equal(t, "completed", p.Status)
	require.Len(t, runner.calls, 4)

	// All three stages and the reply should receive the initial pipe (email metadata).
	initialPipe := runner.calls[0].Pipe
	for i := 1; i < 4; i++ {
		assert.Equal(t, initialPipe, runner.calls[i].Pipe,
			"call %d pipe should equal initial pipe", i)
	}
}
