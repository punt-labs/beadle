package testserver

import (
	"log/slog"
	"net"
	"strconv"
	"testing"

	"github.com/emersion/go-imap/v2"
	"github.com/punt-labs/beadle/internal/email"
)

// TestDialer is a Dialer that injects TestPassword before dialing.
// Required because macOS Keychain is process-global — setting HOME or
// BEADLE_IMAP_PASSWORD via env doesn't prevent keychain from returning
// the real Proton Bridge password.
type TestDialer struct {
	Password string
}

// Dial connects to the IMAP server, injecting the test password.
func (d TestDialer) Dial(cfg *email.Config, logger *slog.Logger) (*email.Client, error) {
	cfgCopy := *cfg
	cfgCopy.TestPassword = d.Password
	return email.Dial(&cfgCopy, logger)
}

// Fixture provides an in-process IMAP+SMTP server pair with a
// pre-configured email.Config pointing at them.
type Fixture struct {
	Config *email.Config
	IMAP   *IMAPServer
	SMTP   *SMTPServer
}

const (
	testUser = "test@test.com"
	testPass = "testpass"
)

// NewFixture starts IMAP and SMTP servers and returns a fixture
// with Config pre-configured to connect to them. Uses Config.TestPassword
// for IMAP auth and sets BEADLE_IMAP_PASSWORD env for SMTP auth.
func NewFixture(t testing.TB) *Fixture {
	t.Helper()

	imapSrv, imapAddr := NewIMAPServer(t, testUser, testPass)
	smtpSrv, smtpAddr := NewSMTPServer(t)

	imapHost, imapPortStr, err := net.SplitHostPort(imapAddr)
	if err != nil {
		t.Fatalf("testserver: split imap addr %s: %v", imapAddr, err)
	}
	imapPort, err := strconv.Atoi(imapPortStr)
	if err != nil {
		t.Fatalf("testserver: parse imap port %s: %v", imapPortStr, err)
	}
	_, smtpPortStr, err := net.SplitHostPort(smtpAddr)
	if err != nil {
		t.Fatalf("testserver: split smtp addr %s: %v", smtpAddr, err)
	}
	smtpPort, err := strconv.Atoi(smtpPortStr)
	if err != nil {
		t.Fatalf("testserver: parse smtp port %s: %v", smtpPortStr, err)
	}

	// Set BEADLE_IMAP_PASSWORD so the secret store resolves via env var
	// (keychain and file backends may not be available in test environments).
	t.Setenv("BEADLE_IMAP_PASSWORD", testPass)

	cfg := &email.Config{
		IMAPHost:     imapHost,
		IMAPPort:     imapPort,
		IMAPUser:     testUser,
		SMTPPort:     smtpPort,
		FromAddress:  testUser,
		TestPassword: testPass,
	}

	return &Fixture{
		Config: cfg,
		IMAP:   imapSrv,
		SMTP:   smtpSrv,
	}
}

// AddMessage seeds a message into the IMAP server. Returns the UID.
func (f *Fixture) AddMessage(folder, from, subject, body string) uint32 {
	return f.IMAP.AddMessage(folder, from, subject, body)
}

// AddMessageWithFlags seeds a message with specific IMAP flags.
func (f *Fixture) AddMessageWithFlags(folder, from, subject, body string, flags []imap.Flag) uint32 {
	return f.IMAP.AddMessageWithFlags(folder, from, subject, body, flags)
}

// SentMessages returns all messages captured by the SMTP server.
func (f *Fixture) SentMessages() []SentMessage {
	return f.SMTP.SentMessages()
}
