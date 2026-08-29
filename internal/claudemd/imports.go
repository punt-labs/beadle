// Package claudemd adds and removes a single @-import line in a user-owned
// CLAUDE.md, disturbing no other byte — with one mandated exception below.
//
// The write is atomic (temp file in the target's own directory, then rename),
// byte-preserving across LF, CRLF, and lone-CR endings, idempotent by a
// terminator-insensitive match, and blind to lines inside a code block. It
// resolves a symlinked target, preserves an existing file's mode, and holds an
// exclusive flock for the whole read-modify-write. The correctness contract is
// ported from GlobalClaudeImports in the vox repo; the flock is the one
// addition the Tool Enable/Disable Standard (§2.4) requires beyond it.
//
// The one exception to byte-preservation: when the host file's final line has
// no terminator, Register adds a single EOL before the import so the two lines
// are not glued (§2.4 mandates this separator). Prune cannot attribute that
// separator to itself, so it does not strip it back off; a Register+Prune
// round-trip on a previously-unterminated file therefore leaves the original
// content plus one trailing EOL. Every other case round-trips byte-for-byte.
package claudemd

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// Guide is the agent-facing user guide that enable deposits into
// <repo>/.punt-labs/beadle/CLAUDE.md.
//
//go:embed guide.md
var Guide []byte

// newFileMode is the mode for a CLAUDE.md that Register creates; an existing
// file keeps its own mode.
const newFileMode = 0o644

// Register adds importLine to the CLAUDE.md at path if no top-level line
// already matches it, creating the file if it does not exist. It reports
// whether the file was written. Re-running is a no-op, so it is the upgrade
// path. importLine must be a single top-level @-import with no surrounding
// whitespace. When the host file's final line has no terminator, the appended
// import is preceded by one EOL per the standard; Prune does not remove that
// added newline, so this is the single case an enable+disable round-trip is
// not byte-for-byte (see the package comment).
//
// The import is always written at column 0, so it is top-level by construction
// (§2.4). A dangling code fence in the user's prose delimits nothing, so it
// never hides the appended line — Register appends it top-level and Prune
// re-matches it regardless of an unterminated opener above.
func Register(path, importLine string) (bool, error) {
	if err := validate(importLine); err != nil {
		return false, err
	}
	var wrote bool
	err := withLock(path, func(target string) error {
		content, err := read(target)
		if err != nil {
			return err
		}
		lines := splitKeepEnds(content)
		if present(lines, importLine) {
			return nil
		}
		next := append(lines, appended(lines, importLine))
		if err := write(target, strings.Join(next, "")); err != nil {
			return err
		}
		wrote = true
		return nil
	})
	return wrote, err
}

// Prune removes every top-level line matching importLine from the CLAUDE.md at
// path, collapsing an accidental duplicate to zero. It reports whether the file
// was written. A missing file is left untouched.
func Prune(path, importLine string) (bool, error) {
	pruned, _, err := prune(path, importLine, false)
	return pruned, err
}

// PruneAndDiscardEmpty prunes importLine and, when that leaves the file empty,
// removes the file — all under a single hold of the CLAUDE.md flock (Lock B).
// Doing the "is it now empty?" check and the removal inside the lock closes the
// TOCTOU an unlocked caller would leave open: between an unlocked prune and an
// unlocked remove, another tool could append its own import and beadle would
// then delete the no-longer-empty file, wiping content the flock exists to
// protect. It reports whether it pruned a line and whether it removed the file.
// Removal keeps the round-3 guards: only when a line was actually pruned, and
// only when path is a regular file — never a symlink (removing the real target
// would strand the link).
func PruneAndDiscardEmpty(path, importLine string) (pruned, removed bool, err error) {
	return prune(path, importLine, true)
}

