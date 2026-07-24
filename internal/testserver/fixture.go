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

// serveUntilCleanup runs serve(ln) in a goroutine and registers a cleanup
// that closes the listener, waits for serve to return, then calls closeFn.
//
// Closing the listener first makes serve's Accept return, so the accept
// goroutine finishes instead of leaking into the next test. It also removes
// a race specific to go-imap: its Serve registers the listener in a
// sync.WaitGroup with Add(1) after releasing the accept-loop mutex, and its
// Close calls Wait; when cleanup's Close ran before the just-launched Serve
// reached Add(1), Wait raced Add. go-smtp's Close does not Wait (only
// Shutdown does) and its WaitGroup counts connections, not the accept loop,
// so for go-smtp closing the listener alone cures the leak. Either way,
// waiting for serve to return before closeFn establishes the happens-before
// that settles the WaitGroup with no concurrent Add.
func serveUntilCleanup(t testing.TB, ln net.Listener, serve func(net.Listener) error, closeFn func() error) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = serve(ln)
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		<-done
		_ = closeFn()
	})
}

// AddMessage seeds a message into the IMAP server. Returns the UID.
func (f *Fixture) AddMessage(folder, from, subject, body string) uint32 {
	return f.IMAP.AddMessage(folder, from, subject, body)
}

// AddMessageWithFlags seeds a message with specific IMAP flags.
func (f *Fixture) AddMessageWithFlags(folder, from, subject, body string, flags []imap.Flag) uint32 {
	return f.IMAP.AddMessageWithFlags(folder, from, subject, body, flags)
}

// AddRawMessage seeds a message with raw RFC822 bytes.
func (f *Fixture) AddRawMessage(folder string, raw []byte) uint32 {
	return f.IMAP.AddRawMessage(folder, raw)
}

// SentMessages returns all messages captured by the SMTP server.
func (f *Fixture) SentMessages() []SentMessage {
	return f.SMTP.SentMessages()
}
