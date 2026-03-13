package email

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"strconv"
)

// SMTPSend delivers a raw RFC 822 message through Proton Bridge's SMTP server.
//
// Proton Bridge SMTP uses STARTTLS with a self-signed certificate on localhost.
// The same credentials used for IMAP work for SMTP.
func SMTPSend(cfg *Config, from, to string, raw []byte) error {
	addr := net.JoinHostPort(cfg.IMAPHost, strconv.Itoa(cfg.SMTPPort))

	password, err := cfg.IMAPPassword()
	if err != nil {
		return fmt.Errorf("read smtp password: %w", err)
	}

	// Connect to Proton Bridge SMTP
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return fmt.Errorf("dial smtp %s: %w", addr, err)
	}

	c, err := smtp.NewClient(conn, cfg.IMAPHost)
	if err != nil {
		conn.Close()
		return fmt.Errorf("smtp client %s: %w", addr, err)
	}
	defer c.Close()

	// STARTTLS with self-signed cert (same as IMAP)
	if err := c.StartTLS(&tls.Config{
		InsecureSkipVerify: true, //nolint:gosec // Proton Bridge uses self-signed certs on localhost
	}); err != nil {
		return fmt.Errorf("smtp starttls: %w", err)
	}

	// Authenticate with the same credentials as IMAP
	auth := smtp.PlainAuth("", cfg.IMAPUser, password, cfg.IMAPHost)
	if err := c.Auth(auth); err != nil {
		return fmt.Errorf("smtp auth: %w", err)
	}

	// Send
	if err := c.Mail(from); err != nil {
		return fmt.Errorf("smtp MAIL FROM: %w", err)
	}
	if err := c.Rcpt(to); err != nil {
		return fmt.Errorf("smtp RCPT TO: %w", err)
	}

	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("smtp DATA: %w", err)
	}
	if _, err := w.Write(raw); err != nil {
		return fmt.Errorf("smtp write body: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp close body: %w", err)
	}

	return c.Quit()
}

// SMTPAvailable checks if Proton Bridge SMTP is reachable.
func SMTPAvailable(cfg *Config) bool {
	addr := net.JoinHostPort(cfg.IMAPHost, strconv.Itoa(cfg.SMTPPort))
	conn, err := net.DialTimeout("tcp", addr, 2*1e9) // 2 seconds
	if err != nil {
		return false
	}
	conn.Close()
	return true
}