// prune is the shared body of Prune and PruneAndDiscardEmpty. It holds Lock B
// for the whole read-modify-write and, when discardEmpty is set and the prune
// emptied a regular-file target, removes the file under that same lock.
func prune(path, importLine string, discardEmpty bool) (pruned, removed bool, err error) {
	if err := validate(importLine); err != nil {
		return false, false, err
	}
	err = withLock(path, func(target string) error {
		content, err := read(target)
		if err != nil {
			return err
		}
		lines := splitKeepEnds(content)
		kept := remove(lines, importLine)
		if len(kept) == len(lines) {
			return nil
		}
		joined := strings.Join(kept, "")
		if err := write(target, joined); err != nil {
			return err
		}
		pruned = true
		if !discardEmpty || joined != "" {
			return nil
		}
		// The file is empty. Remove it, but only when path is a regular file:
		// a symlinked path leaves the link (and its now-empty target) intact.
		fi, statErr := os.Lstat(path)
		if statErr != nil {
			return fmt.Errorf("stat %q: %w", path, statErr)
		}
		if fi.Mode().IsRegular() {
			if err := os.Remove(path); err != nil {
				return fmt.Errorf("removing emptied %q: %w", path, err)
			}
			removed = true
		}
		return nil
	})
	return pruned, removed, err
}

// validate rejects any import line that could not be a lone top-level @-import.
// Register and Prune splice the line in verbatim, so a padded, multi-line, or
// non-@ line would inject a blank line, a second import, or stray markdown.
func validate(line string) error {
	switch {
	case line == "" || strings.TrimSpace(line) == "":
		return fmt.Errorf("import line must be non-empty")
	case strings.ContainsAny(line, "\r\n"):
		return fmt.Errorf("import line must be a single line: %q", line)
	case line != strings.TrimSpace(line):
		return fmt.Errorf("import line must have no leading or trailing whitespace: %q", line)
	case !strings.HasPrefix(line, "@"):
		return fmt.Errorf("import line must begin with %q: %q", "@", line)
	}
	return nil
}

// present reports whether any top-level line matches importLine net of its
// terminator. Lines inside a code block are ignored.
func present(lines []string, importLine string) bool {
	found := false
	scanTopLevel(lines, func(_ int, b string) {
		if b == importLine {
			found = true
		}
	})
	return found
}

// remove returns lines with every top-level match of importLine dropped. Lines
// inside a code block are preserved even when they match.
func remove(lines []string, importLine string) []string {
	drop := make(map[int]bool)
	scanTopLevel(lines, func(i int, b string) {
		if b == importLine {
			drop[i] = true
		}
	})
	if len(drop) == 0 {
		return lines
	}
	kept := make([]string, 0, len(lines))
	for i, ln := range lines {
		if !drop[i] {
			kept = append(kept, ln)
		}
	}
	return kept
}

// scanTopLevel calls fn(index, body) for each line that resolves at the top
// level — outside any fenced or indented code block — passing the line net of
// its terminator. This is the code-block definition the Tool Enable/Disable
// Standard (§2.4) fixes so every implementation agrees, ported from the named
// reference ClaudeMdImport in punt-labs/biff #312. A line is non-top-level when
// it lies inside a matched fenced block (fencedRanges) or is itself an indented
// code line — a tab or four or more leading spaces. beadle's own import is
// written at column 0 with no info string, so it is top-level by construction
// unless it sits inside a genuine fenced block.
func scanTopLevel(lines []string, fn func(i int, body string)) {
	bodies := make([]string, len(lines))
	for i, ln := range lines {
		bodies[i] = trimTerminator(ln)
	}
	inside := make(map[int]bool)
	for _, r := range fencedRanges(bodies) {
		// The content and the closing delimiter are inside; the opener is not.
		for i := r[0] + 1; i <= r[1]; i++ {
			inside[i] = true
		}
	}
	for i, b := range bodies {
		if inside[i] || isIndented(b) {
			continue
		}
		fn(i, b)
	}
}

