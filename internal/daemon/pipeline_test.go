package daemon

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockClaudeRunner implements Runner for pipeline tests.
type mockClaudeRunner struct {
	calls   []mockRunnerCall
	results []WorkerResult
	errs    []error
	idx     int
}

type mockRunnerCall struct {
	Idx  int
	Cmd  string
	Pipe string
	Args map[string]any
}

func (m *mockClaudeRunner) Run(_ context.Context, _ *Executor, _ *Pipeline, idx int, cmd *Command, call CommandCall, pipe string) (string, error) {
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

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

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
			Name:   "summarize",
			Runner: "claude",
			Mode:   "process",
			Prompt: "Summarize the input",
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

func testRunners(runner *mockClaudeRunner) map[string]Runner {
	return map[string]Runner{"claude": runner}
}

func TestExecutor_TwoStagePipeline(t *testing.T) {
	runner := &mockClaudeRunner{
		results: []WorkerResult{
			{Output: "Hello, Jim!"},
			{Output: `{"title":"greeting","summary":"sent"}`},
			{Output: "reply sent"},
		},
	}

	exec := &Executor{
		Planner: &StubPlanner{
			Result: []CommandCall{
				{Command: "greet", Args: map[string]any{}},
				{Command: "summarize", Args: map[string]any{}},
			},
		},
		Commands: testCommands(),
		Runners:  testRunners(runner),
		Logger:   testLogger(),
	}

	meta := EmailMeta{MessageID: "42", From: "jim@test.com", Subject: "Test"}
	p, err := exec.Run(context.Background(), meta, "body")
	require.NoError(t, err)

	assert.Equal(t, "completed", p.Status)
	assert.Equal(t, "", p.Error)
	assert.Len(t, p.Results, 3) // 2 stages + auto-reply
	assert.Equal(t, "Hello, Jim!", p.Results[0])
	assert.Equal(t, `{"title":"greeting","summary":"sent"}`, p.Results[1])
	assert.Len(t, runner.calls, 3) // 2 stages + auto-reply

	// WriteSet is the union of both commands.
	assert.Contains(t, p.WriteSet, "output/greet.txt")
	assert.Contains(t, p.WriteSet, "output/summary.txt")
}

func TestExecutor_StageFailure(t *testing.T) {
	runner := &mockClaudeRunner{
		results: []WorkerResult{
			{Output: "stage 0 ok"},
		},
		errs: []error{
			nil,
			fmt.Errorf("deploy exploded"),
		},
	}

	exec := &Executor{
		Planner: &StubPlanner{
			Result: []CommandCall{
				{Command: "greet", Args: map[string]any{}},
				{Command: "summarize", Args: map[string]any{}},
			},
		},
		Commands: testCommands(),
		Runners:  testRunners(runner),
		Logger:   testLogger(),
	}

	meta := EmailMeta{MessageID: "99", From: "jim@test.com", Subject: "Fail"}
	p, err := exec.Run(context.Background(), meta, "body")
	require.Error(t, err)

	assert.Equal(t, "failed", p.Status)
	assert.Contains(t, p.Error, "stage 1")
	assert.Len(t, p.Results, 1) // first stage succeeded
	// 2 stage calls + 1 else reply call.
	assert.Len(t, runner.calls, 3)
}

func TestExecutor_PlannerFailure(t *testing.T) {
	runner := &mockClaudeRunner{}

	exec := &Executor{
		Planner:  &StubPlanner{Err: fmt.Errorf("no rules matched")},
		Commands: testCommands(),
		Runners:  testRunners(runner),
		Logger:   testLogger(),
	}

	meta := EmailMeta{MessageID: "1", From: "x@test.com", Subject: "Nope"}
	p, err := exec.Run(context.Background(), meta, "body")
	require.Error(t, err)

	assert.Equal(t, "failed", p.Status)
	assert.Contains(t, p.Error, "plan")
	assert.Contains(t, err.Error(), "plan pipeline")
	// Else handler fires reply.
	assert.Len(t, runner.calls, 1)
}

func TestExecutor_EmptyPlan(t *testing.T) {
	runner := &mockClaudeRunner{}

	exec := &Executor{
		Planner:  &StubPlanner{Result: []CommandCall{}},
		Commands: testCommands(),
		Runners:  testRunners(runner),
		Logger:   testLogger(),
	}

	meta := EmailMeta{MessageID: "2", From: "x@test.com", Subject: "Empty"}
	p, err := exec.Run(context.Background(), meta, "body")
	require.Error(t, err)

	assert.Equal(t, "failed", p.Status)
	assert.Contains(t, p.Error, "empty")
	// Else handler fires reply.
	assert.Len(t, runner.calls, 1)
}

func TestExecutor_UnknownCommand(t *testing.T) {
	runner := &mockClaudeRunner{}

	exec := &Executor{
		Planner: &StubPlanner{
			Result: []CommandCall{
				{Command: "nonexistent", Args: map[string]any{}},
			},
		},
		Commands: testCommands(),
		Runners:  testRunners(runner),
		Logger:   testLogger(),
	}

	meta := EmailMeta{MessageID: "3", From: "x@test.com", Subject: "Bad cmd"}
	p, err := exec.Run(context.Background(), meta, "body")
	require.Error(t, err)

	assert.Equal(t, "failed", p.Status)
	assert.Contains(t, p.Error, "unknown command")
	// Else handler fires reply.
	assert.Len(t, runner.calls, 1)
}

func TestExecutor_InvalidArgs(t *testing.T) {
	runner := &mockClaudeRunner{}

	exec := &Executor{
		Planner: &StubPlanner{
			Result: []CommandCall{
				{Command: "deploy", Args: map[string]any{"env": "invalid-env"}},
			},
		},
		Commands: testCommands(),
		Runners:  testRunners(runner),
		Logger:   testLogger(),
	}

	meta := EmailMeta{MessageID: "4", From: "x@test.com", Subject: "Bad args"}
	p, err := exec.Run(context.Background(), meta, "body")
	require.Error(t, err)

	assert.Equal(t, "failed", p.Status)
	assert.Contains(t, p.Error, "stage 0")
	// Else handler fires reply.
	assert.Len(t, runner.calls, 1)
}

func TestExecutor_WorkerError(t *testing.T) {
	runner := &mockClaudeRunner{
		errs: []error{
			fmt.Errorf("something went wrong"),
		},
	}

	exec := &Executor{
		Planner: &StubPlanner{
			Result: []CommandCall{
				{Command: "greet", Args: map[string]any{}},
			},
		},
		Commands: testCommands(),
		Runners:  testRunners(runner),
		Logger:   testLogger(),
	}

	meta := EmailMeta{MessageID: "5", From: "x@test.com", Subject: "Worker fail"}
	p, err := exec.Run(context.Background(), meta, "body")
	require.Error(t, err)

	assert.Equal(t, "failed", p.Status)
	assert.Contains(t, p.Error, "stage 0")
	// 1 stage call (failed) + 1 else reply call.
	assert.Len(t, runner.calls, 2)
}

func TestExecutor_ResultFlowing(t *testing.T) {
	runner := &mockClaudeRunner{
		results: []WorkerResult{
			{Output: "stage-0-output"},
			{Output: `{"title":"flow","summary":"test"}`},
			{Output: "reply sent"},
		},
	}

	exec := &Executor{
		Planner: &StubPlanner{
			Result: []CommandCall{
				{Command: "greet", Args: map[string]any{}},
				{Command: "summarize", Args: map[string]any{}},
			},
		},
		Commands: testCommands(),
		Runners:  testRunners(runner),
		Logger:   testLogger(),
	}

	meta := EmailMeta{MessageID: "6", From: "x@test.com", Subject: "Flow"}
	p, err := exec.Run(context.Background(), meta, "body")
	require.NoError(t, err)

	assert.Equal(t, "completed", p.Status)
	// 2 work stages + auto-reply.
	require.Len(t, p.Results, 3)
	assert.Equal(t, "stage-0-output", p.Results[0])
	assert.Equal(t, `{"title":"flow","summary":"test"}`, p.Results[1])

	// 2 stages + 1 auto-reply.
	require.Len(t, runner.calls, 3)
}

func TestExecutor_AutoReplyArgs(t *testing.T) {
	runner := &mockClaudeRunner{
		results: []WorkerResult{
			{Output: `{"title":"test","summary":"summarized content"}`},
			{Output: "reply sent"},
		},
	}

	exec := &Executor{
		Planner: &StubPlanner{
			Result: []CommandCall{
				{Command: "summarize", Args: map[string]any{}},
			},
		},
		Commands: testCommands(),
		Runners:  testRunners(runner),
		Logger:   testLogger(),
	}

	meta := EmailMeta{MessageID: "10", From: "Alice <alice@example.com>", Subject: "Summarize this"}
	p, err := exec.Run(context.Background(), meta, "body")
	require.NoError(t, err)

	assert.Equal(t, "completed", p.Status)
	require.Len(t, p.Results, 2) // 1 stage + auto-reply
	assert.Equal(t, `{"title":"test","summary":"summarized content"}`, p.Results[0])

	// The reply runner call should have "to" arg with extracted address.
	require.Len(t, runner.calls, 2)
	assert.Equal(t, "reply", runner.calls[1].Cmd)
}

func TestExecutor_NoReplyCommand(t *testing.T) {
	runner := &mockClaudeRunner{
		results: []WorkerResult{
			{Output: "done"},
		},
	}

	// Build commands without the reply command.
	cmds := map[string]*Command{
		"greet": testCommands()["greet"],
	}

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

	meta := EmailMeta{MessageID: "11", From: "bob@test.com", Subject: "Hi"}
	p, err := exec.Run(context.Background(), meta, "body")
	require.NoError(t, err)

	assert.Equal(t, "completed", p.Status)
	assert.Len(t, p.Results, 1) // no auto-reply appended
	assert.Len(t, runner.calls, 1)
}

func TestExecutor_ElseReply(t *testing.T) {
	runner := &mockClaudeRunner{
		results: []WorkerResult{
			{Output: "else reply sent"},
		},
	}

	exec := &Executor{
		Planner:  &StubPlanner{Err: fmt.Errorf("no match")},
		Commands: testCommands(),
		Runners:  testRunners(runner),
		Logger:   testLogger(),
	}

	meta := EmailMeta{MessageID: "12", From: "carol@test.com", Subject: "Unknown"}
	p, err := exec.Run(context.Background(), meta, "body")
	require.Error(t, err)

	assert.Equal(t, "failed", p.Status)
	// Else handler fires a reply.
	require.Len(t, runner.calls, 1)
	assert.Equal(t, "reply", runner.calls[0].Cmd)
}

func TestExecutor_ElseNoReplyCommand(t *testing.T) {
	runner := &mockClaudeRunner{}

	// Build commands without the reply command.
	cmds := map[string]*Command{
		"greet": testCommands()["greet"],
	}

	exec := &Executor{
		Planner:  &StubPlanner{Err: fmt.Errorf("no match")},
		Commands: cmds,
		Runners:  testRunners(runner),
		Logger:   testLogger(),
	}

	meta := EmailMeta{MessageID: "13", From: "dave@test.com", Subject: "Unknown"}
	p, err := exec.Run(context.Background(), meta, "body")
	require.Error(t, err)

	assert.Equal(t, "failed", p.Status)
	// No reply command — else handler logs but does not call runner.
	assert.Len(t, runner.calls, 0)
}
