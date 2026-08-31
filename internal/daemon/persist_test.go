package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
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

// -- beadle-721a: periodic pruning --

func TestPipelineStore_StartPruning_RunsPeriodically(t *testing.T) {
	dir := t.TempDir()
	s := &PipelineStore{Dir: dir, Logger: testLogger(), PruneInterval: 5 * time.Millisecond}

	old := &Pipeline{Version: 1, ID: "old-done", Status: "completed", Email: EmailMeta{From: "x@test.com"}, CreatedAt: time.Now().Add(-60 * 24 * time.Hour)}
	require.NoError(t, s.Save(old))

	s.StartPruning(DefaultRetention)
	defer s.StopPruning()

	require.Eventually(t, func() bool {
		_, err := os.Stat(filepath.Join(dir, "old-done.json"))
		return os.IsNotExist(err)
	}, time.Second, 5*time.Millisecond, "StartPruning must remove an aged record without a second manual Prune call")

	// A pipeline that becomes eligible only AFTER StartPruning is already
	// running must still be caught by a later tick -- proves the loop
	// really is periodic, not just an immediate one-shot in disguise.
	laterOld := &Pipeline{Version: 1, ID: "later-old-done", Status: "completed", Email: EmailMeta{From: "x@test.com"}, CreatedAt: time.Now().Add(-60 * 24 * time.Hour)}
	require.NoError(t, s.Save(laterOld))

	require.Eventually(t, func() bool {
		_, err := os.Stat(filepath.Join(dir, "later-old-done.json"))
		return os.IsNotExist(err)
	}, time.Second, 5*time.Millisecond, "a record aged after StartPruning began must still be pruned on a subsequent tick")
}

func TestPipelineStore_StopPruning_StopsTheGoroutine(t *testing.T) {
	dir := t.TempDir()
	s := &PipelineStore{Dir: dir, Logger: testLogger(), PruneInterval: 5 * time.Millisecond}

	s.StartPruning(DefaultRetention)
	s.StopPruning()

	// Written after the goroutine has fully stopped (StopPruning waits).
	// If the goroutine were still running, this would be pruned on its
	// very next tick.
	old := &Pipeline{Version: 1, ID: "old-done", Status: "completed", Email: EmailMeta{From: "x@test.com"}, CreatedAt: time.Now().Add(-60 * 24 * time.Hour)}
	require.NoError(t, s.Save(old))

	time.Sleep(50 * time.Millisecond)

	_, err := os.Stat(filepath.Join(dir, "old-done.json"))
	assert.NoError(t, err, "a record saved after StopPruning must survive: the goroutine must actually be stopped")
}

func TestPipelineStore_StopPruning_WithoutStartIsNoop(t *testing.T) {
	s := &PipelineStore{Dir: t.TempDir(), Logger: testLogger()}
	assert.NotPanics(t, func() { s.StopPruning() })
}

func TestPipelineStore_StopPruning_Idempotent(t *testing.T) {
	dir := t.TempDir()
	s := &PipelineStore{Dir: dir, Logger: testLogger(), PruneInterval: 5 * time.Millisecond}
	s.StartPruning(DefaultRetention)
	s.StopPruning()
	assert.NotPanics(t, func() { s.StopPruning() })
}

// TestPipelineStore_StartPruning_SecondCallIsNoop proves a second
// StartPruning call while the first goroutine is still running does not
// orphan it. Before this was fixed, the second call unconditionally
// overwrote s.pruneStop, so StopPruning closed only the second (newer)
// channel: the first goroutine, still selecting on the original channel
// nobody held a reference to anymore, never returned, and pruneWG.Wait()
// blocked forever. Run under a timeout so a regression fails the test
// instead of hanging the whole suite.
func TestPipelineStore_StartPruning_SecondCallIsNoop(t *testing.T) {
	dir := t.TempDir()
	s := &PipelineStore{Dir: dir, Logger: testLogger(), PruneInterval: 5 * time.Millisecond}

	s.StartPruning(DefaultRetention)
	s.StartPruning(DefaultRetention) // must be a no-op, not a second goroutine

	done := make(chan struct{})
	go func() {
		s.StopPruning()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("StopPruning did not return: a second StartPruning call orphaned the first goroutine")
	}
}