// fencedRanges returns the [open, close] index pairs of matched fenced blocks
// among the terminator-stripped bodies. A block opened by a run of N of a marker
// closes only on a later same-marker delimiter whose run is at least N: a ```
// block cannot be closed by ~~~ or by a shorter run, so a mismatched or shorter
// delimiter inside the block is content, not a close. Blocks do not nest — once
// open, every line up to the matching close is content. An unterminated opener
// is dropped (it delimits nothing), so a dangling fence in the user's prose
// above the import never swallows the rest of the file.
func fencedRanges(bodies []string) [][2]int {
	var ranges [][2]int
	open := -1
	var openMarker byte
	var openLen int
	for i, b := range bodies {
		marker, run, ok := parseFence(b)
		if open < 0 {
			if ok {
				open, openMarker, openLen = i, marker, run
			}
			continue
		}
		if ok && marker == openMarker && run >= openLen {
			ranges = append(ranges, [2]int{open, i})
			open = -1
		}
	}
	return ranges
}

// parseFence reports whether body is a fence delimiter and, if so, its marker
// character and run length. A delimiter is a non-indented line — no leading tab,
// at most three leading spaces (CommonMark) — whose first non-blank run is three
// or more of a single marker character (a backtick or a tilde), optionally
// followed by an info string. A tab- or four-or-more-space-indented ```/~~~ line
// is an inert indented-code line, never a delimiter (§2.4).
func parseFence(body string) (marker byte, run int, ok bool) {
	if strings.HasPrefix(body, "\t") {
		return 0, 0, false
	}
	stripped := strings.TrimLeft(body, " ")
	if len(body)-len(stripped) >= 4 {
		return 0, 0, false
	}
	if stripped == "" || (stripped[0] != '`' && stripped[0] != '~') {
		return 0, 0, false
	}
	marker = stripped[0]
	for run < len(stripped) && stripped[run] == marker {
		run++
	}
	if run < 3 {
		return 0, 0, false
	}
	return marker, run, true
}

// isIndented reports whether body is an indented code line — a leading tab or
// four or more leading spaces (§2.4).
func isIndented(body string) bool {
	if strings.HasPrefix(body, "\t") {
		return true
	}
	return len(body)-len(strings.TrimLeft(body, " ")) >= 4
}

// appended returns importLine terminated with the host file's EOL, prefixed
// with that EOL when the file's last line is unterminated so the import is
// never glued to the user's last line.
func appended(lines []string, importLine string) string {
	eol := hostEOL(lines)
	sep := ""
	if len(lines) > 0 && terminator(lines[len(lines)-1]) == "" {
		sep = eol
	}
	return sep + importLine + eol
}

// hostEOL returns the file's existing EOL convention — the terminator of its
// first terminated line — defaulting to "\n" for an empty or unterminated file.
func hostEOL(lines []string) string {
	for _, ln := range lines {
		if t := terminator(ln); t != "" {
			return t
		}
	}
	return "\n"
}

// terminator returns the trailing line terminator of line, or "" if the line
// has none.
func terminator(line string) string {
	switch {
	case strings.HasSuffix(line, "\r\n"):
		return "\r\n"
	case strings.HasSuffix(line, "\n"):
		return "\n"
	case strings.HasSuffix(line, "\r"):
		return "\r"
	}
	return ""
}

// trimTerminator returns line without its trailing \r, \n, or \r\n. A line
// carries at most one terminator, so no interior byte is touched.
func trimTerminator(line string) string {
	return strings.TrimRight(line, "\r\n")
}

// splitKeepEnds splits s into lines that each retain their terminator, so that
// strings.Join(splitKeepEnds(s), "") reproduces s byte-for-byte. A final line
// without a terminator is returned as-is.
func splitKeepEnds(s string) []string {
	var lines []string
	for i := 0; i < len(s); {
		j := i
		for j < len(s) && s[j] != '\n' && s[j] != '\r' {
			j++
		}
		if j < len(s) {
			if s[j] == '\r' && j+1 < len(s) && s[j+1] == '\n' {
				j += 2
			} else {
				j++
			}
		}
		lines = append(lines, s[i:j])
		i = j
	}
	return lines
}

