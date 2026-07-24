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
