package email

import (
	"crypto/tls"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"

	"github.com/punt-labs/beadle/internal/channel"
	"github.com/punt-labs/beadle/internal/pgp"
)

// Client wraps an IMAP connection to Proton Bridge.
type Client struct {
	imap   *imapclient.Client
	cfg    *Config
	logger *slog.Logger
}

// Dial connects to the IMAP server. Uses implicit TLS for port 993,
// STARTTLS otherwise.
func Dial(cfg *Config, logger *slog.Logger) (*Client, error) {
	addr := net.JoinHostPort(cfg.IMAPHost, strconv.Itoa(cfg.IMAPPort))

	tlsCfg := &tls.Config{
		ServerName:         cfg.IMAPHost,
		InsecureSkipVerify: cfg.TLSSkipVerify || isLoopback(cfg.IMAPHost), //nolint:gosec // Proton Bridge uses self-signed certs on localhost or behind Docker host networking
	}

	opts := &imapclient.Options{TLSConfig: tlsCfg}

	var c *imapclient.Client
	var err error

	if cfg.IMAPPort == 993 {
		// Implicit TLS (IMAPS) — standard for Fastmail, Gmail, etc.
		c, err = imapclient.DialTLS(addr, opts)
	} else {
		// STARTTLS — Proton Bridge on localhost, or explicit config
		var conn net.Conn
		conn, err = net.DialTimeout("tcp", addr, 10*time.Second)
		if err != nil {
			return nil, fmt.Errorf("dial %s: %w", addr, err)
		}
		c, err = imapclient.NewStartTLS(conn, opts)
		if err != nil {
			conn.Close()
		}
	}
	if err != nil {
		return nil, fmt.Errorf("imap connect %s: %w", addr, err)
	}

	password, err := cfg.IMAPPassword()
	if err != nil {
		c.Close()
		return nil, fmt.Errorf("read password: %w", err)
	}

	if err := c.Login(cfg.IMAPUser, password).Wait(); err != nil {
		c.Close()
		return nil, fmt.Errorf("login %s: %w", cfg.IMAPUser, err)
	}

	logger.Debug("imap connected", "host", cfg.IMAPHost, "user", cfg.IMAPUser)
	return &Client{imap: c, cfg: cfg, logger: logger}, nil
}

// Close logs out and closes the connection.
func (c *Client) Close() error {
	return c.imap.Logout().Wait()
}

// Status returns the number of unseen messages in a folder using the
// IMAP STATUS command. This is lighter than ListMessages because it
// does not download envelopes or bodies.
func (c *Client) Status(folder string) (uint32, error) {
	data, err := c.imap.Status(folder, &imap.StatusOptions{NumUnseen: true}).Wait()
	if err != nil {
		return 0, fmt.Errorf("status %q: %w", folder, err)
	}
	if data.NumUnseen == nil {
		return 0, nil
	}
	return *data.NumUnseen, nil
}

// UnreadCount returns the number of unseen messages in folder. When repoSlug is
// non-empty it counts only messages tagged for that repo (the X-Beadle-Repo
// header or a "[slug]" subject tag) via one UID SEARCH; an empty slug counts
// all unseen via the lighter STATUS command. On a repo SEARCH error it never
// hides mail: it warns and falls back to the all-repo STATUS count.
func (c *Client) UnreadCount(folder, repoSlug string) (uint32, error) {
	if repoSlug == "" {
		return c.Status(folder)
	}
	if _, err := c.imap.Select(folder, &imap.SelectOptions{ReadOnly: true}).Wait(); err != nil {
		return 0, fmt.Errorf("select %q: %w", folder, err)
	}
	searchData, err := c.imap.UIDSearch(repoSearchCriteria(repoSlug, true), nil).Wait()
	if err != nil {
		c.logger.Warn("repo unread search failed; counting all repos", "err", err)
		return c.Status(folder)
	}
	return uint32(len(searchData.AllUIDs())), nil
}

