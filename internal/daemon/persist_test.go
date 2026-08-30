package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPipelineStore_SaveAndLoadRunning(t *testing.T) {
	dir := t.TempDir()
	s := &PipelineStore{Dir: dir, Logger: testLogger()}

	p := &Pipeline{
		Version:   1,
		ID:        "abc-123",
		CreatedAt: time.Date(2026, 4, 15, 10, 0, 0, 0, time.UTC),
		Email:     EmailMeta{MessageID: "1", From: "jim@test.com", Subject: "Test"},
		Status:    "running",
	}

	require.NoError(t, s.Save(p))

	got, err := s.LoadRunning()
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "abc-123", got[0].ID)
	assert.Equal(t, "running", got[0].Status)
	assert.Equal(t, "jim@test.com", got[0].Email.From)
}

func TestPipelineStore_CompletedNotReturned(t *testing.T) {
	dir := t.TempDir()
	s := &PipelineStore{Dir: dir, Logger: testLogger()}

	p := &Pipeline{
		Version: 1,
		ID:      "done-1",
		Email:   EmailMeta{From: "x@test.com"},
		Status:  "completed",
	}
	require.NoError(t, s.Save(p))

	got, err := s.LoadRunning()
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestPipelineStore_AtomicRename(t *testing.T) {
	dir := t.TempDir()
	s := &PipelineStore{Dir: dir, Logger: testLogger()}

	p := &Pipeline{
		Version: 1,
		ID:      "atomic-1",
		Email:   EmailMeta{From: "x@test.com"},
		Status:  "running",
	}
	require.NoError(t, s.Save(p))

	// Final file must exist.
	_, err := os.Stat(filepath.Join(dir, "atomic-1.json"))
	require.NoError(t, err)

	// Temp file must not exist.
	_, err = os.Stat(filepath.Join(dir, ".tmp-atomic-1.json"))
	assert.True(t, os.IsNotExist(err))
}

func TestPipelineStore_CorruptJSONSkipped(t *testing.T) {
	dir := t.TempDir()
	s := &PipelineStore{Dir: dir, Logger: testLogger()}

	// Write a valid pipeline.
	p := &Pipeline{
		Version: 1,
		ID:      "good-1",
		Email:   EmailMeta{From: "x@test.com"},
		Status:  "running",
	}
	require.NoError(t, s.Save(p))

	// Write corrupt JSON alongside it.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bad.json"), []byte("{corrupt"), 0o600))

	got, err := s.LoadRunning()
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "good-1", got[0].ID)
}

