package daemon

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// PipelineStore persists pipeline state to JSON files for crash recovery.
type PipelineStore struct {
	Dir    string
	Logger *slog.Logger

	// Now returns the current time. Nil means time.Now; tests override it
	// to pin Prune's cutoff for exact boundary checks.
	Now func() time.Time

	// PruneInterval is how often StartPruning's goroutine calls Prune.
	// Zero means DefaultPruneInterval; tests set it short to observe the
	// loop tick without waiting a day.
	PruneInterval time.Duration

	pruneStop chan struct{}
	pruneWG   sync.WaitGroup
}

// logger returns s.Logger, or slog.Default if unset. A store built without
// a Logger (as some tests do) must still be safe to call on error paths.
func (s *PipelineStore) logger() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

// now returns s.Now(), or time.Now if s.Now is unset. Every age
// calculation in this file -- Prune's cutoff and the orphaned-temp-file
// check -- goes through this so a test can pin the clock once and get
// deterministic boundaries everywhere.
func (s *PipelineStore) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// DefaultRetention is how long a pipeline record is kept on disk before
// Prune removes it. Each record carries email metadata (from, subject,
// message_id) plus every stage's output, so unbounded retention is a
// data-retention concern, not just a disk one. Thirty days is long enough
// to debug a failed run days after the fact, short enough that a mailbox
// running for months does not accumulate an unbounded audit trail.
const DefaultRetention = 30 * 24 * time.Hour

// Save writes a pipeline to dir/<id>.json via atomic rename.
func (s *PipelineStore) Save(p *Pipeline) error {
	if strings.ContainsAny(p.ID, "/\\") || strings.Contains(p.ID, "..") || p.ID == "" {
		return fmt.Errorf("invalid pipeline ID %q", p.ID)
	}

	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return fmt.Errorf("create pipeline dir %s: %w", s.Dir, err)
	}

	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal pipeline %s: %w", p.ID, err)
	}

	tmp := filepath.Join(s.Dir, ".tmp-"+p.ID+".json")
	final := filepath.Join(s.Dir, p.ID+".json")

	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write temp file %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, final); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename %s to %s: %w", tmp, final, err)
	}
	return nil
}

// LoadRunning reads all JSON files in dir and returns pipelines with status "running".
// Parse errors are logged and skipped.
//
// TRUST BOUNDARY: Pipeline JSON on disk is potentially untrusted
// (crafted files, corruption). This function returns the struct
// for inspection only — callers must not resume pipeline execution
// from loaded state without re-validating all fields.
func (s *PipelineStore) LoadRunning() ([]*Pipeline, error) {
	entries, err := os.ReadDir(s.Dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read pipeline dir %s: %w", s.Dir, err)
	}

	var running []*Pipeline
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		// Skip temp files from incomplete writes.
		if strings.HasPrefix(e.Name(), ".tmp-") {
			continue
		}

		path := filepath.Join(s.Dir, e.Name())
		data, err := os.ReadFile(filepath.Clean(path))
		if err != nil {
			s.logger().Warn("skip unreadable pipeline file", "path", path, "error", err)
			continue
		}

		var p Pipeline
		if err := json.Unmarshal(data, &p); err != nil {
			s.logger().Warn("skip corrupt pipeline file", "path", path, "error", err)
			continue
		}

		if p.Status == "running" {
			running = append(running, &p)
		}
	}
	return running, nil
}

// tmpFileMaxAge is how long a .tmp-<id>.json file may exist before Prune
// treats it as orphaned. Save writes this file and renames it into place
// within milliseconds; a temp file that outlives tmpFileMaxAge means the
// process died between the write and the rename, and nothing else will
// ever clean it up -- LoadRunning and Prune's main loop both skip
// ".tmp-" files by name because they cannot tell "mid-write" from
// "abandoned" from the name alone. Minutes, not DefaultRetention's 30
// days: a legitimate write-then-rename completes far faster than that.
const tmpFileMaxAge = 5 * time.Minute

