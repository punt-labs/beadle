// Package testserver provides in-process IMAP and SMTP servers for
// integration testing. Not for production use.
package testserver

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
)

// IMAPServer is an in-process IMAP server backed by memory.
type IMAPServer struct {
	server   *imapserver.Server
	listener net.Listener
	backend  *memBackend
}

// NewIMAPServer starts an in-process IMAP server on an ephemeral port.
// The server supports STARTTLS with a self-signed certificate.
// It is stopped automatically when the test completes.
func NewIMAPServer(t testing.TB, user, pass string) (*IMAPServer, string) {
	t.Helper()

	backend := &memBackend{
		user:      user,
		pass:      pass,
		mailboxes: map[string]*memMailbox{},
	}
	// Seed INBOX by default.
	backend.mailboxes["INBOX"] = &memMailbox{name: "INBOX", uidNext: 1}

	tlsCert := selfSignedCert(t)

	srv := imapserver.New(&imapserver.Options{
		NewSession: func(conn *imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return &memSession{backend: backend}, nil, nil
		},
		Caps: imap.CapSet{
			imap.CapIMAP4rev1: {},
			imap.CapUIDPlus:   {},
			imap.CapMove:      {},
		},
		TLSConfig:    &tls.Config{Certificates: []tls.Certificate{tlsCert}},
		InsecureAuth: false,
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("testserver: listen: %v", err)
	}

	go srv.Serve(ln) //nolint:errcheck

	t.Cleanup(func() {
		srv.Close()
	})

	addr := ln.Addr().String()
	is := &IMAPServer{server: srv, listener: ln, backend: backend}
	return is, addr
}

// AddMessage seeds a message into the specified folder.
// Creates the folder if it doesn't exist. Returns the assigned UID.
func (s *IMAPServer) AddMessage(folder, from, subject, body string) uint32 {
	s.backend.mu.Lock()
	defer s.backend.mu.Unlock()

	mb, ok := s.backend.mailboxes[folder]
	if !ok {
		mb = &memMailbox{name: folder, uidNext: 1}
		s.backend.mailboxes[folder] = mb
	}

	raw := buildRFC822(from, subject, body)
	uid := mb.uidNext
	mb.messages = append(mb.messages, &memMessage{
		uid:   imap.UID(uid),
		flags: []imap.Flag{},
		raw:   raw,
		date:  time.Now(),
	})
	mb.uidNext++
	return uid
}

// AddMessageWithFlags seeds a message with specific flags.
func (s *IMAPServer) AddMessageWithFlags(folder, from, subject, body string, flags []imap.Flag) uint32 {
	s.backend.mu.Lock()
	defer s.backend.mu.Unlock()

	mb, ok := s.backend.mailboxes[folder]
	if !ok {
		mb = &memMailbox{name: folder, uidNext: 1}
		s.backend.mailboxes[folder] = mb
	}

	raw := buildRFC822(from, subject, body)
	uid := mb.uidNext
	mb.messages = append(mb.messages, &memMessage{
		uid:   imap.UID(uid),
		flags: flags,
		raw:   raw,
		date:  time.Now(),
	})
	mb.uidNext++
	return uid
}

// AddRawMessage seeds a message with raw RFC822 bytes.
// Use this when you need custom headers (e.g., Proton trust headers).
func (s *IMAPServer) AddRawMessage(folder string, raw []byte) uint32 {
	s.backend.mu.Lock()
	defer s.backend.mu.Unlock()

	mb, ok := s.backend.mailboxes[folder]
	if !ok {
		mb = &memMailbox{name: folder, uidNext: 1}
		s.backend.mailboxes[folder] = mb
	}

	uid := mb.uidNext
	mb.messages = append(mb.messages, &memMessage{
		uid:   imap.UID(uid),
		flags: []imap.Flag{},
		raw:   raw,
		date:  time.Now(),
	})
	mb.uidNext++
	return uid
}

func buildRFC822(from, subject, body string) []byte {
	return []byte(fmt.Sprintf(
		"From: %s\r\nSubject: %s\r\nDate: %s\r\nMessage-ID: <%d@test>\r\nContent-Type: text/plain\r\n\r\n%s",
		from, subject, time.Now().Format(time.RFC1123Z), time.Now().UnixNano(), body,
	))
}

// --- Memory backend ---

type memBackend struct {
	mu        sync.Mutex
	user      string
	pass      string
	mailboxes map[string]*memMailbox
}

type memMailbox struct {
	name     string
	messages []*memMessage
	uidNext  uint32
}

type memMessage struct {
	uid   imap.UID
	flags []imap.Flag
	raw   []byte
	date  time.Time
}

func (m *memMessage) hasFlag(flag imap.Flag) bool {
	for _, f := range m.flags {
		if f == flag {
			return true
		}
	}
	return false
}

// --- IMAP Session ---

type memSession struct {
	backend  *memBackend
	selected *memMailbox
}

func (s *memSession) Close() error { return nil }

func (s *memSession) Login(username, password string) error {
	if username != s.backend.user || password != s.backend.pass {
		return &imap.Error{
			Type: imap.StatusResponseTypeNo,
			Code: imap.ResponseCodeAuthenticationFailed,
			Text: "invalid credentials",
		}
	}
	return nil
}

func (s *memSession) Select(mailbox string, options *imap.SelectOptions) (*imap.SelectData, error) {
	s.backend.mu.Lock()
	defer s.backend.mu.Unlock()

	mb, ok := s.backend.mailboxes[mailbox]
	if !ok {
		return nil, fmt.Errorf("no such mailbox: %s", mailbox)
	}
	s.selected = mb

	numMessages := uint32(len(mb.messages))

	return &imap.SelectData{
		NumMessages: numMessages,
		UIDNext:     imap.UID(mb.uidNext),
		UIDValidity: 1,
		Flags:       []imap.Flag{imap.FlagSeen, imap.FlagAnswered, imap.FlagFlagged, imap.FlagDeleted, imap.FlagDraft},
	}, nil
}

func (s *memSession) Create(mailbox string, _ *imap.CreateOptions) error {
	s.backend.mu.Lock()
	defer s.backend.mu.Unlock()
	if _, ok := s.backend.mailboxes[mailbox]; ok {
		return fmt.Errorf("mailbox already exists: %s", mailbox)
	}
	s.backend.mailboxes[mailbox] = &memMailbox{name: mailbox, uidNext: 1}
	return nil
}

func (s *memSession) Delete(mailbox string) error {
	s.backend.mu.Lock()
	defer s.backend.mu.Unlock()
	delete(s.backend.mailboxes, mailbox)
	return nil
}

func (s *memSession) Rename(_, _ string, _ *imap.RenameOptions) error {
	return fmt.Errorf("RENAME not implemented")
}

func (s *memSession) Subscribe(_ string) error   { return nil }
func (s *memSession) Unsubscribe(_ string) error { return nil }

func (s *memSession) List(w *imapserver.ListWriter, ref string, patterns []string, options *imap.ListOptions) error {
	s.backend.mu.Lock()
	names := make([]string, 0, len(s.backend.mailboxes))
	for name := range s.backend.mailboxes {
		names = append(names, name)
	}
	s.backend.mu.Unlock()

	sort.Strings(names)

	for _, name := range names {
		matched := false
		for _, pattern := range patterns {
			if matchMailbox(ref, pattern, name) {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		if err := w.WriteList(&imap.ListData{
			Mailbox: name,
			Delim:   '/',
		}); err != nil {
			return err
		}
	}
	return nil
}

func matchMailbox(ref, pattern, name string) bool {
	full := ref + pattern
	if full == "*" || full == "%" {
		return true
	}
	// Simple prefix match for ref + wildcard
	if strings.HasSuffix(full, "*") {
		return strings.HasPrefix(name, strings.TrimSuffix(full, "*"))
	}
	return name == full
}

func (s *memSession) Status(mailbox string, options *imap.StatusOptions) (*imap.StatusData, error) {
	s.backend.mu.Lock()
	defer s.backend.mu.Unlock()

	mb, ok := s.backend.mailboxes[mailbox]
	if !ok {
		return nil, fmt.Errorf("no such mailbox: %s", mailbox)
	}

	var numMessages uint32
	var numUnseen uint32
	for _, msg := range mb.messages {
		numMessages++
		if !msg.hasFlag(imap.FlagSeen) {
			numUnseen++
		}
	}

	return &imap.StatusData{
		Mailbox:     mb.name,
		NumMessages: &numMessages,
		NumUnseen:   &numUnseen,
		UIDNext:     imap.UID(mb.uidNext),
		UIDValidity: 1,
	}, nil
}

func (s *memSession) Append(mailbox string, r imap.LiteralReader, options *imap.AppendOptions) (*imap.AppendData, error) {
	s.backend.mu.Lock()
	defer s.backend.mu.Unlock()

	mb, ok := s.backend.mailboxes[mailbox]
	if !ok {
		return nil, fmt.Errorf("no such mailbox: %s", mailbox)
	}

	raw := make([]byte, r.Size())
	n, err := io.ReadFull(r, raw)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, err
	}
	raw = raw[:n]

	uid := mb.uidNext
	var flags []imap.Flag
	if options != nil {
		flags = options.Flags
	}
	mb.messages = append(mb.messages, &memMessage{
		uid:   imap.UID(uid),
		flags: flags,
		raw:   raw,
		date:  time.Now(),
	})
	mb.uidNext++

	return &imap.AppendData{
		UID:         imap.UID(uid),
		UIDValidity: 1,
	}, nil
}

func (s *memSession) Poll(_ *imapserver.UpdateWriter, _ bool) error { return nil }

func (s *memSession) Idle(_ *imapserver.UpdateWriter, stop <-chan struct{}) error {
	<-stop
	return nil
}

func (s *memSession) Unselect() error {
	s.selected = nil
	return nil
}

func (s *memSession) Expunge(w *imapserver.ExpungeWriter, uids *imap.UIDSet) error {
	if s.selected == nil {
		return fmt.Errorf("no mailbox selected")
	}
	s.backend.mu.Lock()
	defer s.backend.mu.Unlock()

	var remaining []*memMessage
	var seqNum uint32
	for _, msg := range s.selected.messages {
		seqNum++
		if msg.hasFlag(imap.FlagDeleted) {
			if uids == nil || uids.Contains(msg.uid) {
				if err := w.WriteExpunge(seqNum); err != nil {
					return err
				}
				continue
			}
		}
		remaining = append(remaining, msg)
	}
	s.selected.messages = remaining
	return nil
}

func (s *memSession) Search(kind imapserver.NumKind, criteria *imap.SearchCriteria, options *imap.SearchOptions) (*imap.SearchData, error) {
	if s.selected == nil {
		return nil, fmt.Errorf("no mailbox selected")
	}
	s.backend.mu.Lock()
	defer s.backend.mu.Unlock()

	var uids []imap.UID
	for _, msg := range s.selected.messages {
		if matchesCriteria(msg, criteria) {
			uids = append(uids, msg.uid)
		}
	}

	var uidSet imap.UIDSet
	for _, uid := range uids {
		uidSet.AddNum(uid)
	}

	return &imap.SearchData{
		All:   uidSet,
		Count: uint32(len(uids)),
	}, nil
}

func matchesCriteria(msg *memMessage, criteria *imap.SearchCriteria) bool {
	if criteria == nil {
		return true
	}
	for _, flag := range criteria.Flag {
		if !msg.hasFlag(flag) {
			return false
		}
	}
	for _, flag := range criteria.NotFlag {
		if msg.hasFlag(flag) {
			return false
		}
	}
	return true
}

func (s *memSession) Fetch(w *imapserver.FetchWriter, numSet imap.NumSet, options *imap.FetchOptions) error {
	if s.selected == nil {
		return fmt.Errorf("no mailbox selected")
	}
	s.backend.mu.Lock()
	msgs := make([]*memMessage, len(s.selected.messages))
	copy(msgs, s.selected.messages)
	s.backend.mu.Unlock()

	for seqIdx, msg := range msgs {
		seqNum := uint32(seqIdx + 1)
		if !numSetContains(numSet, seqNum, msg.uid) {
			continue
		}

		resp := w.CreateMessage(seqNum)

		if err := writeFetchResponse(resp, msg, options); err != nil {
			return err
		}

		if err := resp.Close(); err != nil {
			return err
		}
	}
	return nil
}

func numSetContains(numSet imap.NumSet, seqNum uint32, uid imap.UID) bool {
	switch s := numSet.(type) {
	case imap.SeqSet:
		return s.Contains(seqNum)
	case imap.UIDSet:
		return s.Contains(uid)
	default:
		return false
	}
}

func writeFetchResponse(resp *imapserver.FetchResponseWriter, msg *memMessage, options *imap.FetchOptions) error {
	if options.UID {
		resp.WriteUID(msg.uid)
	}
	if options.Flags {
		resp.WriteFlags(msg.flags)
	}
	if options.Envelope {
		resp.WriteEnvelope(parseEnvelope(msg.raw))
	}
	if options.InternalDate {
		resp.WriteInternalDate(msg.date)
	}
	if options.RFC822Size {
		resp.WriteRFC822Size(int64(len(msg.raw)))
	}
	for _, bs := range options.BodySection {
		bw := resp.WriteBodySection(bs, int64(len(msg.raw)))
		if _, err := bw.Write(msg.raw); err != nil {
			return err
		}
		if err := bw.Close(); err != nil {
			return err
		}
	}
	for _, bp := range options.BinarySection {
		bw := resp.WriteBinarySection(bp, int64(len(msg.raw)))
		if _, err := bw.Write(msg.raw); err != nil {
			return err
		}
		if err := bw.Close(); err != nil {
			return err
		}
	}
	return nil
}

func parseEnvelope(raw []byte) *imap.Envelope {
	lines := strings.Split(string(raw), "\r\n")
	env := &imap.Envelope{Date: time.Now()}
	for _, line := range lines {
		if line == "" {
			break
		}
		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "subject: ") {
			env.Subject = strings.TrimPrefix(line, "Subject: ")
		} else if strings.HasPrefix(lower, "from: ") {
			addr := strings.TrimPrefix(line, "From: ")
			env.From = []imap.Address{parseAddress(addr)}
		} else if strings.HasPrefix(lower, "to: ") {
			addr := strings.TrimPrefix(line, "To: ")
			env.To = []imap.Address{parseAddress(addr)}
		} else if strings.HasPrefix(lower, "message-id: ") {
			env.MessageID = strings.TrimPrefix(line, "Message-ID: ")
		}
	}
	return env
}

func parseAddress(s string) imap.Address {
	// Handle "Name <email>" format
	if idx := strings.Index(s, "<"); idx >= 0 {
		name := strings.TrimSpace(s[:idx])
		email := strings.Trim(s[idx:], "<>")
		parts := strings.SplitN(email, "@", 2)
		addr := imap.Address{Name: name}
		if len(parts) == 2 {
			addr.Mailbox = parts[0]
			addr.Host = parts[1]
		}
		return addr
	}
	parts := strings.SplitN(s, "@", 2)
	addr := imap.Address{}
	if len(parts) == 2 {
		addr.Mailbox = parts[0]
		addr.Host = parts[1]
	}
	return addr
}

func (s *memSession) Store(w *imapserver.FetchWriter, numSet imap.NumSet, flags *imap.StoreFlags, options *imap.StoreOptions) error {
	if s.selected == nil {
		return fmt.Errorf("no mailbox selected")
	}
	s.backend.mu.Lock()
	defer s.backend.mu.Unlock()

	for seqIdx, msg := range s.selected.messages {
		seqNum := uint32(seqIdx + 1)
		if !numSetContains(numSet, seqNum, msg.uid) {
			continue
		}
		switch flags.Op {
		case imap.StoreFlagsAdd:
			for _, f := range flags.Flags {
				if !msg.hasFlag(f) {
					msg.flags = append(msg.flags, f)
				}
			}
		case imap.StoreFlagsDel:
			var kept []imap.Flag
			for _, existing := range msg.flags {
				remove := false
				for _, f := range flags.Flags {
					if existing == f {
						remove = true
						break
					}
				}
				if !remove {
					kept = append(kept, existing)
				}
			}
			msg.flags = kept
		case imap.StoreFlagsSet:
			msg.flags = flags.Flags
		}

		if !flags.Silent {
			resp := w.CreateMessage(seqNum)
			resp.WriteFlags(msg.flags)
			if err := resp.Close(); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *memSession) Copy(numSet imap.NumSet, dest string) (*imap.CopyData, error) {
	if s.selected == nil {
		return nil, fmt.Errorf("no mailbox selected")
	}
	s.backend.mu.Lock()
	defer s.backend.mu.Unlock()

	destMb, ok := s.backend.mailboxes[dest]
	if !ok {
		return nil, fmt.Errorf("no such mailbox: %s", dest)
	}

	var srcUIDs, destUIDs imap.UIDSet
	for seqIdx, msg := range s.selected.messages {
		seqNum := uint32(seqIdx + 1)
		if !numSetContains(numSet, seqNum, msg.uid) {
			continue
		}
		srcUIDs.AddNum(msg.uid)

		newUID := imap.UID(destMb.uidNext)
		destMb.uidNext++
		raw := make([]byte, len(msg.raw))
		copy(raw, msg.raw)
		destMb.messages = append(destMb.messages, &memMessage{
			uid:   newUID,
			flags: append([]imap.Flag{}, msg.flags...),
			raw:   raw,
			date:  msg.date,
		})
		destUIDs.AddNum(newUID)
	}

	return &imap.CopyData{
		UIDValidity: 1,
		SourceUIDs:  srcUIDs,
		DestUIDs:    destUIDs,
	}, nil
}

// Move implements imapserver.SessionMove.
func (s *memSession) Move(w *imapserver.MoveWriter, numSet imap.NumSet, dest string) error {
	if s.selected == nil {
		return fmt.Errorf("no mailbox selected")
	}
	s.backend.mu.Lock()
	defer s.backend.mu.Unlock()

	destMb, ok := s.backend.mailboxes[dest]
	if !ok {
		// Auto-create destination.
		destMb = &memMailbox{name: dest, uidNext: 1}
		s.backend.mailboxes[dest] = destMb
	}

	var remaining []*memMessage
	var expungeSeq uint32
	for seqIdx, msg := range s.selected.messages {
		seqNum := uint32(seqIdx + 1)
		if !numSetContains(numSet, seqNum, msg.uid) {
			remaining = append(remaining, msg)
			continue
		}

		newUID := imap.UID(destMb.uidNext)
		destMb.uidNext++
		destMb.messages = append(destMb.messages, &memMessage{
			uid:   newUID,
			flags: append([]imap.Flag{}, msg.flags...),
			raw:   append([]byte(nil), msg.raw...),
			date:  msg.date,
		})

		expungeSeq++
		w.WriteCopyData(&imap.CopyData{
			UIDValidity: 1,
			SourceUIDs:  imap.UIDSetNum(msg.uid),
			DestUIDs:    imap.UIDSetNum(newUID),
		})
		w.WriteExpunge(seqNum)
	}
	s.selected.messages = remaining
	return nil
}

// Ensure memSession implements both Session and SessionMove.
var (
	_ imapserver.Session     = (*memSession)(nil)
	_ imapserver.SessionMove = (*memSession)(nil)
)

// --- TLS ---

func selfSignedCert(t testing.TB) tls.Certificate {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("testserver: generate key: %v", err)
	}

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(1 * time.Hour),
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("testserver: create cert: %v", err)
	}

	return tls.Certificate{
		Certificate: [][]byte{certDER},
		PrivateKey:  key,
	}
}
