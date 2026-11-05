// Package permissions adds and removes beadle's Claude Code allow rules in one
// repo's .claude/settings.json. beadle-email enable is the operator's explicit
// opt-in, so it — not plugin startup — grants the rules, and disable removes
// exactly the set that enable added. Nothing here touches the user-global
// ~/.claude/settings.json (punt-kit permissions.md §6). The beadle-dev plugin's
// rules are never granted: maintainers add them by hand.
//
// Every access to the repo's .claude directory — the lock file included — goes
// through one *os.Root opened on that directory, so the kernel refuses any
// symlink that would lead outside it, even one swapped in while the flock is
// being waited on.
//
// On-disk contract: the rules a grant added are recorded in
// .claude/.beadle-granted.json (a JSON list of strings). Revoke removes only the
// recorded rules, so an allow rule the repo carried before enable survives
// disable. A repo with no record has nothing to revoke. Commit the record with
// settings.json.
//
// Edits preserve every byte of settings.json outside permissions.allow: key
// order, number text and string escaping are kept as written.
package permissions

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"unicode/utf8"
)

// Rules is the deterministic rule set: Grant adds every missing member and
// Revoke removes the recorded subset of it. A rule retired here strands on
// disable, so it stays in the set until no enabled repo can still carry it.
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

const (
	claudeDir    = ".claude"
	settingsName = "settings.json"
	lockName     = ".settings.json.punt-import.lock"
	grantedName  = ".beadle-granted.json"
	newFileMode  = 0o644
)

// request describes one edit. rules is the explicit set to remove; when nil on a
// removal, the recorded set is removed.
type request struct {
	add   bool
	dry   bool
	rules []string
}

// Grant appends each missing rule to <root>/.claude/settings.json, creating the
// file when absent, records the rules it added, and returns them.
func Grant(root string) ([]string, error) { return run(root, request{add: true}) }

// Revoke removes the rules a previous Grant recorded from
// <root>/.claude/settings.json and returns the rules removed. A repo with no
// settings file is left untouched.
func Revoke(root string) ([]string, error) { return run(root, request{}) }

// Remove withdraws the given rules, which must be members of Rules, and drops
// them from the record. Enable uses it to roll back exactly what it just added.
func Remove(root string, rules []string) ([]string, error) {
	if len(rules) == 0 {
		return nil, nil
	}
	return run(root, request{rules: rules})
}

// Preflight reports, without writing anything, whether Grant would be refused.
func Preflight(root string) error {
	_, err := run(root, request{add: true, dry: true})
	return err
}

func run(root string, q request) ([]string, error) {
	if home, err := os.UserHomeDir(); err == nil && sameDir(root, home) {
		return nil, fmt.Errorf("%w: the repo is the home directory, whose .claude/settings.json is user-global", ErrRefused)
	}
	dir := filepath.Join(root, claudeDir)
	// A fast preflight; the confined handle below is what holds the line.
	present, err := settingsPresent(dir, filepath.Join(dir, settingsName))
	if err != nil {
		return nil, err
	}
	if !present && (!q.add || q.dry) {
		return nil, nil
	}

	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", root, err)
	}
	defer func() { _ = r.Close() }()
	if q.add {
		if err := r.Mkdir(claudeDir, 0o750); err != nil && !errors.Is(err, fs.ErrExist) {
			return nil, fmt.Errorf("creating %s: %w", dir, err)
		}
	}
	c, err := r.OpenRoot(claudeDir)
	if err != nil {
		return nil, openError(dir, err)
	}
	defer func() { _ = c.Close() }()

	if q.dry {
		_, err := transact(c, q)
		return nil, err
	}
	var changed []string
	err = withLock(c, func() error {
		var txErr error
		changed, txErr = transact(c, q)
		return txErr
	})
	return changed, err
}

// openError explains a failure to open .claude, naming a symlink as a refusal.
func openError(dir string, err error) error {
	if info, statErr := os.Lstat(dir); statErr == nil && info.Mode()&fs.ModeSymlink != 0 {
		return fmt.Errorf("%w: %s is a symlink", ErrRefused, filepath.Base(dir))
	}
	return fmt.Errorf("opening %s: %w", dir, err)
}

// settingsPresent reports whether settings.json exists and refuses a symlinked
// .claude or settings.json. It is an early, cheap check on the plain path; the
// os.Root handle enforces the same boundary during the locked edit.
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