// Prune deletes pipeline records older than maxAge, judged by CreatedAt,
// and reaps orphaned .tmp- files (see tmpFileMaxAge). A record still
// "running" is left alone regardless of age: Prune must never destroy
// the only record of a pipeline that might still be executing. Call the
// startup stale-pipeline sweep (LoadRunning, mark failed, Save) before
// Prune runs, so a pipeline orphaned by a daemon crash is marked failed
// -- and therefore eligible for removal -- first. Unreadable or corrupt
// files are logged and skipped, same as LoadRunning. It returns the
// number of files removed.
//
// Safe to call concurrently with Save (StartPruning does exactly that):
// Save writes to a temp file and renames it into place, so a reader here
// sees the old content or the new content, never a torn write, and every
// pipeline has a distinct UUID filename, so Prune and Save never touch
// the same bytes at the same instant.
func (s *PipelineStore) Prune(maxAge time.Duration) (int, error) {
	entries, err := os.ReadDir(s.Dir)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read pipeline dir %s: %w", s.Dir, err)
	}

	now := s.now()
	cutoff := now.Add(-maxAge)
	removed := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}

		path := filepath.Join(s.Dir, e.Name())

		if strings.HasPrefix(e.Name(), ".tmp-") {
			if s.pruneOrphanedTemp(path, now) {
				removed++
			}
			continue
		}

		data, err := os.ReadFile(filepath.Clean(path))
		if err != nil {
			s.logger().Warn("skip unreadable pipeline file during prune", "path", path, "error", err)
			continue
		}

		var p Pipeline
		if err := json.Unmarshal(data, &p); err != nil {
			s.logger().Warn("skip corrupt pipeline file during prune", "path", path, "error", err)
			continue
		}

		if p.Status == "running" || !p.CreatedAt.Before(cutoff) {
			continue
		}

		if err := os.Remove(path); err != nil {
			s.logger().Warn("remove aged pipeline file failed", "path", path, "error", err)
			continue
		}
		removed++
	}
	return removed, nil
}

// pruneOrphanedTemp removes the .tmp- file at path if it is older than
// tmpFileMaxAge as of now. Age is judged by a fresh os.Stat of the file
// on disk, not by unmarshaling its content: a torn write may not even
// parse as JSON. It returns whether the file was removed.
func (s *PipelineStore) pruneOrphanedTemp(path string, now time.Time) bool {
	info, err := os.Stat(path)
	if err != nil {
		s.logger().Warn("skip unstatable temp file during prune", "path", path, "error", err)
		return false
	}
	if now.Sub(info.ModTime()) < tmpFileMaxAge {
		return false
	}
	if err := os.Remove(path); err != nil {
		s.logger().Warn("remove orphaned temp file failed", "path", path, "error", err)
		return false
	}
	return true
}

// DefaultPruneInterval is how often StartPruning's goroutine calls
// Prune. Retention itself (DefaultRetention) is 30 days; a daily sweep
// is frequent enough to bound disk growth without walking the pipeline
// directory more than necessary.
const DefaultPruneInterval = 24 * time.Hour

// StartPruning launches a goroutine that calls Prune(maxAge) once
// immediately and then every s.PruneInterval (DefaultPruneInterval if
// unset) until StopPruning is called. This replaces the daemon's old
// startup-only pruning: a daemon that stays up for months -- the case
// its own premise of acting on email unattended makes the common one --
// must keep enforcing retention, not just once at process start.
//
// Call StartPruning after the startup stale-pipeline sweep, so a
// pipeline the daemon just marked "failed" is eligible for removal on
// this goroutine's very first pass. See Prune's doc comment for why the
// resulting concurrency with Executor.Run's Save calls needs no lock.
func (s *PipelineStore) StartPruning(maxAge time.Duration) {
	interval := s.PruneInterval
	if interval <= 0 {
		interval = DefaultPruneInterval
	}

	stop := make(chan struct{})
	s.pruneStop = stop
	s.pruneWG.Add(1)
	go func() {
		defer s.pruneWG.Done()
		s.pruneTick(maxAge)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				s.pruneTick(maxAge)
			}
		}
	}()
}

// StopPruning stops the goroutine started by StartPruning and waits for
// it to exit. Safe to call even if StartPruning was never called, or was
// already stopped.
func (s *PipelineStore) StopPruning() {
	if s.pruneStop == nil {
		return
	}
	close(s.pruneStop)
	s.pruneWG.Wait()
	s.pruneStop = nil
}

// pruneTick runs one Prune pass and logs the outcome. Errors are logged,
// not returned: StartPruning's goroutine has no caller to report them to.
func (s *PipelineStore) pruneTick(maxAge time.Duration) {
	removed, err := s.Prune(maxAge)
	if err != nil {
		s.logger().Warn("periodic prune failed", "error", err)
		return
	}
	if removed > 0 {
		s.logger().Info("periodic prune removed aged pipeline records", "count", removed)
	}
}