// TestPipelineStore_Prune_ConcurrentWithSave exercises the invariant
// StartPruning's doc comment claims: Prune walking the pipeline
// directory while Executor.Run's goroutines call Save on their own
// pipelines is safe without a lock. Run with -race (mandatory for this
// repo) so the race detector, not just the assertions below, is a judge.
func TestPipelineStore_Prune_ConcurrentWithSave(t *testing.T) {
	dir := t.TempDir()
	s := &PipelineStore{Dir: dir, Logger: testLogger()}

	const nPipelines = 20
	const nSavesPerPipeline = 10

	var wg sync.WaitGroup
	var pruneErrs int32

	// Writers: each simulates one pipeline's lifecycle -- "running" for
	// most of its saves, an aged CreatedAt, then "completed" on its last
	// save. A concurrent Prune pass must never remove a "running" file,
	// and the final "completed" state must always survive to be read
	// back intact (never a torn or corrupt file).
	for i := 0; i < nPipelines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			p := &Pipeline{
				Version:   1,
				ID:        fmt.Sprintf("concurrent-%d", i),
				Status:    "running",
				Email:     EmailMeta{From: "x@test.com"},
				CreatedAt: time.Now().Add(-60 * 24 * time.Hour), // aged from the start
			}
			for j := 0; j < nSavesPerPipeline; j++ {
				if j == nSavesPerPipeline-1 {
					p.Status = "completed"
				}
				if err := s.Save(p); err != nil {
					t.Errorf("save pipeline %s: %v", p.ID, err)
				}
			}
		}(i)
	}

	// Pruner: hammers Prune concurrently with the writers above.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			if _, err := s.Prune(DefaultRetention); err != nil {
				atomic.AddInt32(&pruneErrs, 1)
			}
		}
	}()

	wg.Wait()
	assert.Zero(t, atomic.LoadInt32(&pruneErrs), "Prune must never error while racing concurrent Save calls")

	// wg.Wait() above only returns once every writer goroutine AND the
	// pruner goroutine have both returned, so no Save or Prune call is
	// still in flight below. Every pipeline's last write set its status
	// to "completed" and its CreatedAt was aged from the moment it was
	// first created, so by this point every record on disk -- whatever
	// the concurrent race above already removed or left behind -- is
	// completed and old enough to prune. One more Prune call, now fully
	// sequential, must therefore remove everything that is left: the
	// directory ends up completely empty, which is the real claim this
	// test makes about "no lock needed" -- not merely that removed does
	// not exceed nPipelines, which held trivially either way.
	removed, err := s.Prune(DefaultRetention)
	require.NoError(t, err)
	assert.LessOrEqual(t, removed, nPipelines, "Prune cannot remove more files than were ever written")

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	for _, e := range entries {
		data, readErr := os.ReadFile(filepath.Join(dir, e.Name()))
		require.NoError(t, readErr)
		var p Pipeline
		require.NoError(t, json.Unmarshal(data, &p), "every surviving file must be valid, complete JSON, never a torn write")
	}
	assert.Empty(t, entries, "every pipeline finished completed and aged with no writer or pruner still running; the final sequential Prune call must remove all of them")
}

// -- beadle-28ew: orphaned .tmp- files --

func TestPipelineStore_Prune_ReapsOrphanedTempFile(t *testing.T) {
	dir := t.TempDir()
	s := &PipelineStore{Dir: dir, Logger: testLogger()}

	tmpPath := filepath.Join(dir, ".tmp-orphan-1.json")
	require.NoError(t, os.WriteFile(tmpPath, []byte(`{"id":"orphan-1"`), 0o600)) // deliberately truncated JSON
	oldMtime := time.Now().Add(-1 * time.Hour)
	require.NoError(t, os.Chtimes(tmpPath, oldMtime, oldMtime))

	removed, err := s.Prune(DefaultRetention)
	require.NoError(t, err)
	assert.Equal(t, 1, removed)

	_, err = os.Stat(tmpPath)
	assert.True(t, os.IsNotExist(err), "a .tmp- file older than the orphan threshold must be reaped")
}

func TestPipelineStore_Prune_KeepsFreshTempFile(t *testing.T) {
	dir := t.TempDir()
	s := &PipelineStore{Dir: dir, Logger: testLogger()}

	tmpPath := filepath.Join(dir, ".tmp-inflight-1.json")
	require.NoError(t, os.WriteFile(tmpPath, []byte(`{"id":"inflight-1"}`), 0o600))
	// mtime is "now" -- indistinguishable from a write-then-rename in
	// flight, so Prune must leave it alone.

	removed, err := s.Prune(DefaultRetention)
	require.NoError(t, err)
	assert.Equal(t, 0, removed)

	_, err = os.Stat(tmpPath)
	assert.NoError(t, err, "a fresh .tmp- file must never be reaped: it may just be mid-rename")
}

func TestPipelineStore_Prune_TempFileAgeJudgedByFilesystemNotContent(t *testing.T) {
	dir := t.TempDir()
	s := &PipelineStore{Dir: dir, Logger: testLogger()}

	// Not valid JSON at all -- a torn write may not even parse. Age must
	// still be judged by mtime, never by trying to unmarshal this.
	tmpPath := filepath.Join(dir, ".tmp-torn-1.json")
	require.NoError(t, os.WriteFile(tmpPath, []byte(`{not json at all`), 0o600))
	oldMtime := time.Now().Add(-1 * time.Hour)
	require.NoError(t, os.Chtimes(tmpPath, oldMtime, oldMtime))

	removed, err := s.Prune(DefaultRetention)
	require.NoError(t, err)
	assert.Equal(t, 1, removed)
}

