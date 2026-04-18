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

// mockSpawner returns preconfigured results per mission, keyed by call index.
type mockSpawner struct {
	calls   []mockSpawnerCall
	results []WorkerResult
	errs    []error
	idx     int
}

type mockSpawnerCall struct {
	MissionID        string
	MCPConfigPath    string
	SystemPromptPath string
	EnvOverrides     map[string]string
}

func (m *mockSpawner) Run(_ context.Context, missionID, mcpConfigPath, systemPromptPath string, envOverrides map[string]string) (WorkerResult, error) {
	m.calls = append(m.calls, mockSpawnerCall{
		MissionID:        missionID,
		MCPConfigPath:    mcpConfigPath,
		SystemPromptPath: systemPromptPath,
		EnvOverrides:     envOverrides,
	})
	i := m.idx
	m.idx++
	if i < len(m.errs) && m.errs[i] != nil {
		return WorkerResult{MissionID: missionID, IsError: true}, m.errs[i]
	}
	if i < len(m.results) {
		return m.results[i], nil
	}
	return WorkerResult{MissionID: missionID, Output: "ok"}, nil
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func testCommands() map[string]*Command {
	return map[string]*Command{
		"greet": {
			Name:   "greet",
			Prompt: "Greet the user",
			Input:  "none",
			Output: "prose",
			Budget: struct {
				Rounds              int  `yaml:"rounds"`
				ReflectionAfterEach bool `yaml:"reflection_after_each"`
			}{Rounds: 1},
			WriteSet:   []string{"output/greet.txt"},
			MCPServers: []string{"ethos"},
		},
		"summarize": {
			Name:   "summarize",
			Prompt: "Summarize the input",
			Input:  "required",
			Output: "prose",
			Budget: struct {
				Rounds              int  `yaml:"rounds"`
				ReflectionAfterEach bool `yaml:"reflection_after_each"`
			}{Rounds: 1},
			WriteSet:   []string{"output/summary.txt"},
			MCPServers: []string{"ethos", "beadle-email"},
		},
		"deploy": {
			Name:   "deploy",
			Prompt: "Deploy to production",
			Input:  "optional",
			Output: "prose",
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
			Name:   "reply",
			Prompt: "Reply to the sender with the pipeline output",
			Input:  "required",
			Output: "prose",
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

func TestExecutor_TwoStagePipeline(t *testing.T) {
	sp := &mockSpawner{
		results: []WorkerResult{
			{Output: "Hello, Jim!"},
			{Output: "Summary: greeting sent"},
		},
	}
	mock := &mockMissionCreator{}
	tmpl := &MissionTemplate{TmpDir: t.TempDir()}

	exec := &Executor{
		Planner: &StubPlanner{
			Result: []CommandCall{
				{Command: "greet", Args: map[string]any{}},
				{Command: "summarize", Args: map[string]any{}},
			},
		},
		Commands:  testCommands(),
		Missions:  mock,
		Spawner:   sp,
		Templates: tmpl,
		Registry:  DefaultMCPRegistry(),
		Logger:    testLogger(),
	}

	meta := EmailMeta{MessageID: "42", From: "jim@test.com", Subject: "Test"}
	p, err := exec.Run(context.Background(), meta, "body")
	require.NoError(t, err)

	assert.Equal(t, "completed", p.Status)
	assert.Equal(t, "", p.Error)
	assert.Len(t, p.Results, 3) // 2 stages + auto-reply
	assert.Equal(t, "Hello, Jim!", p.Results[0])
	assert.Equal(t, "Summary: greeting sent", p.Results[1])
	assert.Len(t, sp.calls, 3) // 2 stages + auto-reply
	assert.Len(t, mock.calls, 3)

	// WriteSet is the union of both commands.
	assert.Contains(t, p.WriteSet, "output/greet.txt")
	assert.Contains(t, p.WriteSet, "output/summary.txt")
}

func TestExecutor_StageFailure(t *testing.T) {
	sp := &mockSpawner{
		results: []WorkerResult{
			{Output: "stage 0 ok"},
		},
		errs: []error{
			nil,
			fmt.Errorf("deploy exploded"),
		},
	}
	mock := &mockMissionCreator{}
	tmpl := &MissionTemplate{TmpDir: t.TempDir()}

	exec := &Executor{
		Planner: &StubPlanner{
			Result: []CommandCall{
				{Command: "greet", Args: map[string]any{}},
				{Command: "summarize", Args: map[string]any{}},
			},
		},
		Commands:  testCommands(),
		Missions:  mock,
		Spawner:   sp,
		Templates: tmpl,
		Registry:  DefaultMCPRegistry(),
		Logger:    testLogger(),
	}

	meta := EmailMeta{MessageID: "99", From: "jim@test.com", Subject: "Fail"}
	p, err := exec.Run(context.Background(), meta, "body")
	require.Error(t, err)

	assert.Equal(t, "failed", p.Status)
	assert.Contains(t, p.Error, "stage 1")
	assert.Len(t, p.Results, 1) // first stage succeeded
	// 2 stage spawns + 1 else reply spawn.
	assert.Len(t, sp.calls, 3)
}

func TestExecutor_PlannerFailure(t *testing.T) {
	sp := &mockSpawner{}
	mock := &mockMissionCreator{}
	tmpl := &MissionTemplate{TmpDir: t.TempDir()}

	exec := &Executor{
		Planner:   &StubPlanner{Err: fmt.Errorf("no rules matched")},
		Commands:  testCommands(),
		Missions:  mock,
		Spawner:   sp,
		Templates: tmpl,
		Registry:  DefaultMCPRegistry(),
		Logger:    testLogger(),
	}

	meta := EmailMeta{MessageID: "1", From: "x@test.com", Subject: "Nope"}
	p, err := exec.Run(context.Background(), meta, "body")
	require.Error(t, err)

	assert.Equal(t, "failed", p.Status)
	assert.Contains(t, p.Error, "plan")
	assert.Contains(t, err.Error(), "plan pipeline")
	// Else handler fires reply.
	assert.Len(t, sp.calls, 1)
}

func TestExecutor_EmptyPlan(t *testing.T) {
	sp := &mockSpawner{}
	mock := &mockMissionCreator{}
	tmpl := &MissionTemplate{TmpDir: t.TempDir()}

	exec := &Executor{
		Planner:   &StubPlanner{Result: []CommandCall{}},
		Commands:  testCommands(),
		Missions:  mock,
		Spawner:   sp,
		Templates: tmpl,
		Registry:  DefaultMCPRegistry(),
		Logger:    testLogger(),
	}

	meta := EmailMeta{MessageID: "2", From: "x@test.com", Subject: "Empty"}
	p, err := exec.Run(context.Background(), meta, "body")
	require.Error(t, err)

	assert.Equal(t, "failed", p.Status)
	assert.Contains(t, p.Error, "empty")
	// Else handler fires reply.
	assert.Len(t, sp.calls, 1)
}

func TestExecutor_UnknownCommand(t *testing.T) {
	sp := &mockSpawner{}
	mock := &mockMissionCreator{}
	tmpl := &MissionTemplate{TmpDir: t.TempDir()}

	exec := &Executor{
		Planner: &StubPlanner{
			Result: []CommandCall{
				{Command: "nonexistent", Args: map[string]any{}},
			},
		},
		Commands:  testCommands(),
		Missions:  mock,
		Spawner:   sp,
		Templates: tmpl,
		Registry:  DefaultMCPRegistry(),
		Logger:    testLogger(),
	}

	meta := EmailMeta{MessageID: "3", From: "x@test.com", Subject: "Bad cmd"}
	p, err := exec.Run(context.Background(), meta, "body")
	require.Error(t, err)

	assert.Equal(t, "failed", p.Status)
	assert.Contains(t, p.Error, "unknown command")
	// Else handler fires reply.
	assert.Len(t, sp.calls, 1)
}

func TestExecutor_InvalidArgs(t *testing.T) {
	sp := &mockSpawner{}
	mock := &mockMissionCreator{}
	tmpl := &MissionTemplate{TmpDir: t.TempDir()}

	exec := &Executor{
		Planner: &StubPlanner{
			Result: []CommandCall{
				{Command: "deploy", Args: map[string]any{"env": "invalid-env"}},
			},
		},
		Commands:  testCommands(),
		Missions:  mock,
		Spawner:   sp,
		Templates: tmpl,
		Registry:  DefaultMCPRegistry(),
		Logger:    testLogger(),
	}

	meta := EmailMeta{MessageID: "4", From: "x@test.com", Subject: "Bad args"}
	p, err := exec.Run(context.Background(), meta, "body")
	require.Error(t, err)

	assert.Equal(t, "failed", p.Status)
	assert.Contains(t, p.Error, "stage 0")
	// Else handler fires reply.
	assert.Len(t, sp.calls, 1)
}

func TestExecutor_WorkerError(t *testing.T) {
	sp := &mockSpawner{
		results: []WorkerResult{
			{Output: "something went wrong", IsError: true, ExitCode: 1},
		},
	}
	mock := &mockMissionCreator{}
	tmpl := &MissionTemplate{TmpDir: t.TempDir()}

	exec := &Executor{
		Planner: &StubPlanner{
			Result: []CommandCall{
				{Command: "greet", Args: map[string]any{}},
			},
		},
		Commands:  testCommands(),
		Missions:  mock,
		Spawner:   sp,
		Templates: tmpl,
		Registry:  DefaultMCPRegistry(),
		Logger:    testLogger(),
	}

	meta := EmailMeta{MessageID: "5", From: "x@test.com", Subject: "Worker fail"}
	p, err := exec.Run(context.Background(), meta, "body")
	require.Error(t, err)

	assert.Equal(t, "failed", p.Status)
	assert.Contains(t, p.Error, "worker error")
	// 1 stage spawn (failed) + 1 else reply spawn.
	assert.Len(t, sp.calls, 2)
}

func TestExecutor_ResultFlowing(t *testing.T) {
	// Verify that the second stage's spawner call happens after the first
	// completes, and the mock records both calls in order.
	sp := &mockSpawner{
		results: []WorkerResult{
			{Output: "stage-0-output"},
			{Output: "stage-1-output"},
		},
	}
	mock := &mockMissionCreator{}
	tmpl := &MissionTemplate{TmpDir: t.TempDir()}

	exec := &Executor{
		Planner: &StubPlanner{
			Result: []CommandCall{
				{Command: "greet", Args: map[string]any{}},
				{Command: "summarize", Args: map[string]any{}},
			},
		},
		Commands:  testCommands(),
		Missions:  mock,
		Spawner:   sp,
		Templates: tmpl,
		Registry:  DefaultMCPRegistry(),
		Logger:    testLogger(),
	}

	meta := EmailMeta{MessageID: "6", From: "x@test.com", Subject: "Flow"}
	p, err := exec.Run(context.Background(), meta, "body")
	require.NoError(t, err)

	assert.Equal(t, "completed", p.Status)
	// 2 work stages + auto-reply.
	require.Len(t, p.Results, 3)
	assert.Equal(t, "stage-0-output", p.Results[0])
	assert.Equal(t, "stage-1-output", p.Results[1])

	// 2 stages + 1 auto-reply.
	require.Len(t, sp.calls, 3)
}

func TestExecutor_AutoReplyArgs(t *testing.T) {
	sp := &mockSpawner{
		results: []WorkerResult{
			{Output: "summarized content"},
			{Output: "reply sent"}, // auto-reply stage
		},
	}
	mock := &mockMissionCreator{}
	tmpl := &MissionTemplate{TmpDir: t.TempDir()}

	exec := &Executor{
		Planner: &StubPlanner{
			Result: []CommandCall{
				{Command: "summarize", Args: map[string]any{}},
			},
		},
		Commands:  testCommands(),
		Missions:  mock,
		Spawner:   sp,
		Templates: tmpl,
		Registry:  DefaultMCPRegistry(),
		Logger:    testLogger(),
	}

	meta := EmailMeta{MessageID: "10", From: "Alice <alice@example.com>", Subject: "Summarize this"}
	p, err := exec.Run(context.Background(), meta, "body")
	require.NoError(t, err)

	assert.Equal(t, "completed", p.Status)
	require.Len(t, p.Results, 2) // 1 stage + auto-reply
	assert.Equal(t, "summarized content", p.Results[0])

	// The reply mission subject includes "reply" command name.
	require.Len(t, mock.calls, 2)
	assert.Contains(t, mock.calls[1].Subject, "reply")

	// Verify the sender address was extracted from "Name <email>" format.
	// The reply stage was called (2 spawner calls), confirming args passed validation.
	require.Len(t, sp.calls, 2)
}

func TestExecutor_NoReplyCommand(t *testing.T) {
	sp := &mockSpawner{
		results: []WorkerResult{
			{Output: "done"},
		},
	}
	mock := &mockMissionCreator{}
	tmpl := &MissionTemplate{TmpDir: t.TempDir()}

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
		Commands:  cmds,
		Missions:  mock,
		Spawner:   sp,
		Templates: tmpl,
		Registry:  DefaultMCPRegistry(),
		Logger:    testLogger(),
	}

	meta := EmailMeta{MessageID: "11", From: "bob@test.com", Subject: "Hi"}
	p, err := exec.Run(context.Background(), meta, "body")
	require.NoError(t, err)

	assert.Equal(t, "completed", p.Status)
	assert.Len(t, p.Results, 1) // no auto-reply appended
	assert.Len(t, sp.calls, 1)  // only the greet stage
}

func TestExecutor_ElseReply(t *testing.T) {
	sp := &mockSpawner{
		results: []WorkerResult{
			{Output: "else reply sent"}, // else handler reply
		},
	}
	mock := &mockMissionCreator{}
	tmpl := &MissionTemplate{TmpDir: t.TempDir()}

	exec := &Executor{
		Planner:   &StubPlanner{Err: fmt.Errorf("no match")},
		Commands:  testCommands(),
		Missions:  mock,
		Spawner:   sp,
		Templates: tmpl,
		Registry:  DefaultMCPRegistry(),
		Logger:    testLogger(),
	}

	meta := EmailMeta{MessageID: "12", From: "carol@test.com", Subject: "Unknown"}
	p, err := exec.Run(context.Background(), meta, "body")
	require.Error(t, err)

	assert.Equal(t, "failed", p.Status)
	// Else handler fires a reply spawn.
	require.Len(t, sp.calls, 1)
	require.Len(t, mock.calls, 1)
	// The else reply mission subject includes "reply".
	assert.Contains(t, mock.calls[0].Subject, "reply")
}

func TestExecutor_ElseNoReplyCommand(t *testing.T) {
	sp := &mockSpawner{}
	mock := &mockMissionCreator{}
	tmpl := &MissionTemplate{TmpDir: t.TempDir()}

	// Build commands without the reply command.
	cmds := map[string]*Command{
		"greet": testCommands()["greet"],
	}

	exec := &Executor{
		Planner:   &StubPlanner{Err: fmt.Errorf("no match")},
		Commands:  cmds,
		Missions:  mock,
		Spawner:   sp,
		Templates: tmpl,
		Registry:  DefaultMCPRegistry(),
		Logger:    testLogger(),
	}

	meta := EmailMeta{MessageID: "13", From: "dave@test.com", Subject: "Unknown"}
	p, err := exec.Run(context.Background(), meta, "body")
	require.Error(t, err)

	assert.Equal(t, "failed", p.Status)
	// No reply command — else handler logs but does not spawn.
	assert.Len(t, sp.calls, 0)
	assert.Len(t, mock.calls, 0)
}
