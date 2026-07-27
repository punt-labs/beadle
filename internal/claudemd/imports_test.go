package claudemd

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const line = "@.punt-labs/beadle/CLAUDE.md"

// writeHost writes content to a fresh file in a temp dir and returns its path.
func writeHost(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "CLAUDE.md")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}

func readHost(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(data)
}

func TestRegister(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		wantWrote bool
		want      string
	}{
		{"absent file", "", true, line + "\n"},
		{"lf trailing newline", "# Title\n\nbody\n", true, "# Title\n\nbody\n" + line + "\n"},
		{"crlf preserved", "# Title\r\nbody\r\n", true, "# Title\r\nbody\r\n" + line + "\r\n"},
		{"lone cr preserved", "# Title\rbody\r", true, "# Title\rbody\r" + line + "\r"},
		{"no trailing newline gets separator", "hello", true, "hello\n" + line + "\n"},
		{"already present is no-op", "# Title\n" + line + "\n", false, "# Title\n" + line + "\n"},
		{
			"match inside fenced block is not seen",
			"# Title\n```\n" + line + "\n```\n",
			true,
			"# Title\n```\n" + line + "\n```\n" + line + "\n",
		},
		{
			"match in indented block is not seen",
			"# Title\n\n    " + line + "\n",
			true,
			"# Title\n\n    " + line + "\n" + line + "\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var path string
			if tt.name == "absent file" {
				path = filepath.Join(t.TempDir(), "CLAUDE.md")
			} else {
				path = writeHost(t, tt.content)
			}
			wrote, err := Register(path, line)
			require.NoError(t, err)
			assert.Equal(t, tt.wantWrote, wrote)
			assert.Equal(t, tt.want, readHost(t, path))
		})
	}
}

func TestRegisterIdempotent(t *testing.T) {
	path := writeHost(t, "# Title\n")
	wrote, err := Register(path, line)
	require.NoError(t, err)
	require.True(t, wrote)
	first := readHost(t, path)

	wrote, err = Register(path, line)
	require.NoError(t, err)
	assert.False(t, wrote, "second register must not write")
	assert.Equal(t, first, readHost(t, path), "re-run must not change bytes")
}

// TestFenceAudit drives every fence and code-block shape through the core
// contract: enable is idempotent (a second Register appends no duplicate) and
// disable round-trips (Prune restores the original bytes). content is the user's
// file before any import; the column-0 import beadle appends is always
// top-level, so a dangling opener never hides it — it delimits nothing (§2.4).
func TestFenceAudit(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{"balanced backtick fence with info string", "# T\n```go\ncode\n```\n"},
		{"balanced tilde fence", "# T\n~~~\ncode\n~~~\n"},
		{"balanced tilde fence with info string", "# T\n~~~python\ncode\n~~~\n"},
		{"crlf-terminated fence lines", "# T\r\n```\r\ncode\r\n```\r\n"},
		{"import-matching line inside a fence", "# T\n```\n" + line + "\n```\n"},
		{"indented code block", "# T\n\n    " + line + "\n"},
		{"backticks then tildes, balanced", "```\na\n```\n~~~\nb\n~~~\n"},
		// A dangling opener delimits nothing: the appended import stays top-level.
		{"only a dangling fence", "```\n"},
		{"dangling fence with info string", "# T\n```go\ncode\n"},
		// ``` cannot close a ~~~ block, but a later ~~~ can, so this closes.
		{"tilde block with an inner backtick line, balanced", "~~~\ncode\n```\ncode\n~~~\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeHost(t, tt.content)

			wrote, err := Register(path, line)
			require.NoError(t, err)
			require.True(t, wrote)
			afterFirst := readHost(t, path)

			wrote, err = Register(path, line)
			require.NoError(t, err)
			assert.False(t, wrote, "second enable must append no duplicate")
			assert.Equal(t, afterFirst, readHost(t, path), "idempotent re-run")

			_, err = Prune(path, line)
			require.NoError(t, err)
			assert.Equal(t, tt.content, readHost(t, path), "disable restores the original bytes")
		})
	}
}