// ListFolders returns all available mailbox folders.
func (c *Client) ListFolders() ([]channel.Folder, error) {
	listCmd := c.imap.List("", "*", nil)
	mailboxes, err := listCmd.Collect()
	if err != nil {
		return nil, fmt.Errorf("list folders: %w", err)
	}

	folders := make([]channel.Folder, 0, len(mailboxes))
	for _, mb := range mailboxes {
		folders = append(folders, channel.Folder{Name: mb.Mailbox})
	}
	return folders, nil
}

// ListMessages returns recent messages from a folder.
// ListResult holds the messages returned by ListMessages along with the
// total number of messages matching the query criteria.
type ListResult struct {
	Messages []channel.MessageSummary
	Total    int // total matching messages (before count limit)
}

// SearchQuery names the criteria for a mailbox search. A zero field is unset;
// a zero SearchQuery selects the plain recency listing.
type SearchQuery struct {
	From       string    // From header substring
	Subject    string    // Subject header substring
	Since      time.Time // messages on/after this date (Date header, SENTSINCE)
	Text       string    // free text over the whole message (IMAP TEXT)
	RepoSlug   string    // "" = all repos
	UnreadOnly bool
}

func (c *Client) ListMessages(folder string, count int, unreadOnly bool, repoSlug string) (*ListResult, error) {
	return c.SearchMessages(folder, SearchQuery{RepoSlug: repoSlug, UnreadOnly: unreadOnly}, count, 0)
}

// SearchMessages returns the messages in folder matching q, windowed to count
// messages ending offset positions back from the most recent match. It is the
// engine behind ListMessages: an empty query with offset 0 takes the recency
// fast path; any criterion or a non-zero offset runs one UID SEARCH. Total in
// the result counts every match, so a caller can page. On a SEARCH error it
// falls back like the listing does — widen a repo scope, else floor to recency
// — so a transient failure never returns a misleading empty result.
func (c *Client) SearchMessages(folder string, q SearchQuery, count, offset int) (*ListResult, error) {
	if offset < 0 {
		return nil, fmt.Errorf("offset must be non-negative")
	}

	mbox, err := c.imap.Select(folder, &imap.SelectOptions{ReadOnly: true}).Wait()
	if err != nil {
		return nil, fmt.Errorf("select %q: %w", folder, err)
	}
	if mbox.NumMessages == 0 || count <= 0 {
		return &ListResult{}, nil
	}

	numSet, total, err := c.listSet(mbox.NumMessages, q, count, offset)
	if err != nil {
		return nil, err
	}
	if numSet == nil {
		// No message matched the query, or the page is past the end.
		return &ListResult{Total: total}, nil
	}

	summaries, err := c.fetchSummaries(numSet)
	if err != nil {
		return nil, err
	}
	return &ListResult{Messages: summaries, Total: total}, nil
}

// fetchSummaries fetches envelope, flags, and Proton trust headers for numSet
// and renders them into message summaries. Messages without an envelope are
// skipped.
func (c *Client) fetchSummaries(numSet imap.NumSet) ([]channel.MessageSummary, error) {
	fetchOpts := &imap.FetchOptions{
		Envelope: true,
		Flags:    true,
		UID:      true,
		BodySection: []*imap.FetchItemBodySection{
			{Specifier: imap.PartSpecifierHeader, HeaderFields: []string{
				"X-Pm-Content-Encryption", "X-Pm-Origin", "Content-Type",
			}, Peek: true},
		},
	}

	msgs, err := c.imap.Fetch(numSet, fetchOpts).Collect()
	if err != nil {
		return nil, fmt.Errorf("fetch list: %w", err)
	}

	summaries := make([]channel.MessageSummary, 0, len(msgs))
	for _, msg := range msgs {
		if msg.Envelope == nil {
			continue
		}

		from := ""
		if len(msg.Envelope.From) > 0 {
			from = formatAddress(msg.Envelope.From[0])
		}

		headerBytes := msg.FindBodySection(&imap.FetchItemBodySection{
			Specifier:    imap.PartSpecifierHeader,
			HeaderFields: []string{"X-Pm-Content-Encryption", "X-Pm-Origin", "Content-Type"},
			Peek:         true,
		})
		trust := classifyFromHeaders(string(headerBytes))

		unread := true
		for _, f := range msg.Flags {
			if f == imap.FlagSeen {
				unread = false
				break
			}
		}

		summaries = append(summaries, channel.MessageSummary{
			ID:         strconv.FormatUint(uint64(msg.UID), 10),
			From:       from,
			Date:       msg.Envelope.Date,
			Subject:    msg.Envelope.Subject,
			TrustLevel: trust.Level,
			HasSig:     trust.HasSig,
			Unread:     unread,
		})
	}
	return summaries, nil
}

