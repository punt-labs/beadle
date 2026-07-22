package email

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"regexp"
	"strings"
	"time"
	"unicode"
)

// Repo-tag header names. X-Beadle-Repo carries the owner/repo slug so tools can
// filter a shared mailbox by repo; X-Beadle-Agent names the sending identity.
const (
	HeaderRepo  = "X-Beadle-Repo"
	HeaderAgent = "X-Beadle-Agent"
)

// gitRemoteTimeout bounds the git subprocess that ResolveRepoTag spawns.
const gitRemoteTimeout = 2 * time.Second

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
// does not parse to a two-part slug. Nested paths (e.g. a GitLab group) and
// slugs carrying control characters return "" — the regexp dot matches CR, so
// a control character must be rejected explicitly rather than reaching a header.
//
// A trailing ".git" is stripped by design: git clone does the same, so the
// remote "owner/my.git" names the repo "my", and the tag becomes
// "[owner/my]". A repository literally named "my.git" is therefore tagged as
// "my" — the git-clone convention wins over that rare literal name.
func parseRepoSlug(url string) string {
	url = strings.TrimSpace(url)
	for _, re := range []*regexp.Regexp{slugSCP, slugURL} {
		if m := re.FindStringSubmatch(url); m != nil {
			slug := m[1]
			if strings.Count(slug, "/") == 1 && !strings.ContainsFunc(slug, unicode.IsControl) {
				return slug
			}
		}
	}
	return ""
}

// ResolveRepoTag builds a RepoTag from the working directory's git origin
// remote and the given agent handle. It returns a zero RepoTag when no git
// remote resolves or the URL is not an owner/repo slug, so a caller with no
// repo context still sends normally — it never fails a send. The git lookup is
// bounded by ctx and a short internal timeout. Skips are logged at Debug with a
// distinct reason; the remote URL is never logged, since it may embed a token.
func ResolveRepoTag(ctx context.Context, logger *slog.Logger, agent string) RepoTag {
	if logger == nil {
		logger = slog.Default()
	}
	ctx, cancel := context.WithTimeout(ctx, gitRemoteTimeout)
	defer cancel()

	out, err := exec.CommandContext(ctx, "git", "remote", "get-url", "origin").Output()
	if err != nil {
		logger.Debug("repo tag skipped: git origin remote unavailable", "err", err)
		return RepoTag{}
	}
	slug := parseRepoSlug(string(out))
	if slug == "" {
		logger.Debug("repo tag skipped: git remote is not an owner/repo slug")
		return RepoTag{}
	}
	// Drop a tainted agent handle at the source so neither the SMTP nor the
	// Resend header path can carry a control character.
	if strings.ContainsFunc(agent, unicode.IsControl) {
		logger.Debug("repo tag: dropping agent handle with control characters")
		agent = ""
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
	if t.bracketTagged(rest) {
		return s
	}
	return prefix + "[" + t.Slug + "] " + rest
}

// bracketTagged reports whether s begins with a bracket tag owned by the same
// org as this tag — a leading "[owner/repo]" whose owner matches t.Slug's owner
// case-insensitively (GitHub owners are case-insensitive and clone-URL casing
// often differs from notification casing). This preserves org repo tags,
// including a cross-repo reply within the org (e.g. "[punt-labs/lux]" seen by
// beadle), while leaving phrase prefixes like "[CI/CD]" or "[1/2]" and any
// malformed bracket ("[punt-labs/CI/CD]", "[punt-labs/]") to receive the tag.
func (t RepoTag) bracketTagged(s string) bool {
	if !strings.HasPrefix(s, "[") {
		return false
	}
	end := strings.IndexByte(s, ']')
	if end < 0 {
		return false
	}
	owner, _, ok := splitSlug(s[1:end])
	if !ok {
		return false
	}
	curOwner, _, _ := strings.Cut(t.Slug, "/")
	return strings.EqualFold(owner, curOwner)
}

// splitSlug splits a well-formed "owner/repo" token into its parts. It returns
// ok=false unless the token has exactly one slash, both parts are non-empty,
// and it carries no whitespace or control characters — the same shape
// parseRepoSlug accepts.
func splitSlug(s string) (owner, repo string, ok bool) {
	owner, repo, ok = strings.Cut(s, "/")
	if !ok || owner == "" || repo == "" {
		return "", "", false
	}
	if strings.ContainsRune(repo, '/') {
		return "", "", false
	}
	if strings.ContainsFunc(s, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}) {
		return "", "", false
	}
	return owner, repo, true
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