// transact performs one edit inside the confined directory and returns the
// rules changed. The record is written before settings.json on a grant and
// after it on a removal, so a crash never leaves a granted rule unrecorded.
func transact(c *os.Root, q request) ([]string, error) {
	data, err := readConfined(c, settingsName)
	if err != nil {
		return nil, err
	}
	granted, err := readGranted(c)
	if err != nil {
		return nil, err
	}
	rules := q.rules
	switch {
	case q.add:
		rules = Rules
	case rules == nil:
		rules = granted
	}
	if !q.add {
		rules = slices.DeleteFunc(slices.Clone(rules), func(s string) bool { return !slices.Contains(Rules, s) })
	}

	changed, out, err := plan(data, q.add, rules)
	if err != nil || q.dry {
		return nil, err
	}
	if q.add {
		if len(changed) == 0 {
			return nil, nil
		}
		merged := slices.Clone(granted)
		for _, rule := range changed {
			if !slices.Contains(merged, rule) {
				merged = append(merged, rule)
			}
		}
		if err := writeGranted(c, merged); err != nil {
			return nil, err
		}
		return changed, writeAtomic(c, settingsName, out)
	}
	if len(changed) > 0 {
		if err := writeAtomic(c, settingsName, out); err != nil {
			return nil, err
		}
	}
	rest := slices.DeleteFunc(slices.Clone(granted), func(s string) bool { return slices.Contains(rules, s) })
	if len(rest) != len(granted) {
		if err := writeGranted(c, rest); err != nil {
			return nil, err
		}
	}
	return changed, nil
}

// member is one key of a JSON object with its value kept as written.
type member struct {
	key string
	val json.RawMessage
}

// plan computes the allow-list edit. It returns the rules changed and the new
// file contents; with nothing to change it returns no contents. Only
// permissions.allow is rebuilt: every other key and value passes through as the
// raw bytes read, so ordering, number precision and escaping are preserved.
func plan(data []byte, add bool, rules []string) ([]string, []byte, error) {
	top := []member{}
	if data != nil {
		if !utf8.Valid(data) {
			return nil, nil, fmt.Errorf("%w: not valid UTF-8", ErrRefused)
		}
		var err error
		if top, err = parseObject(data); err != nil {
			return nil, nil, fmt.Errorf("%w: %w", ErrRefused, err)
		}
	}
	permsAt, err := find(top, "permissions")
	if err != nil {
		return nil, nil, err
	}
	perms := []member{}
	if permsAt >= 0 {
		if perms, err = parseObject(top[permsAt].val); err != nil {
			return nil, nil, fmt.Errorf("%w: 'permissions' is not an object", ErrRefused)
		}
	}
	allowAt, err := find(perms, "allow")
	if err != nil {
		return nil, nil, err
	}
	var allow []json.RawMessage
	if allowAt >= 0 {
		val := bytes.TrimSpace(perms[allowAt].val)
		if val[0] != '[' || json.Unmarshal(val, &allow) != nil {
			return nil, nil, fmt.Errorf("%w: 'permissions.allow' is not a list", ErrRefused)
		}
	}

	var changed []string
	if add {
		for _, rule := range rules {
			if !hasRule(allow, rule) {
				enc, err := encodeString(rule)
				if err != nil {
					return nil, nil, err
				}
				allow = append(allow, enc)
				changed = append(changed, rule)
			}
		}
	} else {
		kept := allow[:0:0]
		for _, raw := range allow {
			if s, ok := ruleString(raw); ok && slices.Contains(rules, s) {
				if !slices.Contains(changed, s) {
					changed = append(changed, s)
				}
				continue
			}
			kept = append(kept, raw)
		}
		allow = kept
	}
	if len(changed) == 0 {
		return nil, nil, nil
	}

	allowRaw := json.RawMessage("[" + string(bytes.Join(rawBytes(allow), []byte(","))) + "]")
	perms = set(perms, "allow", allowRaw)
	permsRaw, err := encodeObject(perms)
	if err != nil {
		return nil, nil, err
	}
	top = set(top, "permissions", permsRaw)
	compact, err := encodeObject(top)
	if err != nil {
		return nil, nil, err
	}
	var out bytes.Buffer
	if err := json.Indent(&out, compact, "", "  "); err != nil {
		return nil, nil, fmt.Errorf("formatting settings: %w", err)
	}
	out.WriteByte('\n')
	return changed, out.Bytes(), nil
}

func rawBytes(raws []json.RawMessage) [][]byte {
	out := make([][]byte, len(raws))
	for i, r := range raws {
		out[i] = r
	}
	return out
}

func hasRule(allow []json.RawMessage, rule string) bool {
	return slices.ContainsFunc(allow, func(raw json.RawMessage) bool {
		s, ok := ruleString(raw)
		return ok && s == rule
	})
}

func ruleString(raw json.RawMessage) (string, bool) {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", false
	}
	return s, true
}

// find returns the index of key in ms, -1 when absent. A duplicated key is
// ambiguous about which value a reader sees, so it is refused.
func find(ms []member, key string) (int, error) {
	at := -1
	for i, m := range ms {
		if m.key != key {
			continue
		}
		if at >= 0 {
			return 0, fmt.Errorf("%w: duplicate %q key", ErrRefused, key)
		}
		at = i
	}
	return at, nil
}