// listSet decides which messages SearchMessages fetches and reports how many
// match. With no criteria and offset 0 it takes the last count by recency — the
// fast path that issues no SEARCH. Any criterion, or a non-zero offset, runs one
// UID SEARCH and windows the result. On a SEARCH error it delegates to
// searchFallback. A nil NumSet means nothing matched or the page is past the
// end.
func (c *Client) listSet(numMessages uint32, q SearchQuery, count, offset int) (imap.NumSet, int, error) {
	crit := searchCriteria(q)
	if crit == nil && offset == 0 {
		return recencySet(numMessages, clampCount(count, numMessages)), int(numMessages), nil
	}
	if crit == nil {
		// offset > 0 with no filter: search all messages for exact windowing,
		// since the sequence-number recency set has no clean offset.
		crit = &imap.SearchCriteria{}
	}

	searchData, err := c.imap.UIDSearch(crit, nil).Wait()
	if err != nil {
		return c.searchFallback(numMessages, q, count, offset, err)
	}
	numSet, total := selectUIDs(searchData, count, offset)
	return numSet, total, nil
}

// searchFallback recovers from a SEARCH error without hiding mail. A repo-scoped
// query widens to all repos (keeping unread and content criteria), matching
// UnreadCount's never-empty policy. If that retry also fails, or the query was
// never repo-scoped, it floors to the recency window — the never-empty floor.
// The floor returns the most-recent count and does not honor offset, because a
// recency window cannot be paged exactly once SEARCH is unavailable.
func (c *Client) searchFallback(numMessages uint32, q SearchQuery, count, offset int, cause error) (imap.NumSet, int, error) {
	if q.RepoSlug != "" {
		wq := q
		wq.RepoSlug = ""
		c.logger.Warn("repo search failed; widening to all repos", "err", cause)
		if retry, err := c.imap.UIDSearch(searchCriteria(wq), nil).Wait(); err == nil {
			numSet, total := selectUIDs(retry, count, offset)
			return numSet, total, nil
		}
	}
	c.logger.Warn("search failed; listing all recent mail", "err", cause)
	return recencySet(numMessages, clampCount(count, numMessages)), int(numMessages), nil
}

// clampCount bounds count to [0, numMessages] so it is a safe recencySet span.
func clampCount(count int, numMessages uint32) int {
	if count > int(numMessages) {
		return int(numMessages)
	}
	return count
}

// selectUIDs turns a SEARCH result into a fetch set: the page of count matching
// UIDs ending offset back from the most recent. Total counts every match, even
// when the requested page is empty (offset past the end), so callers can page.
func selectUIDs(searchData *imap.SearchData, count, offset int) (imap.NumSet, int) {
	uids := searchData.AllUIDs()
	total := len(uids)
	page := window(uids, count, offset)
	if len(page) == 0 {
		return nil, total
	}
	return imap.UIDSetNum(page...), total
}

// recencySet selects the last count messages by sequence number.
// count must lie in [1, numMessages].
func recencySet(numMessages uint32, count int) imap.SeqSet {
	start := uint32(1)
	if numMessages > uint32(count) {
		start = numMessages - uint32(count) + 1
	}
	return imap.SeqSet{{Start: start, Stop: numMessages}}
}

