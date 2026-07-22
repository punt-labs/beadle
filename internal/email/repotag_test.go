package email

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseRepoSlug(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want string
	}{
		{"scp ssh", "git@github.com:punt-labs/beadle.git", "punt-labs/beadle"},
		{"scp ssh no suffix", "git@github.com:punt-labs/beadle", "punt-labs/beadle"},
		{"https", "https://github.com/punt-labs/beadle.git", "punt-labs/beadle"},
		{"https no suffix", "https://github.com/punt-labs/beadle", "punt-labs/beadle"},
		{"ssh scheme with port", "ssh://git@github.com:22/punt-labs/beadle.git", "punt-labs/beadle"},
		{"trailing whitespace", "git@github.com:punt-labs/beadle.git\n", "punt-labs/beadle"},
		{"nested path rejected", "https://gitlab.com/group/sub/repo.git", ""},
		{"control char rejected", "git@github.com:punt-labs\r/beadle", ""},
		// Stripping a trailing ".git" is git-clone convention: a remote named
		// "owner/my.git" clones into "my", so the tag is "owner/my".
		{"repo named my.git strips to my", "git@github.com:punt-labs/my.git", "punt-labs/my"},
		{"garbage", "not-a-url", ""},
		{"empty", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, parseRepoSlug(tc.url))
		})
	}
}

func TestRepoTag_Subject(t *testing.T) {
	tag := RepoTag{Slug: "punt-labs/beadle", Agent: "claude"}
	tests := []struct {
		name string
		tag  RepoTag
		in   string
		want string
	}{
		{"empty tag unchanged", RepoTag{}, "Hello", "Hello"},
		{"fresh subject tagged", tag, "Hello", "[punt-labs/beadle] Hello"},
		{"same repo not re-tagged", tag, "[punt-labs/beadle] Hello", "[punt-labs/beadle] Hello"},
		{"same owner other repo not re-tagged", tag, "[punt-labs/ethos] Hi", "[punt-labs/ethos] Hi"},
		{"same owner sibling repo not re-tagged", tag, "[punt-labs/lux] Hi", "[punt-labs/lux] Hi"},
		{"reply with org tag not re-tagged", tag, "Re: [punt-labs/lux] Hi", "Re: [punt-labs/lux] Hi"},
		{"reply fresh tagged after prefix", tag, "Re: Hello", "Re: [punt-labs/beadle] Hello"},
		{"fwd fresh tagged after prefix", tag, "Fwd: Hello", "Fwd: [punt-labs/beadle] Hello"},
		{"bare non-repo bracket still tagged", tag, "[URGENT] fix", "[punt-labs/beadle] [URGENT] fix"},
		// A different owner is a phrase prefix, not a repo tag — it must still be tagged.
		{"different owner repo tagged", tag, "[owner/repo] x", "[punt-labs/beadle] [owner/repo] x"},
		{"CI/CD phrase tagged", tag, "[CI/CD] pipeline", "[punt-labs/beadle] [CI/CD] pipeline"},
		{"UI/UX phrase tagged", tag, "[UI/UX] review", "[punt-labs/beadle] [UI/UX] review"},
		{"A/B phrase tagged", tag, "[A/B] test", "[punt-labs/beadle] [A/B] test"},
		{"AI/ML phrase tagged", tag, "[AI/ML] notes", "[punt-labs/beadle] [AI/ML] notes"},
		// A numeric or spaced bracket is not a repo tag — it must still be tagged.
		{"numeric fraction tagged", tag, "[1/2] status", "[punt-labs/beadle] [1/2] status"},
		{"numeric date tagged", tag, "[7/22] standup", "[punt-labs/beadle] [7/22] standup"},
		{"spaced prefix tagged", tag, "[Part 1/2] notes", "[punt-labs/beadle] [Part 1/2] notes"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.tag.subject(tc.in))
		})
	}
}

func TestRepoTag_SubjectIdempotent(t *testing.T) {
	tag := RepoTag{Slug: "punt-labs/beadle", Agent: "claude"}
	once := tag.subject("Hello")
	twice := tag.subject(once)
	assert.Equal(t, once, twice, "tagging an already-tagged subject must be a no-op")
}