// TestPipelineStore_Prune_TempFileVanishedBeforeStatIsSilent proves a
// .tmp- file that a concurrent Save already renamed into place by the
// time pruneOrphanedTemp gets to it is a silent skip, not a warning: the
// rename working as intended is not a fault. The file is removed here
// (rather than actually raced with a concurrent Save) so the ENOENT is
// deterministic instead of depending on goroutine scheduling.
func TestPipelineStore_Prune_TempFileVanishedBeforeStatIsSilent(t *testing.T) {
	dir := t.TempDir()
	logger, buf := testLoggerCapture()
	s := &PipelineStore{Dir: dir, Logger: logger}

	path := filepath.Join(dir, ".tmp-vanished-1.json")
	require.NoError(t, os.WriteFile(path, []byte(`{}`), 0o600))
	require.NoError(t, os.Remove(path))

	removed := s.pruneOrphanedTemp(path, s.now())
	assert.False(t, removed, "a file that already vanished cannot itself be removed by this call")
	assert.NotContains(t, buf.String(), "unstatable temp file",
		"a benign ENOENT from a concurrent Save's rename must not be logged as a warning")
}

// TestPipelineStore_Prune_AgedFileVanishedBeforeReadIsSilent is
// pruneOrphanedTemp's counterpart for a regular (non-.tmp-) pipeline
// record: a file another concurrent Prune call already removed between
// ReadDir and this read is gone, not broken.
func TestPipelineStore_Prune_AgedFileVanishedBeforeReadIsSilent(t *testing.T) {
	dir := t.TempDir()
	logger, buf := testLoggerCapture()
	s := &PipelineStore{Dir: dir, Logger: logger}

	path := filepath.Join(dir, "vanished-1.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"id":"vanished-1"}`), 0o600))
	require.NoError(t, os.Remove(path))

	removed := s.pruneAgedFile(path, s.now())
	assert.False(t, removed, "a file that already vanished cannot itself be removed by this call")
	assert.NotContains(t, buf.String(), "unreadable pipeline file",
		"a benign ENOENT from a concurrent Prune's remove must not be logged as a warning")
}

// TestPipelineStore_Prune_ConcurrentPruneCallsDoNotWarnOnVanishedFile
// exercises the same race through the public Prune entry point: two
// goroutines racing to prune the same directory of aged, non-running
// records will, for at least one of the many files, have one goroutine
// remove a file the other has already listed via ReadDir but not yet
// read -- and that read must not log a warning.
func TestPipelineStore_Prune_ConcurrentPruneCallsDoNotWarnOnVanishedFile(t *testing.T) {
	dir := t.TempDir()
	logger, buf := testLoggerCapture()
	s := &PipelineStore{Dir: dir, Logger: logger}

	const nFiles = 200
	old := time.Now().Add(-60 * 24 * time.Hour)
	for i := 0; i < nFiles; i++ {
		p := &Pipeline{Version: 1, ID: fmt.Sprintf("race-%d", i), Status: "completed", Email: EmailMeta{From: "x@test.com"}, CreatedAt: old}
		require.NoError(t, s.Save(p))
	}

	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = s.Prune(DefaultRetention)
		}()
	}
	wg.Wait()

	assert.NotContains(t, buf.String(), "skip unreadable pipeline file during prune",
		"two Prune calls racing to remove the same aged files must not log a benign ENOENT as a warning")

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Empty(t, entries, "both Prune calls together must have removed every aged record")
}

func TestPipelineStore_Prune_UsesInjectedClockForTempFileAge(t *testing.T) {
	dir := t.TempDir()
	fixedNow := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	s := &PipelineStore{Dir: dir, Logger: testLogger(), Now: func() time.Time { return fixedNow }}

	tmpPath := filepath.Join(dir, ".tmp-clock-1.json")
	require.NoError(t, os.WriteFile(tmpPath, []byte(`{}`), 0o600))
	// Real mtime is "now" (test wall-clock), but the injected clock is
	// pinned far in the future relative to it, so the file must be
	// judged aged and removed -- proves the age check uses s.Now, not a
	// bare time.Now() call.
	require.NoError(t, os.Chtimes(tmpPath, fixedNow.Add(-2*time.Hour), fixedNow.Add(-2*time.Hour)))

	removed, err := s.Prune(DefaultRetention)
	require.NoError(t, err)
	assert.Equal(t, 1, removed)
}