// window returns the page of uids of length count that ends offset positions
// back from the most recent (last) element. offset 0 is the newest page — the
// last count elements — so window(uids, count, 0) equals the old lastN. A count
// or window that runs off the front is clamped; an offset at or past the end
// yields an empty slice. count and offset are assumed non-negative.
func window(uids []imap.UID, count, offset int) []imap.UID {
	end := len(uids) - offset
	if end <= 0 || count <= 0 {
		return nil
	}
	start := end - count
	if start < 0 {
		start = 0
	}
	return uids[start:end]
}

// searchCriteria builds the IMAP SEARCH criteria for q. It returns nil when q is
// entirely empty, signalling the plain recency path. A non-empty RepoSlug adds
// one OR of two arms — the X-Beadle-Repo header and the "[slug]" subject tag —
// so agent- and GitHub-tagged mail matches via the header while a human reply
// that kept "Re: [slug] ..." matches via the subject. UnreadOnly adds a
// top-level NotFlag:[FlagSeen]; From/Subject add Header substrings; Since adds
// SENTSINCE; Text adds a whole-message TEXT search. IMAP ANDs top-level keys, so
// these compose in one command.
//
// The repo header arm is a substring match — IMAP offers no exact-header search
// — so a slug that is a prefix of another repo's slug would also match that
// repo's mail. No such colliding repo exists, and the subject arm is
// bracket-anchored, so this is accepted rather than post-filtered client-side.
func searchCriteria(q SearchQuery) *imap.SearchCriteria {
	if q.RepoSlug == "" && !q.UnreadOnly && q.From == "" && q.Subject == "" &&
		q.Since.IsZero() && q.Text == "" {
		return nil
	}
	crit := &imap.SearchCriteria{}
	if q.UnreadOnly {
		crit.NotFlag = []imap.Flag{imap.FlagSeen}
	}
	if q.RepoSlug != "" {
		crit.Or = [][2]imap.SearchCriteria{{
			{Header: []imap.SearchCriteriaHeaderField{{Key: HeaderRepo, Value: q.RepoSlug}}},
			{Header: []imap.SearchCriteriaHeaderField{{Key: "Subject", Value: "[" + q.RepoSlug + "]"}}},
		}}
	}
	if q.From != "" {
		crit.Header = append(crit.Header, imap.SearchCriteriaHeaderField{Key: "From", Value: q.From})
	}
	if q.Subject != "" {
		crit.Header = append(crit.Header, imap.SearchCriteriaHeaderField{Key: "Subject", Value: q.Subject})
	}
	if !q.Since.IsZero() {
		crit.SentSince = q.Since
	}
	if q.Text != "" {
		crit.Text = []string{q.Text}
	}
	return crit
}

// repoSearchCriteria builds the scope-only criteria the poller and listing use:
// a repo slug, optionally intersected with "unread". It is the two-field caller
// of searchCriteria, so the recency-path and scope behavior are unchanged.
func repoSearchCriteria(slug string, unreadOnly bool) *imap.SearchCriteria {
	return searchCriteria(SearchQuery{RepoSlug: slug, UnreadOnly: unreadOnly})
}

// ParseSearchSince parses a search "since" date. It accepts a full RFC3339
// timestamp or a bare YYYY-MM-DD date; the latter is midnight UTC on that day.
func ParseSearchSince(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("invalid date %q: want RFC3339 or YYYY-MM-DD", s)
}

