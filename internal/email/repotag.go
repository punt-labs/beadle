package email

import (
	"bytes"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

// Repo-tag header names. X-Beadle-Repo carries the owner/repo slug so tools can
// filter a shared mailbox by repo; X-Beadle-Agent names the sending identity.
const (
	HeaderRepo  = "X-Beadle-Repo"
	HeaderAgent = "X-Beadle-Agent"
)

// RepoTag identifies the repository and agent that originate an outbound
// message. A zero RepoTag (empty Slug) carries no repo context: every compose
// path omits the subject tag and the X-Beadle-* headers and sends normally.
type RepoTag struct {
	Slug  string // owner/repo, e.g. "punt-labs/beadle"
	Agent string // ethos handle, e.g. "claude"; may be empty
}

// slugSCP matches scp-style SSH remotes (git@host:owner/repo[.git]).
// slugURL matches scheme remotes (https|ssh://host[:port]/owner/repo[.git]).
var (
	slugSCP = regexp.MustCompile(`^[^@]+@[^:]+:(.+?)(?:\.git)?$`)
	slugURL = regexp.MustCompile(`^(?:https?|ssh)://[^/]+(?::\d+)?/(.+?)(?:\.git)?$`)
)

// parseRepoSlug extracts owner/repo from a git remote URL, or "" when the URL
// does not parse to a two-part slug. Nested paths (e.g. a GitLab group) return
// "" because the tag convention is owner/repo only.
func parseRepoSlug(url string) string {
	url = strings.TrimSpace(url)
	for _, re := range []*regexp.Regexp{slugSCP, slugURL} {
		if m := re.FindStringSubmatch(url); m != nil {
			slug := m[1]
			if strings.Count(slug, "/") == 1 {
				return slug
			}
		}
	}
	return ""
}

// ResolveRepoTag builds a RepoTag from the working directory's git origin
// remote and the given agent handle. It returns a zero RepoTag when no git
// remote resolves, so a caller with no repo context still sends normally. It
// never fails a send.
func ResolveRepoTag(agent string) RepoTag {
	out, err := exec.Command("git", "remote", "get-url", "origin").Output()
	if err != nil {
		return RepoTag{}
	}
	slug := parseRepoSlug(string(out))
	if slug == "" {
		return RepoTag{}
	}
	return RepoTag{Slug: slug, Agent: agent}
}

// empty reports whether the tag carries no repo context.
func (t RepoTag) empty() bool { return t.Slug == "" }

// replyPrefix matches leading Re:/Fwd:/Fw: reply markers, case-insensitively
// and repeated, so a subject tag lands after them (matching GitHub's form).
var replyPrefix = regexp.MustCompile(`(?i)^((re|fwd|fw)\s*:\s*)+`)

// subject returns s with the repo tag inserted as "[slug] ". It is idempotent:
// a subject already carrying a leading "[owner/repo]" tag (after any Re:/Fwd:
// markers) is returned unchanged, so a reply keeps exactly one tag. A zero tag
// returns s unchanged.
func (t RepoTag) subject(s string) string {
	if t.empty() {
		return s
	}
	prefix := replyPrefix.FindString(s)
	rest := s[len(prefix):]
	if bracketTagged(rest) {
		return s
	}
	return prefix + "[" + t.Slug + "] " + rest
}

// bracketTagged reports whether s begins with an owner/repo bracket tag such as
// "[punt-labs/beadle]". The slash distinguishes a repo tag from an unrelated
// leading bracket like "[URGENT]".
func bracketTagged(s string) bool {
	if !strings.HasPrefix(s, "[") {
		return false
	}
	end := strings.IndexByte(s, ']')
	if end < 0 {
		return false
	}
	return strings.Contains(s[1:end], "/")
}

// headers returns the X-Beadle-* header map for the Resend JSON path, or nil
// when the tag carries no repo context.
func (t RepoTag) headers() map[string]string {
	if t.empty() {
		return nil
	}
	h := map[string]string{HeaderRepo: t.Slug}
	if t.Agent != "" {
		h[HeaderAgent] = t.Agent
	}
	return h
}

// writeHeaders appends the X-Beadle-* header lines to buf for the raw-MIME
// compose paths, or does nothing when the tag is empty. These are top-level
// RFC 822 headers, written outside any signed body, so they never alter a
// PGP/MIME signature. Values are rejected if they contain CR/LF.
func (t RepoTag) writeHeaders(buf *bytes.Buffer) error {
	if t.empty() {
		return nil
	}
	if strings.ContainsAny(t.Slug, "\r\n") || strings.ContainsAny(t.Agent, "\r\n") {
		return fmt.Errorf("repo tag header contains CR/LF")
	}
	fmt.Fprintf(buf, "%s: %s\r\n", HeaderRepo, t.Slug)
	if t.Agent != "" {
		fmt.Fprintf(buf, "%s: %s\r\n", HeaderAgent, t.Agent)
	}
	return nil
}