func set(ms []member, key string, val json.RawMessage) []member {
	for i := range ms {
		if ms[i].key == key {
			ms[i].val = val
			return ms
		}
	}
	return append(ms, member{key: key, val: val})
}

// parseObject splits data, exactly one JSON object, into its members in source
// order, leaving each value as the bytes written.
func parseObject(data []byte) ([]member, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return nil, fmt.Errorf("not valid JSON: %w", err)
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, errors.New("not a JSON object")
	}
	ms := []member{}
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil, fmt.Errorf("not valid JSON: %w", err)
		}
		key, ok := kt.(string)
		if !ok {
			return nil, errors.New("not valid JSON: object key is not a string")
		}
		var val json.RawMessage
		if err := dec.Decode(&val); err != nil {
			return nil, fmt.Errorf("not valid JSON: %w", err)
		}
		ms = append(ms, member{key: key, val: val})
	}
	if _, err := dec.Token(); err != nil {
		return nil, fmt.Errorf("not valid JSON: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("not valid JSON: trailing data after the object")
	}
	return ms, nil
}

func encodeObject(ms []member) (json.RawMessage, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, m := range ms {
		if i > 0 {
			b.WriteByte(',')
		}
		k, err := encodeString(m.key)
		if err != nil {
			return nil, err
		}
		b.Write(k)
		b.WriteByte(':')
		b.Write(m.val)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// encodeString quotes s without HTML escaping, so & < > survive an edit.
func encodeString(s string) (json.RawMessage, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		return nil, fmt.Errorf("encoding %q: %w", s, err)
	}
	return bytes.TrimRight(b.Bytes(), "\n"), nil
}

// readConfined reads name from c, nil when absent, refusing a symlink.
func readConfined(c *os.Root, name string) ([]byte, error) {
	info, err := c.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("inspecting %s: %w", name, err)
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return nil, fmt.Errorf("%w: %s is a symlink", ErrRefused, name)
	}
	data, err := c.ReadFile(name)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", name, err)
	}
	return data, nil
}

func readGranted(c *os.Root) ([]string, error) {
	data, err := readConfined(c, grantedName)
	if err != nil || data == nil {
		return nil, err
	}
	var granted []string
	if err := json.Unmarshal(data, &granted); err != nil {
		return nil, fmt.Errorf("%w: %s is not a JSON list of strings: %w", ErrRefused, grantedName, err)
	}
	return granted, nil
}

// writeGranted records the granted rules, or deletes the record when none remain.
func writeGranted(c *os.Root, granted []string) error {
	if len(granted) == 0 {
		if err := c.Remove(grantedName); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("removing %s: %w", grantedName, err)
		}
		return nil
	}
	data, err := json.MarshalIndent(granted, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding %s: %w", grantedName, err)
	}
	return writeAtomic(c, grantedName, append(data, '\n'))
}

// writeAtomic replaces name in c with data through a temp file and rename, all
// confined to c.
func writeAtomic(c *os.Root, name string, data []byte) (err error) {
	mode := fs.FileMode(newFileMode)
	if info, statErr := c.Stat(name); statErr == nil {
		mode = info.Mode().Perm()
	}
	tmpName, tmp, err := createTemp(c)
	if err != nil {
		return fmt.Errorf("creating temp file for %s: %w", name, err)
	}
	defer func() { _ = c.Remove(tmpName) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing %s: %w", tmpName, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("syncing %s: %w", tmpName, err)
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing %s: %w", tmpName, err)
	}
	if err := c.Rename(tmpName, name); err != nil {
		return fmt.Errorf("replacing %s: %w", name, err)
	}
	return nil
}

// createTemp makes an exclusive, randomly named file in c; os.Root has no
// CreateTemp.
func createTemp(c *os.Root) (string, *os.File, error) {
	var lastErr error
	for range 10 {
		var b [8]byte
		if _, err := rand.Read(b[:]); err != nil {
			return "", nil, fmt.Errorf("choosing temp name: %w", err)
		}
		name := ".beadle." + hex.EncodeToString(b[:]) + ".tmp"
		f, err := c.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			return name, f, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return "", nil, err
		}
		lastErr = err
	}
	return "", nil, lastErr
}

// withLock holds an exclusive flock on the tool-agnostic sibling lock file that
// every punt tool takes before editing settings.json. The lock is opened through
// c, so a symlinked lock cannot lead outside .claude.
func withLock(c *os.Root, fn func() error) error {
	lock, err := c.OpenFile(lockName, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("opening lock %s: %w", lockName, err)
	}
	defer func() { _ = lock.Close() }()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil { // #nosec G115 -- fd fits in int
		return fmt.Errorf("locking %s: %w", lockName, err)
	}
	defer func() { _ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) }() // #nosec G115 -- fd fits in int
	return fn()
}

func sameDir(a, b string) bool {
	ai, errA := os.Stat(a)
	bi, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(ai, bi)
}