// read returns the file's bytes verbatim, or "" when it does not exist. It does
// no newline translation, so a read/write round-trip preserves LF, CRLF, and
// lone-CR endings.
func read(path string) (string, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("reading %q: %w", path, err)
	}
	return string(data), nil
}

// write replaces path's contents with text atomically: a temp file in path's
// own directory is written, then renamed over the target. An existing file's
// mode is preserved; a new file gets newFileMode.
func write(path, text string) error {
	dir := filepath.Dir(path)
	mode := os.FileMode(newFileMode)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	} else if !os.IsNotExist(err) {
		// Only a missing file falls back to newFileMode; any other stat error
		// must surface rather than silently rewrite with a possibly-wrong mode.
		return fmt.Errorf("stat %q: %w", path, err)
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("creating temp file in %q: %w", dir, err)
	}
	tmpName := tmp.Name()
	if err := writeTemp(tmp, text, mode); err != nil {
		return errors.Join(err, removeTemp(tmpName))
	}
	if err := os.Rename(tmpName, path); err != nil {
		err = fmt.Errorf("renaming %q to %q: %w", tmpName, path, err)
		return errors.Join(err, removeTemp(tmpName))
	}
	return nil
}

// removeTemp deletes an orphaned temp file, reporting a failure that is not a
// missing file so the leftover is not silently ignored.
func removeTemp(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing temp file %q: %w", path, err)
	}
	return nil
}

// writeTemp writes text to the open temp file, fsyncs and closes it, and stamps
// mode. The rename in write depends on the bytes being durable first.
func writeTemp(tmp *os.File, text string, mode os.FileMode) error {
	if _, err := tmp.WriteString(text); err != nil {
		return errors.Join(fmt.Errorf("writing temp file %q: %w", tmp.Name(), err), tmp.Close())
	}
	if err := tmp.Sync(); err != nil {
		return errors.Join(fmt.Errorf("syncing temp file %q: %w", tmp.Name(), err), tmp.Close())
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing temp file %q: %w", tmp.Name(), err)
	}
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		return fmt.Errorf("setting mode on %q: %w", tmp.Name(), err)
	}
	return nil
}

// withLock resolves path (following a symlink to its real file), takes Lock B —
// the tool-agnostic sibling lock for the host CLAUDE.md — and runs fn with the
// resolved target. The lock serializes the whole read-modify-write against a
// parallel invocation, which atomic rename alone cannot: two unsynchronized
// writers each read the old bytes and the second clobbers the first.
func withLock(path string, fn func(target string) error) error {
	target, err := resolve(path)
	if err != nil {
		return err
	}
	lockPath, err := siblingLockPath(path)
	if err != nil {
		return err
	}
	return flockFile(lockPath, func() error { return fn(target) })
}

// The enable/disable guidance layer uses two nested locks:
//
//   - Lock A — the per-repo OPERATION lock. It serializes beadle's own enable
//     against disable so the two never interleave. Key: the canonical repo root,
//     hashed into a lock file in the OS temp dir. It is beadle-internal —
//     enable/disable acquire it through WithLock for the whole operation.
//   - Lock B — the per-CLAUDE.md-path flock. It serializes the CLAUDE.md
//     read-modify-write across ALL tools, not just beadle, so §2.4 mandates it
//     be the sibling ".<basename>.punt-import.lock" in the host file's own
//     directory — tool-agnostic by requirement, since vox, quarry, biff, and
//     beadle all mutate the same ~/.claude/CLAUDE.md and must take the identical
//     lock. Register and Prune acquire it through withLock.
//
// INVARIANT: every observation OR mutation of CLAUDE.md — reading it, appending
// the import, pruning it, and the "remove the file if pruning emptied it"
// decision (PruneAndDiscardEmpty) — happens while holding Lock B. Nothing
// touches CLAUDE.md state outside B.
//
// Deadlock freedom: Lock A nests Lock B (never the reverse). Lock A is a temp-dir
// hash lock and Lock B is a sibling file next to the CLAUDE.md, so their paths
// never coincide and a fixed acquire order over distinct locks cannot cycle.

