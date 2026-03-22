package testserver

import (
	"io"
	"net"
	"sync"
	"testing"

	gosmtp "github.com/emersion/go-smtp"
)

// SMTPServer is an in-process SMTP server that captures sent messages.
type SMTPServer struct {
	server  *gosmtp.Server
	backend *memSMTPBackend
}

// SentMessage represents a message captured by the SMTP server.
type SentMessage struct {
	From string
	To   []string
	Raw  []byte
}

// NewSMTPServer starts an in-process SMTP server on an ephemeral port.
// It is stopped automatically when the test completes.
func NewSMTPServer(t testing.TB) (*SMTPServer, string) {
	t.Helper()

	backend := &memSMTPBackend{}
	srv := gosmtp.NewServer(backend)
	srv.AllowInsecureAuth = true

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("testserver: smtp listen: %v", err)
	}

	go srv.Serve(ln) //nolint:errcheck

	t.Cleanup(func() {
		srv.Close()
	})

	addr := ln.Addr().String()
	return &SMTPServer{server: srv, backend: backend}, addr
}

// SentMessages returns all messages captured by the server.
func (s *SMTPServer) SentMessages() []SentMessage {
	s.backend.mu.Lock()
	defer s.backend.mu.Unlock()
	out := make([]SentMessage, len(s.backend.messages))
	copy(out, s.backend.messages)
	return out
}

// Close shuts down the server.
func (s *SMTPServer) Close() error {
	return s.server.Close()
}

// --- SMTP Backend ---

type memSMTPBackend struct {
	mu       sync.Mutex
	messages []SentMessage
}

func (b *memSMTPBackend) NewSession(_ *gosmtp.Conn) (gosmtp.Session, error) {
	return &memSMTPSession{backend: b}, nil
}

type memSMTPSession struct {
	backend *memSMTPBackend
	from    string
	to      []string
}

func (s *memSMTPSession) Mail(from string, _ *gosmtp.MailOptions) error {
	s.from = from
	return nil
}

func (s *memSMTPSession) Rcpt(to string, _ *gosmtp.RcptOptions) error {
	s.to = append(s.to, to)
	return nil
}

func (s *memSMTPSession) Data(r io.Reader) error {
	raw, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	s.backend.mu.Lock()
	defer s.backend.mu.Unlock()
	s.backend.messages = append(s.backend.messages, SentMessage{
		From: s.from,
		To:   s.to,
		Raw:  raw,
	})
	return nil
}

func (s *memSMTPSession) Reset() {
	s.from = ""
	s.to = nil
}

func (s *memSMTPSession) Logout() error {
	return nil
}
