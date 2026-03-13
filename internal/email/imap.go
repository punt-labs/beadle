package email

import (
	"crypto/tls"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strconv"
	"strings"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"

	"github.com/punt-labs/beadle/internal/channel"
)

// Client wraps an IMAP connection to Proton Bridge.
type Client struct {
	imap   *imapclient.Client
	cfg    *Config
	logger *slog.Logger
}

// Dial connects to the IMAP server with STARTTLS and logs in.
func Dial(cfg *Config, logger *slog.Logger) (*Client, error) {
	addr := net.JoinHostPort(cfg.IMAPHost, strconv.Itoa(cfg.IMAPPort))

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", addr, err)
	}

	c, err := imapclient.NewStartTLS(conn, &imapclient.Options{
		TLSConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // Proton Bridge uses self-signed certs on localhost
	})
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("starttls %s: %w", addr, err)
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
func (c *Client) ListMessages(folder string, count int, unreadOnly bool) ([]channel.MessageSummary, error) {
	mbox, err := c.imap.Select(folder, &imap.SelectOptions{ReadOnly: true}).Wait()
	if err != nil {
		return nil, fmt.Errorf("select %q: %w", folder, err)
	}

	if mbox.NumMessages == 0 {
		return nil, nil
	}

	// Determine which messages to fetch
	var numSet imap.NumSet
	if unreadOnly {
		searchData, err := c.imap.Search(&imap.SearchCriteria{
			NotFlag: []imap.Flag{imap.FlagSeen},
		}, nil).Wait()
		if err != nil {
			return nil, fmt.Errorf("search unseen: %w", err)
		}
		uids := searchData.AllUIDs()
		if len(uids) == 0 {
			return nil, nil
		}
		// Take the last `count` UIDs
		if len(uids) > count {
			uids = uids[len(uids)-count:]
		}
		numSet = imap.UIDSetNum(uids...)
	} else {
		// Fetch the last `count` messages by sequence number
		start := uint32(1)
		if mbox.NumMessages > uint32(count) {
			start = mbox.NumMessages - uint32(count) + 1
		}
		numSet = imap.SeqSet{{Start: start, Stop: mbox.NumMessages}}
	}

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

	fetchCmd := c.imap.Fetch(numSet, fetchOpts)
	msgs, err := fetchCmd.Collect()
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

		// Parse Proton headers for quick trust classification
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

// Ensure Client satisfies io.Closer.
var _ io.Closer = (*Client)(nil)
