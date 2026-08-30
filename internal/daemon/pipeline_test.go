package daemon

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/punt-labs/beadle/internal/enable"
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

func TestStageContext(t *testing.T) {
	tests := []struct {
		name string
		args map[string]any
		pipe string
		want string
	}{
		{
			name: "no args, no pipe",
			args: map[string]any{},
			pipe: "",
			want: "stage args: none\npipeline output: none",
		},
		{
			name: "one arg, no pipe",
			args: map[string]any{"env": "prod"},
			pipe: "",
			want: "stage args: env=prod\npipeline output: none",
		},
		{
			name: "multiple args sorted by key",
			args: map[string]any{"zebra": "z", "alpha": "a", "mid": 1},
			pipe: "",
			want: "stage args: alpha=a, mid=1, zebra=z\npipeline output: none",
		},
		{
			name: "pipe carried through",
			args: map[string]any{},
			pipe: `{"title":"x"}`,
			want: `stage args: none` + "\n" + `pipeline output: {"title":"x"}`,
		},
		{
			name: "args and pipe together",
			args: map[string]any{"to": "jim@test.com"},
			pipe: "prior output",
			want: "stage args: to=jim@test.com\npipeline output: prior output",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stageContext(tt.args, tt.pipe)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestStageContext_LongArgValueCapped(t *testing.T) {
	long := ""
	for i := 0; i < 600; i++ {
		long += "a"
	}
	got := stageContext(map[string]any{"note": long}, "")

	capped := ""
	for i := 0; i < 500; i++ {
		capped += "a"
	}
	assert.Equal(t, "stage args: note="+capped+truncationMarker+"\npipeline output: none", got)
}

func TestCapRunes(t *testing.T) {
	tests := []struct {
		name string
		s    string
		n    int
		want string
	}{
		{"under cap, unchanged, no marker", "short", 500, "short"},
		{"exactly at cap, unchanged, no marker", "abcde", 5, "abcde"},
		{"over cap, truncated with marker", "abcdef", 5, "abcde" + truncationMarker},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, capRunes(tt.s, tt.n))
		})
	}
}

// TestBuildStageContract asserts the generated contract no longer emits
// inputs.args or inputs.pipeline_output -- both are unknown fields that
// ethos's strict-decode mission-contract schema rejects (only inputs.trigger,
// inputs.files, inputs.ticket, and inputs.references are recognized). The
// content those two fields used to carry must survive in the top-level
// context field instead.
func TestBuildStageContract(t *testing.T) {
	meta := EmailMeta{MessageID: "42", From: "alice@example.com", Subject: "Deploy please"}
	cmd := &Command{
		Prompt:   "Deploy to production",
		WriteSet: []string{"deploy/manifest.yaml"},
		Budget: struct {
			Rounds              int  `yaml:"rounds"`
			ReflectionAfterEach bool `yaml:"reflection_after_each"`
		}{Rounds: 2, ReflectionAfterEach: true},
	}
	call := CommandCall{Command: "deploy", Args: map[string]any{"env": "prod"}}

	out := buildStageContract(meta, cmd, call, `{"summary":"prior stage output"}`)

	var doc map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(out), &doc))

	assert.Equal(t, "claude", doc["leader"])
	assert.Equal(t, "bwk", doc["worker"])

	inputs, ok := doc["inputs"].(map[string]any)
	require.True(t, ok, "inputs must be a map")
	_, hasArgs := inputs["args"]
	assert.False(t, hasArgs, "inputs.args must not be emitted -- unknown field rejected by ethos")
	_, hasPipelineOutput := inputs["pipeline_output"]
	assert.False(t, hasPipelineOutput, "inputs.pipeline_output must not be emitted -- unknown field rejected by ethos")

	trigger, ok := inputs["trigger"].(map[string]any)
	require.True(t, ok, "inputs.trigger must be a map")
	assert.Equal(t, "email", trigger["type"])
	assert.Equal(t, meta.MessageID, trigger["message_id"])

	contextVal, ok := doc["context"].(string)
	require.True(t, ok, "context must be a string")
	assert.Contains(t, contextVal, "env=prod")
	assert.Contains(t, contextVal, `{"summary":"prior stage output"}`)

	ws, ok := doc["write_set"].([]any)
	require.True(t, ok, "write_set must be a list")
	assert.Equal(t, []any{"deploy/manifest.yaml"}, ws)

	budget, ok := doc["budget"].(map[string]any)
	require.True(t, ok, "budget must be a map")
	assert.Equal(t, 2, budget["rounds"])
	assert.Equal(t, true, budget["reflection_after_each"])
}