func TestPipelineStore_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	s := &PipelineStore{Dir: dir, Logger: testLogger()}

	got, err := s.LoadRunning()
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestPipelineStore_NonexistentDir(t *testing.T) {
	s := &PipelineStore{Dir: filepath.Join(t.TempDir(), "missing"), Logger: testLogger()}

	got, err := s.LoadRunning()
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestPipelineStore_MixedStatuses(t *testing.T) {
	dir := t.TempDir()
	s := &PipelineStore{Dir: dir, Logger: testLogger()}

	cases := []struct {
		id     string
		status string
	}{
		{"r1", "running"},
		{"c1", "completed"},
		{"f1", "failed"},
		{"r2", "running"},
	}
	for _, tc := range cases {
		p := &Pipeline{Version: 1, ID: tc.id, Status: tc.status, Email: EmailMeta{From: "x@test.com"}}
		require.NoError(t, s.Save(p))
	}

	got, err := s.LoadRunning()
	require.NoError(t, err)
	require.Len(t, got, 2)

	ids := map[string]bool{}
	for _, p := range got {
		ids[p.ID] = true
	}
	assert.True(t, ids["r1"])
	assert.True(t, ids["r2"])
}

// TestExecutor_Run_PersistsToDisk proves that a pipeline run through
// Executor.Run with a real Store -- the daemon's production
// configuration, once main.go wires one in -- actually writes a record to
// disk, not just that save() was called. testCommands, testRunners, and
// mockClaudeRunner are declared in pipeline_test.go; this test exercises
// the same Executor type through its real persistence path instead of
// mocking it away.
func TestExecutor_Run_PersistsToDisk(t *testing.T) {
	dir := t.TempDir()
	store := &PipelineStore{Dir: dir, Logger: testLogger()}

	runner := &mockClaudeRunner{
		results: []WorkerResult{
			{Output: "Hello, Jim!"},
			{Output: "reply sent"},
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
		Store:    store,
		Logger:   testLogger(),
	}

	meta := EmailMeta{MessageID: "42", From: "jim@test.com", Subject: "Test"}
	p, err := exec.Run(context.Background(), meta, "body")
	require.NoError(t, err)
	require.Equal(t, "completed", p.Status)

	data, err := os.ReadFile(filepath.Join(dir, p.ID+".json"))
	require.NoError(t, err, "Executor.Run with a real Store must write the pipeline to disk")

	var onDisk Pipeline
	require.NoError(t, json.Unmarshal(data, &onDisk))
	assert.Equal(t, "completed", onDisk.Status)
	assert.Equal(t, p.ID, onDisk.ID)
	assert.Equal(t, "jim@test.com", onDisk.Email.From)
	assert.Equal(t, p.Results, onDisk.Results)
}

func TestPipelineStore_PruneRemovesAgedTerminalRecords(t *testing.T) {
	dir := t.TempDir()
	s := &PipelineStore{Dir: dir, Logger: testLogger()}

	old := &Pipeline{Version: 1, ID: "old-done", Status: "completed", Email: EmailMeta{From: "x@test.com"}, CreatedAt: time.Now().Add(-60 * 24 * time.Hour)}
	oldFailed := &Pipeline{Version: 1, ID: "old-failed", Status: "failed", Email: EmailMeta{From: "x@test.com"}, CreatedAt: time.Now().Add(-60 * 24 * time.Hour)}
	recent := &Pipeline{Version: 1, ID: "recent-done", Status: "completed", Email: EmailMeta{From: "x@test.com"}, CreatedAt: time.Now()}
	require.NoError(t, s.Save(old))
	require.NoError(t, s.Save(oldFailed))
	require.NoError(t, s.Save(recent))

	removed, err := s.Prune(DefaultRetention)
	require.NoError(t, err)
	assert.Equal(t, 2, removed)

	_, err = os.Stat(filepath.Join(dir, "old-done.json"))
	assert.True(t, os.IsNotExist(err), "aged completed record should be pruned")
	_, err = os.Stat(filepath.Join(dir, "old-failed.json"))
	assert.True(t, os.IsNotExist(err), "aged failed record should be pruned")
	_, err = os.Stat(filepath.Join(dir, "recent-done.json"))
	assert.NoError(t, err, "recent record should survive prune")
}

func TestPipelineStore_PruneRetainsExactCutoffBoundary(t *testing.T) {
	dir := t.TempDir()
	fixedNow := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	s := &PipelineStore{Dir: dir, Logger: testLogger(), Now: func() time.Time { return fixedNow }}

	maxAge := 24 * time.Hour
	cutoff := fixedNow.Add(-maxAge)

	atCutoff := &Pipeline{Version: 1, ID: "at-cutoff", Status: "completed", Email: EmailMeta{From: "x@test.com"}, CreatedAt: cutoff}
	justPast := &Pipeline{Version: 1, ID: "just-past", Status: "completed", Email: EmailMeta{From: "x@test.com"}, CreatedAt: cutoff.Add(-time.Nanosecond)}
	require.NoError(t, s.Save(atCutoff))
	require.NoError(t, s.Save(justPast))

	removed, err := s.Prune(maxAge)
	require.NoError(t, err)
	assert.Equal(t, 1, removed)

	_, err = os.Stat(filepath.Join(dir, "at-cutoff.json"))
	assert.NoError(t, err, "a record aged exactly maxAge is not older than maxAge and must be kept")

	_, err = os.Stat(filepath.Join(dir, "just-past.json"))
	assert.True(t, os.IsNotExist(err), "a record one nanosecond past the cutoff is older than maxAge and must be removed")
}

func TestPipelineStore_PruneNeverRemovesRunning(t *testing.T) {
	dir := t.TempDir()
	s := &PipelineStore{Dir: dir, Logger: testLogger()}

	oldRunning := &Pipeline{Version: 1, ID: "old-running", Status: "running", Email: EmailMeta{From: "x@test.com"}, CreatedAt: time.Now().Add(-60 * 24 * time.Hour)}
	require.NoError(t, s.Save(oldRunning))

	removed, err := s.Prune(DefaultRetention)
	require.NoError(t, err)
	assert.Equal(t, 0, removed, "an aged but still-running record must never be pruned")

	_, err = os.Stat(filepath.Join(dir, "old-running.json"))
	assert.NoError(t, err)
}

func TestPipelineStore_PruneNonexistentDir(t *testing.T) {
	s := &PipelineStore{Dir: filepath.Join(t.TempDir(), "missing"), Logger: testLogger()}

	removed, err := s.Prune(DefaultRetention)
	require.NoError(t, err)
	assert.Equal(t, 0, removed)
}

func TestPipelineStore_NilLoggerDoesNotPanic(t *testing.T) {
	dir := t.TempDir()
	s := &PipelineStore{Dir: dir} // no Logger set

	require.NoError(t, os.WriteFile(filepath.Join(dir, "bad.json"), []byte("{corrupt"), 0o600))

	assert.NotPanics(t, func() {
		got, err := s.LoadRunning()
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	old := &Pipeline{Version: 1, ID: "old-done", Status: "completed", Email: EmailMeta{From: "x@test.com"}, CreatedAt: time.Now().Add(-60 * 24 * time.Hour)}
	require.NoError(t, s.Save(old))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bad2.json"), []byte("{corrupt"), 0o600))

	assert.NotPanics(t, func() {
		removed, err := s.Prune(DefaultRetention)
		require.NoError(t, err)
		assert.Equal(t, 1, removed)
	})
}

func TestPipelineStore_PruneSkipsCorruptFiles(t *testing.T) {
	dir := t.TempDir()
	s := &PipelineStore{Dir: dir, Logger: testLogger()}

	old := &Pipeline{Version: 1, ID: "old-done", Status: "completed", Email: EmailMeta{From: "x@test.com"}, CreatedAt: time.Now().Add(-60 * 24 * time.Hour)}
	require.NoError(t, s.Save(old))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bad.json"), []byte("{corrupt"), 0o600))

	removed, err := s.Prune(DefaultRetention)
	require.NoError(t, err)
	assert.Equal(t, 1, removed)

	_, err = os.Stat(filepath.Join(dir, "bad.json"))
	assert.NoError(t, err, "a corrupt file must be skipped, not deleted")
}