func TestRepoTag_Headers(t *testing.T) {
	tests := []struct {
		name string
		tag  RepoTag
		want map[string]string
	}{
		{"empty tag nil", RepoTag{}, nil},
		{"slug only", RepoTag{Slug: "punt-labs/beadle"}, map[string]string{HeaderRepo: "punt-labs/beadle"}},
		{"slug and agent", RepoTag{Slug: "punt-labs/beadle", Agent: "claude"}, map[string]string{HeaderRepo: "punt-labs/beadle", HeaderAgent: "claude"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.tag.headers())
		})
	}
}

func TestRepoTag_WriteHeaders(t *testing.T) {
	var buf bytes.Buffer
	tag := RepoTag{Slug: "punt-labs/beadle", Agent: "claude"}
	require.NoError(t, tag.writeHeaders(&buf))
	assert.Equal(t, "X-Beadle-Repo: punt-labs/beadle\r\nX-Beadle-Agent: claude\r\n", buf.String())

	// Empty tag writes nothing.
	var empty bytes.Buffer
	require.NoError(t, RepoTag{}.writeHeaders(&empty))
	assert.Empty(t, empty.String())
}

func TestRepoTag_WriteHeadersRejectsCRLF(t *testing.T) {
	tests := []struct {
		name string
		tag  RepoTag
	}{
		{"slug CRLF", RepoTag{Slug: "punt-labs/beadle\r\nBcc: evil@evil.com"}},
		{"agent CRLF", RepoTag{Slug: "punt-labs/beadle", Agent: "claude\r\nX: y"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			err := tc.tag.writeHeaders(&buf)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "CR/LF")
		})
	}
}

// gitInit runs "git <args>" in dir and fails the test on error.
func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
}

// tempRepoDir creates an isolated directory under /tmp (outside any parent git
// repo) and chdirs into it for the test. A short /tmp path keeps ResolveRepoTag,
// which reads the process working directory, off the beadle repo it lives in.
func tempRepoDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "bg-repotag-*")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(dir) })
	t.Chdir(dir)
	return dir
}

func TestResolveRepoTag(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}

	t.Run("origin remote resolves", func(t *testing.T) {
		dir := tempRepoDir(t)
		gitRun(t, dir, "init", "-q")
		gitRun(t, dir, "remote", "add", "origin", "git@github.com:punt-labs/beadle.git")

		tag := ResolveRepoTag(context.Background(), nil, "claude")
		assert.Equal(t, "punt-labs/beadle", tag.Slug)
		assert.Equal(t, "claude", tag.Agent)
	})

	t.Run("no origin remote yields zero tag", func(t *testing.T) {
		dir := tempRepoDir(t)
		gitRun(t, dir, "init", "-q")

		tag := ResolveRepoTag(context.Background(), nil, "claude")
		assert.True(t, tag.empty(), "no remote must yield a zero tag")
	})

	t.Run("not a git repo yields zero tag", func(t *testing.T) {
		tempRepoDir(t) // no git init

		tag := ResolveRepoTag(context.Background(), nil, "claude")
		assert.True(t, tag.empty(), "outside a repo must yield a zero tag")
	})

	t.Run("nested path remote yields zero tag", func(t *testing.T) {
		dir := tempRepoDir(t)
		gitRun(t, dir, "init", "-q")
		gitRun(t, dir, "remote", "add", "origin", "https://gitlab.com/group/sub/repo.git")

		tag := ResolveRepoTag(context.Background(), nil, "claude")
		assert.True(t, tag.empty(), "a nested-path remote is not an owner/repo slug")
	})

	t.Run("control-char agent handle dropped", func(t *testing.T) {
		dir := tempRepoDir(t)
		gitRun(t, dir, "init", "-q")
		gitRun(t, dir, "remote", "add", "origin", "git@github.com:punt-labs/beadle.git")

		tag := ResolveRepoTag(context.Background(), nil, "cl\raude")
		assert.Equal(t, "punt-labs/beadle", tag.Slug)
		assert.Empty(t, tag.Agent, "a tainted agent handle must be dropped")
	})
}
