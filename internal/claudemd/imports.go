// Package claudemd adds and removes a single @-import line in a user-owned
// CLAUDE.md without disturbing any other byte of the file.
//
// The write is atomic (temp file in the target's own directory, then rename),
// byte-preserving across LF, CRLF, and lone-CR endings, idempotent by a
// terminator-insensitive match, and blind to lines inside a code block. It
// resolves a symlinked target, preserves an existing file's mode, and holds an
// exclusive flock for the whole read-modify-write. The correctness contract is
// ported from GlobalClaudeImports in the vox repo; the flock is the one
// addition the Tool Enable/Disable Standard (§2.4) requires beyond it.
package claudemd

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
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
// whitespace.
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
		kept := remove(lines, importLine)
		if len(kept) == len(lines) {
			return nil
		}
		if err := write(target, strings.Join(kept, "")); err != nil {
			return err
		}
		wrote = true
		return nil
	})
	return wrote, err
}

// validate rejects any import line that could not be a lone top-level @-import.
// register and prune splice the line in verbatim, so a padded, multi-line, or
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
// its terminator. A fence delimiter is a line whose first non-whitespace run is
// three or more backticks or tildes; each one flips the fenced state. An
// indented code block line begins with a tab or four or more spaces. This is
// the code-block definition the Tool Enable/Disable Standard (§2.4) fixes so
// every implementation agrees.
func scanTopLevel(lines []string, fn func(i int, body string)) {
	inFence := false
	for i, ln := range lines {
		b := trimTerminator(ln)
		if isFence(b) {
			inFence = !inFence
			continue
		}
		if inFence || isIndented(b) {
			continue
		}
		fn(i, b)
	}
}

func isFence(body string) bool {
	s := strings.TrimLeft(body, " \t")
	return strings.HasPrefix(s, "```") || strings.HasPrefix(s, "~~~")
}

func isIndented(body string) bool {
	return strings.HasPrefix(body, "\t") || strings.HasPrefix(body, "    ")
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
	data, err := os.ReadFile(path)
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
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("creating temp file in %q: %w", dir, err)
	}
	tmpName := tmp.Name()
	if err := writeTemp(tmp, text, mode); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("renaming %q to %q: %w", tmpName, path, err)
	}
	return nil
}

// writeTemp writes text to the open temp file, fsyncs and closes it, and stamps
// mode. The rename in write depends on the bytes being durable first.
func writeTemp(tmp *os.File, text string, mode os.FileMode) error {
	if _, err := tmp.WriteString(text); err != nil {
		tmp.Close()
		return fmt.Errorf("writing temp file %q: %w", tmp.Name(), err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("syncing temp file %q: %w", tmp.Name(), err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing temp file %q: %w", tmp.Name(), err)
	}
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		return fmt.Errorf("setting mode on %q: %w", tmp.Name(), err)
	}
	return nil
}

// withLock resolves path (following a symlink to its real file), takes an
// exclusive flock keyed on the resolved path, and runs fn with the resolved
// target. The lock serializes the whole read-modify-write against a parallel
// invocation, which atomic rename alone cannot: two unsynchronized writers each
// read the old bytes and the second clobbers the first.
func withLock(path string, fn func(target string) error) (err error) {
	target, err := resolve(path)
	if err != nil {
		return err
	}
	abs, err := filepath.Abs(target)
	if err != nil {
		return fmt.Errorf("resolving %q: %w", target, err)
	}
	sum := sha256.Sum256([]byte(abs))
	lockPath := filepath.Join(os.TempDir(), "beadle-claudemd-"+hex.EncodeToString(sum[:])+".lock")
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("opening lock %q: %w", lockPath, err)
	}
	defer func() {
		// Close releases the flock; report its failure only if fn succeeded.
		if cerr := lock.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("closing lock %q: %w", lockPath, cerr)
		}
	}()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("locking %q: %w", lockPath, err)
	}
	return fn(target)
}

// resolve returns the real path a write must rename onto. A symlinked target
// (dotfile managers create these) is followed to its destination so the rename
// updates the file and preserves the link; a missing or plain path is returned
// unchanged.
func resolve(path string) (string, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return path, nil
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		return path, nil
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("resolving symlink %q: %w", path, err)
	}
	return real, nil
}