// TestPresentFenceSemantics pins the balanced-pair fence rules (§2.4) at the
// present() boundary: an import inside a real fenced block is hidden, but a
// dangling or mismatched opener delimits nothing and leaves the import visible.
func TestPresentFenceSemantics(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{"import inside a real fenced block is hidden", "# T\n```\n" + line + "\n```\n", false},
		{"import inside a tilde block is hidden", "# T\n~~~\n" + line + "\n~~~\n", false},
		{"dangling opener above import leaves it visible", "```\n" + line + "\n", true},
		{"indented fences are inert, import top-level", "    ```\n" + line + "\n    ```\n", true},
		{"backtick cannot close a tilde block, opener dangles", "~~~\n" + line + "\n```\n", true},
		{"shorter run cannot close, longer opener dangles", "````\n" + line + "\n```\n", true},
		{"inner tilde does not toggle a backtick block", "```\n~~~\n" + line + "\n```\n", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := present(splitKeepEnds(tt.content), line)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestDanglingFenceAboveImportStillPruned is the regression proving GAP 2 fixed.
// A stray ``` in the user's prose above a registered import used to flip the
// naive parity scan for the rest of the file: beadle misread its own column-0
// import as fenced and disable left it — a dead @-import that 404s every
// session. The balanced-pair scan treats the dangling opener as delimiting
// nothing, so the import stays top-level and Prune removes it. Under the old
// logic Prune would report no change and strand the import.
func TestDanglingFenceAboveImportStillPruned(t *testing.T) {
	content := "# Notes\n\n```\nsome unclosed snippet\n\n" + line + "\n"
	path := writeHost(t, content)

	pruned, err := Prune(path, line)
	require.NoError(t, err)
	assert.True(t, pruned, "the import below a dangling fence is top-level and is pruned")
	assert.Equal(t, "# Notes\n\n```\nsome unclosed snippet\n\n", readHost(t, path),
		"only the import line is removed; the user's dangling fence is left intact")
}

func TestPrune(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		wantWrote bool
		want      string
	}{
		{"removes top-level line", "# Title\n" + line + "\n", true, "# Title\n"},
		{
			"collapses duplicate to zero",
			"# Title\n" + line + "\n" + line + "\n",
			true,
			"# Title\n",
		},
		{
			"fenced match left untouched",
			"# Title\n```\n" + line + "\n```\n",
			false,
			"# Title\n```\n" + line + "\n```\n",
		},
		{"absent line is no-op", "# Title\nbody\n", false, "# Title\nbody\n"},
		{
			"removes top-level keeps fenced",
			"# Title\n```\n" + line + "\n```\n" + line + "\n",
			true,
			"# Title\n```\n" + line + "\n```\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeHost(t, tt.content)
			wrote, err := Prune(path, line)
			require.NoError(t, err)
			assert.Equal(t, tt.wantWrote, wrote)
			assert.Equal(t, tt.want, readHost(t, path))
		})
	}
}

func TestPruneAndDiscardEmpty(t *testing.T) {
	tests := []struct {
		name        string
		content     string
		wantPruned  bool
		wantRemoved bool
		wantExists  bool
		wantFile    string // checked only when wantExists
	}{
		{"sole import removed then file discarded", line + "\n", true, true, false, ""},
		{"import among content leaves non-empty file", "# T\n" + line + "\n", true, false, true, "# T\n"},
		{"no import present is a no-op, file kept", "# T\n", false, false, true, "# T\n"},
		{"pre-existing empty file kept (nothing pruned)", "", false, false, true, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeHost(t, tt.content)
			pruned, removed, err := PruneAndDiscardEmpty(path, line)
			require.NoError(t, err)
			assert.Equal(t, tt.wantPruned, pruned, "pruned")
			assert.Equal(t, tt.wantRemoved, removed, "removed")
			_, statErr := os.Stat(path)
			if tt.wantExists {
				require.NoError(t, statErr, "file must remain")
				assert.Equal(t, tt.wantFile, readHost(t, path))
			} else {
				assert.True(t, os.IsNotExist(statErr), "emptied file must be gone")
			}
		})
	}
}

