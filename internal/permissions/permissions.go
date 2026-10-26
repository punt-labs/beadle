// Package permissions adds and removes beadle's Claude Code allow rules in one
// repo's .claude/settings.json. beadle-email enable is the operator's explicit
// opt-in, so it — not plugin startup — grants the rules, and disable removes
// exactly the same set. Nothing here touches the user-global
// ~/.claude/settings.json (punt-kit permissions.md §6). The beadle-dev plugin's
// rules are never granted: maintainers add them by hand.
package permissions

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"unicode/utf8"
)

// Rules is the deterministic rule set: Grant adds every missing member and
// Revoke removes these exact values. A rule retired here strands on disable, so
// it stays in the set until no enabled repo can still carry it.
var Rules = []string{
	"mcp__plugin_beadle_email__*",
	"mcp__beadle-email__*",
	"Bash(beadle-email:*)",
	"Skill(beadle)",
	"Skill(contacts)",
	"Skill(inbox)",
	"Skill(mail)",
	"Skill(send)",
}

// ErrRefused marks a settings file left untouched because editing it would be
// unsafe or would overwrite data beadle does not own.
var ErrRefused = errors.New("refusing to edit .claude/settings.json")

const newFileMode = 0o644

// Grant appends each missing rule to <root>/.claude/settings.json, creating the
// file when absent, and returns the rules added.
func Grant(root string) ([]string, error) { return update(root, true) }

// Revoke removes every rule in Rules from <root>/.claude/settings.json and
// returns the rules removed. A repo with no settings file is left untouched.
func Revoke(root string) ([]string, error) { return update(root, false) }

func update(root string, add bool) ([]string, error) {
	if home, err := os.UserHomeDir(); err == nil && sameDir(root, home) {
		return nil, fmt.Errorf("%w: the repo is the home directory, whose .claude/settings.json is user-global", ErrRefused)
	}
	dir := filepath.Join(root, ".claude")
	file := filepath.Join(dir, "settings.json")
	present, err := settingsPresent(dir, file)
	if err != nil {
		return nil, err
	}
	if !present && !add {
		return nil, nil
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("creating %s: %w", dir, err)
	}
	var changed []string
	err = withLock(filepath.Join(dir, ".settings.json.punt-import.lock"), func() error {
		var applyErr error
		changed, applyErr = apply(file, add)
		return applyErr
	})
	return changed, err
}

// settingsPresent reports whether settings.json exists and refuses a symlinked
// .claude or settings.json. It runs before the lock is taken: the lock file
// lives in .claude/, so a .claude symlink would place it outside the repo.
func settingsPresent(dir, file string) (bool, error) {
	for _, path := range []string{dir, file} {
		info, err := os.Lstat(path)
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("inspecting %s: %w", path, err)
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return false, fmt.Errorf("%w: %s is a symlink", ErrRefused, filepath.Base(path))
		}
	}
	return true, nil
}

func apply(file string, add bool) ([]string, error) {
	settings, err := read(file)
	if err != nil {
		return nil, err
	}
	perms, ok := settings["permissions"]
	if !ok {
		perms = map[string]any{}
	}
	permsMap, ok := perms.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%w: 'permissions' is not an object", ErrRefused)
	}
	allowRaw, ok := permsMap["allow"]
	if !ok {
		allowRaw = []any{}
	}
	allow, ok := allowRaw.([]any)
	if !ok {
		return nil, fmt.Errorf("%w: 'permissions.allow' is not a list", ErrRefused)
	}

	var changed []string
	if add {
		for _, rule := range Rules {
			if !slices.Contains(allow, any(rule)) {
				allow = append(allow, rule)
				changed = append(changed, rule)
			}
		}
	} else {
		kept := make([]any, 0, len(allow))
		for _, rule := range allow {
			if s, isStr := rule.(string); isStr && slices.Contains(Rules, s) {
				if !slices.Contains(changed, s) {
					changed = append(changed, s)
				}
				continue
			}
			kept = append(kept, rule)
		}
		allow = kept
	}
	if len(changed) == 0 {
		return nil, nil
	}
	permsMap["allow"] = allow
	settings["permissions"] = permsMap
	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encoding %s: %w", file, err)
	}
	return changed, writeAtomic(file, append(out, '\n'))
}

func read(file string) (map[string]any, error) {
	data, err := os.ReadFile(filepath.Clean(file))
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", file, err)
	}
	if !utf8.Valid(data) {
		return nil, fmt.Errorf("%w: not valid UTF-8", ErrRefused)
	}
	var parsed any
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("%w: not valid JSON: %w", ErrRefused, err)
	}
	settings, ok := parsed.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%w: not a JSON object", ErrRefused)
	}
	return settings, nil
}

func writeAtomic(file string, data []byte) error {
	mode := fs.FileMode(newFileMode)
	if info, err := os.Stat(file); err == nil {
		mode = info.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(file), ".settings.json.*.tmp")
	if err != nil {
		return fmt.Errorf("creating temp file for %s: %w", file, err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing %s: %w", tmpName, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("syncing %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing %s: %w", tmpName, err)
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		return fmt.Errorf("chmod %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, file); err != nil {
		return fmt.Errorf("replacing %s: %w", file, err)
	}
	return nil
}

// withLock holds an exclusive flock on the tool-agnostic sibling lock file that
// every punt tool takes before editing settings.json.
func withLock(path string, fn func() error) error {
	lock, err := os.OpenFile(filepath.Clean(path), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("opening lock %s: %w", path, err)
	}
	defer func() { _ = lock.Close() }()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil { // #nosec G115 -- fd fits in int
		return fmt.Errorf("locking %s: %w", path, err)
	}
	defer func() { _ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) }() // #nosec G115 -- fd fits in int
	return fn()
}

func sameDir(a, b string) bool {
	ai, errA := os.Stat(a)
	bi, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(ai, bi)
}
