package testserver

import (
	"net"
	"strconv"
	"testing"

	"github.com/emersion/go-imap/v2"
	"github.com/punt-labs/beadle/internal/email"
)

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
// with Config pre-configured to connect to them. Sets BEADLE_IMAP_PASSWORD
// in the test environment so credential resolution works.
func NewFixture(t testing.TB) *Fixture {
	t.Helper()

	imapSrv, imapAddr := NewIMAPServer(t, testUser, testPass)
	smtpSrv, smtpAddr := NewSMTPServer(t)

	imapHost, imapPortStr, _ := net.SplitHostPort(imapAddr)
	imapPort, _ := strconv.Atoi(imapPortStr)
	_, smtpPortStr, _ := net.SplitHostPort(smtpAddr)
	smtpPort, _ := strconv.Atoi(smtpPortStr)

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
