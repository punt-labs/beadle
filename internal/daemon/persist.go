package daemon

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// PipelineStore persists pipeline state to JSON files for crash recovery.
type PipelineStore struct {
	Dir    string
	Logger *slog.Logger
}

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
		os.Remove(tmp)
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
		data, err := os.ReadFile(path)
		if err != nil {
			s.Logger.Warn("skip unreadable pipeline file", "path", path, "error", err)
			continue
		}

		var p Pipeline
		if err := json.Unmarshal(data, &p); err != nil {
			s.Logger.Warn("skip corrupt pipeline file", "path", path, "error", err)
			continue
		}

		if p.Status == "running" {
			running = append(running, &p)
		}
	}
	return running, nil
}
