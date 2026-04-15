package daemon

import (
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