func TestBuildStageContract_NoArgsNoPipe(t *testing.T) {
	meta := EmailMeta{MessageID: "7", From: "bob@example.com", Subject: "Greet"}
	cmd := &Command{
		Prompt:   "Greet the user",
		WriteSet: []string{"output/greet.txt"},
		Budget: struct {
			Rounds              int  `yaml:"rounds"`
			ReflectionAfterEach bool `yaml:"reflection_after_each"`
		}{Rounds: 1},
	}
	call := CommandCall{Command: "greet", Args: map[string]any{}}

	out := buildStageContract(meta, cmd, call, "")

	var doc map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(out), &doc))

	contextVal, ok := doc["context"].(string)
	require.True(t, ok, "context must be a string")
	assert.Equal(t, "stage args: none\npipeline output: none", contextVal)
}

// isolatedEthosEnv builds a process environment for exec'ing the real ethos
// CLI that reads the real, current identity/archetype data but writes
// nothing to it. ethos derives its global root (session bindings, mission
// ID counters, delegations, locks) from os.UserHomeDir(), and its repo root
// (mission storage, identity/personality/role/team resolution) from cwd or
// the ETHOS_REPO_ROOT override -- so both must be redirected together, or
// half the state (e.g. the global session binding) still lands on the real
// ambient trees.
//
// This repo runs in a shared, multi-agent environment: without this,
// `ethos mission create`/`ethos mission abandon` corrupt whatever mission a
// real concurrent session has bound (bindDispatchedMission,
// clearClosedSessionBindings), and burn real per-date mission-ID counter
// slots.
//
// Every subtree ethos might WRITE to (global sessions/missions/delegations/
// locks/counters; repo missions/sessions) is left absent, so ethos creates
// a fresh, empty one under the scratch roots. Every subtree ethos only
// READS (global archetypes; repo identities/personalities/roles/teams/
// talents/writing-styles) is symlinked to the real, current data so
// contract validation exercises the genuine schema and identity graph.
func isolatedEthosEnv(t *testing.T) []string {
	t.Helper()

	realHome, err := os.UserHomeDir()
	require.NoError(t, err)
	realRepoRoot, err := enable.RepoRoot()
	require.NoError(t, err)

	scratchHome := t.TempDir()
	scratchGlobalEthos := filepath.Join(scratchHome, ".punt-labs", "ethos")
	require.NoError(t, os.MkdirAll(scratchGlobalEthos, 0o700))
	require.NoError(t, os.Symlink(
		filepath.Join(realHome, ".punt-labs", "ethos", "archetypes"),
		filepath.Join(scratchGlobalEthos, "archetypes"),
	))

	scratchRepo := t.TempDir()
	scratchRepoEthos := filepath.Join(scratchRepo, ".punt-labs", "ethos")
	require.NoError(t, os.MkdirAll(scratchRepoEthos, 0o700))
	realRepoEthos := filepath.Join(realRepoRoot, ".punt-labs", "ethos")
	for _, sub := range []string{"identities", "personalities", "roles", "teams", "talents", "writing-styles"} {
		require.NoError(t, os.Symlink(filepath.Join(realRepoEthos, sub), filepath.Join(scratchRepoEthos, sub)))
	}

	env := make([]string, 0, len(os.Environ())+2)
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "HOME=") || strings.HasPrefix(kv, "ETHOS_REPO_ROOT=") {
			continue
		}
		env = append(env, kv)
	}
	return append(env, "HOME="+scratchHome, "ETHOS_REPO_ROOT="+scratchRepo)
}