func TestPruneAndDiscardEmptyKeepsSymlink(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real-CLAUDE.md")
	require.NoError(t, os.WriteFile(real, []byte(line+"\n"), 0o644))
	link := filepath.Join(dir, "CLAUDE.md")
	require.NoError(t, os.Symlink(real, link))

	pruned, removed, err := PruneAndDiscardEmpty(link, line)
	require.NoError(t, err)
	assert.True(t, pruned)
	assert.False(t, removed, "a symlinked path is never removed")

	fi, err := os.Lstat(link)
	require.NoError(t, err)
	assert.NotZero(t, fi.Mode()&os.ModeSymlink, "the link survives")
	assert.Equal(t, "", readHost(t, real), "its target is emptied, not deleted")
}

// TestPruneAndDiscardEmptyNoTOCTOUWipe proves the removal cannot wipe a
// concurrent writer's content. A registrar adding a DIFFERENT import races the
// prune-and-discard of beadle's import on the same file. Because the emptiness
// check and the removal both hold Lock B, the two serialize: whichever order
// runs, the registrar's line is present at the end and the file exists.
func TestPruneAndDiscardEmptyNoTOCTOUWipe(t *testing.T) {
	const other = "@.punt-labs/other/CLAUDE.md"
	path := writeHost(t, line+"\n")

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _, err := PruneAndDiscardEmpty(path, line)
		assert.NoError(t, err)
	}()
	go func() {
		defer wg.Done()
		_, err := Register(path, other)
		assert.NoError(t, err)
	}()
	wg.Wait()

	data, err := os.ReadFile(path)
	require.NoError(t, err, "the file exists — a momentary empty removal is refilled by the registrar")
	assert.Contains(t, string(data), other, "the concurrent registrar's import is never wiped")
}

func TestCanonicalKeySameForSpellings(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	require.NoError(t, os.Symlink(real, link))

	viaReal, err := canonicalKey(filepath.Join(real, "CLAUDE.md"))
	require.NoError(t, err)
	viaLink, err := canonicalKey(filepath.Join(link, "CLAUDE.md"))
	require.NoError(t, err)
	assert.Equal(t, viaReal, viaLink, "a symlinked parent keys the same lock as the real path")

	viaDots, err := canonicalKey(filepath.Join(real, "sub", "..", "CLAUDE.md"))
	require.NoError(t, err)
	assert.Equal(t, viaReal, viaDots, `"a/../x" keys the same lock as "x"`)

	// Neither the file nor its parent exists: fall back to the cleaned absolute
	// path so the key is still deterministic rather than an error.
	deep := filepath.Join(real, "missing-dir", "CLAUDE.md")
	viaDeep, err := canonicalKey(deep)
	require.NoError(t, err)
	assert.Equal(t, deep, viaDeep, "an unresolvable path keys on its cleaned absolute form")
}

func TestSiblingLockPath(t *testing.T) {
	dir := t.TempDir()
	host := filepath.Join(dir, "CLAUDE.md")
	require.NoError(t, os.WriteFile(host, []byte("x\n"), 0o644))

	got, err := siblingLockPath(host)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, ".CLAUDE.md.punt-import.lock"), got,
		"Lock B is the tool-agnostic sibling in the host's own directory (§2.4)")
}

func TestSiblingLockPathCanonicalizes(t *testing.T) {
	// A CLAUDE.md symlinked into a dotfile store keys its lock next to the real
	// file, so a tool naming the file through the link and one naming it directly
	// take the identical lock — the cross-tool serialization §2.4 requires.
	store := t.TempDir()
	real := filepath.Join(store, "CLAUDE.md")
	require.NoError(t, os.WriteFile(real, []byte("x\n"), 0o644))
	link := filepath.Join(t.TempDir(), "CLAUDE.md")
	require.NoError(t, os.Symlink(real, link))

	viaReal, err := siblingLockPath(real)
	require.NoError(t, err)
	viaLink, err := siblingLockPath(link)
	require.NoError(t, err)
	assert.Equal(t, viaReal, viaLink, "a symlinked host keys the same lock as its real target")
}

func TestPruneMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "CLAUDE.md")
	wrote, err := Prune(path, line)
	require.NoError(t, err)
	assert.False(t, wrote)
	_, err = os.Stat(path)
	assert.True(t, os.IsNotExist(err), "prune must not create the file")
}

func TestRegisterPreservesMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "CLAUDE.md")
	require.NoError(t, os.WriteFile(path, []byte("# Title\n"), 0o600))

	wrote, err := Register(path, line)
	require.NoError(t, err)
	require.True(t, wrote)

	fi, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), fi.Mode().Perm(), "existing mode must survive")
}

func TestRegisterNewFileMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "CLAUDE.md")
	wrote, err := Register(path, line)
	require.NoError(t, err)
	require.True(t, wrote)

	fi, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(newFileMode), fi.Mode().Perm(), "new file gets 0644")
}

func TestRegisterFollowsSymlink(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real-CLAUDE.md")
	require.NoError(t, os.WriteFile(real, []byte("# Title\n"), 0o644))
	link := filepath.Join(dir, "CLAUDE.md")
	require.NoError(t, os.Symlink(real, link))

	wrote, err := Register(link, line)
	require.NoError(t, err)
	require.True(t, wrote)

	fi, err := os.Lstat(link)
	require.NoError(t, err)
	assert.NotZero(t, fi.Mode()&os.ModeSymlink, "the link must survive, not be clobbered")
	assert.Equal(t, "# Title\n"+line+"\n", readHost(t, real), "the real file gets the import")
}

func TestConcurrentRegisterAppendsOnce(t *testing.T) {
	path := writeHost(t, "# Title\n")
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := Register(path, line)
			assert.NoError(t, err)
		}()
	}
	wg.Wait()

	// The flock serializes the read-modify-write, so parallel writers see each
	// other's append and the line lands exactly once.
	assert.Equal(t, "# Title\n"+line+"\n", readHost(t, path))
}

func TestRegisterStatErrorNotSwallowed(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "afile")
	require.NoError(t, os.WriteFile(file, []byte("x"), 0o644))

	// A path whose parent is a regular file: Lstat fails with ENOTDIR, which is
	// not "not exist". resolve must surface it rather than treat it as absent
	// and risk clobbering a symlink on a transient stat fault.
	_, err := Register(filepath.Join(file, "CLAUDE.md"), line)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stat")
}

func TestUnterminatedRoundTripGainsOneEOL(t *testing.T) {
	// The one documented non-byte-preserving case: a file whose last line has
	// no terminator gains one EOL when Register adds the mandated separator, and
	// Prune does not strip it back off. The round-trip therefore leaves the
	// original content plus exactly one trailing "\n"; every other byte matches.
	path := writeHost(t, "no newline here")

	_, err := Register(path, line)
	require.NoError(t, err)
	assert.Equal(t, "no newline here\n"+line+"\n", readHost(t, path))

	wrote, err := Prune(path, line)
	require.NoError(t, err)
	require.True(t, wrote)
	assert.Equal(t, "no newline here\n", readHost(t, path),
		"original content plus the single mandated trailing EOL")
}

func TestWriteStatErrorNotSwallowed(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "afile")
	require.NoError(t, os.WriteFile(file, []byte("x"), 0o644))

	// A path under a regular file: os.Stat fails with ENOTDIR, not "not exist".
	// write must surface it rather than fall back to newFileMode and rewrite
	// with a possibly-wrong mode.
	err := write(filepath.Join(file, "CLAUDE.md"), "data")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stat")
}

func TestRemoveTemp(t *testing.T) {
	dir := t.TempDir()
	assert.NoError(t, removeTemp(filepath.Join(dir, "gone")), "a missing temp is not an error")

	f := filepath.Join(dir, "orphan.tmp")
	require.NoError(t, os.WriteFile(f, []byte("x"), 0o644))
	require.NoError(t, removeTemp(f))
	_, err := os.Stat(f)
	assert.True(t, os.IsNotExist(err), "the orphan is gone")
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name string
		line string
	}{
		{"empty", ""},
		{"whitespace only", "   "},
		{"embedded newline", "@a\n@b"},
		{"embedded cr", "@a\r@b"},
		{"leading space", " @line"},
		{"trailing space", "@line "},
		{"no at prefix", "line"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Register(filepath.Join(t.TempDir(), "CLAUDE.md"), tt.line)
			assert.Error(t, err)
			_, err = Prune(filepath.Join(t.TempDir(), "CLAUDE.md"), tt.line)
			assert.Error(t, err)
		})
	}
}