// FetchMessage retrieves a full message by UID from the given folder.
func (c *Client) FetchMessage(folder string, uid uint32) (*channel.Message, error) {
	_, err := c.imap.Select(folder, &imap.SelectOptions{ReadOnly: true}).Wait()
	if err != nil {
		return nil, fmt.Errorf("select %q: %w", folder, err)
	}

	fetchOpts := &imap.FetchOptions{
		Envelope: true,
		Flags:    true,
		BodySection: []*imap.FetchItemBodySection{
			{Peek: true}, // Entire message (RFC822)
		},
	}

	fetchCmd := c.imap.Fetch(imap.UIDSetNum(imap.UID(uid)), fetchOpts)
	msgs, err := fetchCmd.Collect()
	if err != nil {
		return nil, fmt.Errorf("fetch uid %d: %w", uid, err)
	}
	if len(msgs) == 0 {
		return nil, fmt.Errorf("message uid %d not found", uid)
	}

	buf := msgs[0]
	raw := buf.FindBodySection(&imap.FetchItemBodySection{Peek: true})
	if raw == nil {
		return nil, fmt.Errorf("message uid %d: empty body", uid)
	}

	return c.parseMessage(buf, raw)
}

// FetchRaw retrieves the raw RFC822 bytes for a message.
func (c *Client) FetchRaw(folder string, uid uint32) ([]byte, error) {
	_, err := c.imap.Select(folder, &imap.SelectOptions{ReadOnly: true}).Wait()
	if err != nil {
		return nil, fmt.Errorf("select %q: %w", folder, err)
	}

	fetchOpts := &imap.FetchOptions{
		BodySection: []*imap.FetchItemBodySection{
			{Peek: true},
		},
	}

	fetchCmd := c.imap.Fetch(imap.UIDSetNum(imap.UID(uid)), fetchOpts)
	msgs, err := fetchCmd.Collect()
	if err != nil {
		return nil, fmt.Errorf("fetch raw uid %d: %w", uid, err)
	}
	if len(msgs) == 0 {
		return nil, fmt.Errorf("message uid %d not found", uid)
	}

	raw := msgs[0].FindBodySection(&imap.FetchItemBodySection{Peek: true})
	if raw == nil {
		return nil, fmt.Errorf("message uid %d: empty body", uid)
	}
	return raw, nil
}

func (c *Client) parseMessage(buf *imapclient.FetchMessageBuffer, raw []byte) (*channel.Message, error) {
	env := buf.Envelope

	from := ""
	if len(env.From) > 0 {
		from = formatAddress(env.From[0])
	}
	to := ""
	if len(env.To) > 0 {
		to = formatAddress(env.To[0])
	}

	// Decrypt PGP/MIME encrypted messages before parsing MIME content.
	// The decrypted plaintext may itself be a full MIME message
	// (multipart/signed, multipart/mixed, etc.).
	if pgp.IsEncrypted(raw) && c.cfg.GPGSigner != "" {
		passphrase, _ := c.cfg.GPGPassphrase()
		if result, err := pgp.Decrypt(c.cfg.GPGBinary, passphrase, raw); err == nil {
			raw = result.Plaintext
		}
	}

	body, attachments, headers := ParseMIME(raw)
	trust := ClassifyTrust(headers, raw)

	encryption := "tls"
	if enc, ok := headers["X-Pm-Content-Encryption"]; ok && strings.Contains(strings.ToLower(enc), "end-to-end") {
		encryption = "end-to-end"
	}

	return &channel.Message{
		ID:          strconv.FormatUint(uint64(buf.UID), 10),
		From:        from,
		To:          to,
		Date:        env.Date,
		Subject:     env.Subject,
		Body:        body,
		TrustLevel:  trust,
		Channel:     "email",
		Encryption:  encryption,
		Attachments: attachments,
		RawHeaders:  headers,
	}, nil
}

// MoveMessage moves a message by UID from one folder to another.
// The go-imap/v2 Move command handles the MOVE extension (RFC 6851) with
// automatic fallback to COPY+STORE+EXPUNGE for older servers.
func (c *Client) MoveMessage(srcFolder string, uid uint32, dstFolder string) error {
	_, err := c.imap.Select(srcFolder, &imap.SelectOptions{ReadOnly: false}).Wait()
	if err != nil {
		return fmt.Errorf("select %q: %w", srcFolder, err)
	}

	_, err = c.imap.Move(imap.UIDSetNum(imap.UID(uid)), dstFolder).Wait()
	if err != nil {
		return fmt.Errorf("move uid %d to %q: %w", uid, dstFolder, err)
	}
	return nil
}