// TestBuildStageContract_ValidatesAgainstRealEthosCLI invokes the real
// `ethos mission create --file <path>` binary (via os/exec, the same
// mechanism createMissionFromContract in mission.go uses) against a
// contract built by buildStageContract, and asserts it succeeds. This is
// the exact bug that shipped undetected: the generated contract was never
// validated against the real CLI, only against a hand-rolled string or
// struct shape that did not encode ethos's actual schema. A test that only
// checks the Go string, without handing it to the real ethos binary, would
// not have caught it and must not be trusted to catch a recurrence.
//
// Both exec.Command calls run with isolatedEthosEnv so ethos's writes never
// touch the real ambient ~/.punt-labs/ethos or this checkout's real mission
// history -- see isolatedEthosEnv's doc comment.
func TestBuildStageContract_ValidatesAgainstRealEthosCLI(t *testing.T) {
	ethosPath, err := exec.LookPath("ethos")
	if err != nil {
		t.Skip("ethos not on PATH; skipping real-CLI mission-contract validation")
	}

	isolatedEnv := isolatedEthosEnv(t)

	meta := EmailMeta{MessageID: "regression-8gt", From: "jim@test.com", Subject: "beadle-8gt regression test"}
	// A unique write_set path per run: ethos refuses to create a mission
	// whose write_set overlaps any currently-open mission's write_set, so a
	// fixed path would let a mission leaked by a prior failed/killed test
	// run permanently wedge every later run.
	writeSetPath := fmt.Sprintf("internal/daemon/testdata/mission-regression-fixture-8gt-%s.txt", uuid.New().String())
	cmd := &Command{
		Prompt:   "Deploy to production",
		WriteSet: []string{writeSetPath},
		Budget: struct {
			Rounds              int  `yaml:"rounds"`
			ReflectionAfterEach bool `yaml:"reflection_after_each"`
		}{Rounds: 1, ReflectionAfterEach: false},
	}
	call := CommandCall{Command: "deploy", Args: map[string]any{"env": "prod"}}

	contract := buildStageContract(meta, cmd, call, `{"prior":"output"}`)

	f, err := os.CreateTemp(t.TempDir(), "contract-*.yaml")
	require.NoError(t, err)
	_, err = f.WriteString(contract)
	require.NoError(t, err)
	require.NoError(t, f.Close())

	createCmd := exec.Command(ethosPath, "mission", "create", "--file", f.Name())
	createCmd.Env = isolatedEnv
	out, err := createCmd.CombinedOutput()
	require.NoError(t, err, "ethos mission create rejected the generated contract: %s", string(out))
	rawOut := string(out)

	// Register cleanup before parsing the mission ID below: a
	// parseMissionID failure there must not orphan the just-created
	// mission with no cleanup registered. The closure re-parses rawOut
	// independently so it does not depend on the outer parse succeeding.
	t.Cleanup(func() {
		missionID, parseErr := parseMissionID(rawOut)
		if parseErr != nil {
			t.Errorf("cleanup: could not parse mission ID from create output, mission is leaked and must be abandoned by hand: %v (output: %s)", parseErr, rawOut)
			return
		}
		abandonCmd := exec.Command(ethosPath, "mission", "abandon", missionID, "--reason", "test cleanup")
		abandonCmd.Env = isolatedEnv
		if abandonOut, abandonErr := abandonCmd.CombinedOutput(); abandonErr != nil {
			t.Errorf("cleanup: ethos mission abandon %s failed, mission is leaked and must be abandoned by hand: %v (output: %s)", missionID, abandonErr, string(abandonOut))
		}
	})

	_, parseErr := parseMissionID(rawOut)
	require.NoError(t, parseErr, "output: %s", rawOut)
}