// WithLock runs fn while holding Lock A — the per-repo OPERATION lock (see the
// note above). The lock file lives in the OS temp dir, named by the SHA-256 of
// key's canonical path, so two callers naming the same repo by different
// spellings (a symlinked parent, "./x", "a/../x") take the SAME lock, while
// distinct repos never contend. This is beadle-internal; the cross-tool
// CLAUDE.md lock is Lock B, the sibling file taken by withLock.
func WithLock(key string, fn func() error) error {
	canon, err := canonicalKey(key)
	if err != nil {
		return err
	}
	sum := sha256.Sum256([]byte(canon))
	lockPath := filepath.Join(os.TempDir(), "beadle-claudemd-"+hex.EncodeToString(sum[:])+".lock")
	return flockFile(lockPath, fn)
}

// siblingLockPath returns Lock B for the host CLAUDE.md: the sibling
// ".<basename>.punt-import.lock" in the host file's own directory (§2.4). The
// host path is symlink-canonicalized first, so two spellings of one file — a
// symlinked parent, or a CLAUDE.md symlinked into a dotfile store — take the
// identical lock, and vox, quarry, biff, and beadle all serialize on it. The
// lock is a separate file, never the host inode: the atomic rename that replaces
// the host would carry a lock held on the target away with the dead inode, so
// the next writer would serialize against nothing.
func siblingLockPath(host string) (string, error) {
	canon, err := canonicalKey(host)
	if err != nil {
		return "", err
	}
	dir, base := filepath.Split(canon)
	return filepath.Join(dir, "."+base+".punt-import.lock"), nil
}

// flockFile runs fn while holding an exclusive flock on lockPath, creating the
// lock file if it does not exist. Closing the file releases the flock; its
// close failure surfaces only when fn itself succeeded.
func flockFile(lockPath string, fn func() error) (err error) {
	lock, err := os.OpenFile(filepath.Clean(lockPath), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("opening lock %q: %w", lockPath, err)
	}
	defer func() {
		if cerr := lock.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("closing lock %q: %w", lockPath, cerr)
		}
	}()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("locking %q: %w", lockPath, err)
	}
	return fn()
}

// canonicalKey resolves key to a stable absolute path so different spellings of
// the same file take the same lock. It absolutizes first, then follows symlinks
// (including symlinked parents). For a path that does not exist yet — a
// CLAUDE.md about to be created — it canonicalizes the parent, which must exist,
// and rejoins the base, so the pre-create and post-create keys still agree.
func canonicalKey(key string) (string, error) {
	abs, err := filepath.Abs(key)
	if err != nil {
		return "", fmt.Errorf("resolving %q: %w", key, err)
	}
	if p, err := filepath.EvalSymlinks(abs); err == nil {
		return p, nil
	}
	dir, base := filepath.Split(abs)
	if d, err := filepath.EvalSymlinks(dir); err == nil {
		return filepath.Join(d, base), nil
	}
	return abs, nil
}

// resolve returns the real path a write must rename onto. A symlinked target
// (dotfile managers create these) is followed to its destination so the rename
// updates the file and preserves the link; a missing or plain path is returned
// unchanged.
func resolve(path string) (string, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		// A missing file is written fresh at path; any other stat error (a
		// permission or I/O fault) must not be swallowed, or a symlinked target
		// would be silently clobbered by a rename onto the link itself.
		if os.IsNotExist(err) {
			return path, nil
		}
		return "", fmt.Errorf("stat %q: %w", path, err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		return path, nil
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("resolving symlink %q: %w", path, err)
	}
	return resolved, nil
}