// MoveMessages moves multiple messages by UID from one folder to another.
// Issues a single SELECT followed by a single MOVE command. UIDs that
// don't exist on the server are silently ignored by the IMAP protocol.
func (c *Client) MoveMessages(srcFolder string, uids []uint32, dstFolder string) error {
	if len(uids) == 0 {
		return nil
	}

	_, err := c.imap.Select(srcFolder, &imap.SelectOptions{ReadOnly: false}).Wait()
	if err != nil {
		return fmt.Errorf("select %q: %w", srcFolder, err)
	}

	imapUIDs := make([]imap.UID, len(uids))
	for i, u := range uids {
		imapUIDs[i] = imap.UID(u)
	}

	_, err = c.imap.Move(imap.UIDSetNum(imapUIDs...), dstFolder).Wait()
	if err != nil {
		return fmt.Errorf("move %d messages to %q: %w", len(uids), dstFolder, err)
	}
	return nil
}

// SetSeen marks a message read or unread by UID, adding or removing the \Seen
// flag. It mirrors MoveMessage: select read-write, then STORE. seen true adds
// \Seen (read); seen false removes it (unread). A UID absent on the server is
// ignored by the protocol, as with Move.
func (c *Client) SetSeen(folder string, uid uint32, seen bool) error {
	return c.SetSeenBatch(folder, []uint32{uid}, seen)
}

// SetSeenBatch marks multiple messages read or unread by UID in one STORE.
// A single SELECT precedes the STORE. UIDs absent on the server are silently
// ignored by the IMAP protocol.
func (c *Client) SetSeenBatch(folder string, uids []uint32, seen bool) error {
	if len(uids) == 0 {
		return nil
	}

	if _, err := c.imap.Select(folder, &imap.SelectOptions{ReadOnly: false}).Wait(); err != nil {
		return fmt.Errorf("select %q: %w", folder, err)
	}

	imapUIDs := make([]imap.UID, len(uids))
	for i, u := range uids {
		imapUIDs[i] = imap.UID(u)
	}

	op := imap.StoreFlagsDel
	if seen {
		op = imap.StoreFlagsAdd
	}
	store := &imap.StoreFlags{
		Op:     op,
		Silent: true,
		Flags:  []imap.Flag{imap.FlagSeen},
	}
	if err := c.imap.Store(imap.UIDSetNum(imapUIDs...), store, nil).Close(); err != nil {
		return fmt.Errorf("store \\Seen on %d messages: %w", len(uids), err)
	}
	return nil
}

func formatAddress(addr imap.Address) string {
	if addr.Name != "" {
		return fmt.Sprintf("%s <%s@%s>", addr.Name, addr.Mailbox, addr.Host)
	}
	return fmt.Sprintf("%s@%s", addr.Mailbox, addr.Host)
}

// HeaderTrust is a preliminary trust classification from headers alone.
type HeaderTrust struct {
	Level  channel.TrustLevel
	HasSig bool
}

// classifyFromHeaders does a quick trust classification from headers only,
// without parsing the full MIME body. Used for list summaries.
func classifyFromHeaders(headerBlock string) HeaderTrust {
	lower := strings.ToLower(headerBlock)
	if strings.Contains(lower, "x-pm-content-encryption: end-to-end") &&
		strings.Contains(lower, "x-pm-origin: internal") {
		return HeaderTrust{Level: channel.Trusted}
	}
	if strings.Contains(lower, "multipart/signed") {
		return HeaderTrust{Level: channel.Unverified, HasSig: true}
	}
	return HeaderTrust{Level: channel.Unverified}
}

// isLoopback returns true if host is a loopback address.
func isLoopback(host string) bool {
	ip := net.ParseIP(host)
	if ip != nil {
		return ip.IsLoopback()
	}
	return host == "localhost"
}

// Ensure Client satisfies io.Closer.
var _ io.Closer = (*Client)(nil)
