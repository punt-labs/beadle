package email

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"strconv"
	"time"
)

// SMTPSend delivers a raw RFC 822 message through Proton Bridge's SMTP server.
//
// Proton Bridge SMTP uses STARTTLS with a self-signed certificate on localhost.
// The same credentials used for IMAP work for SMTP.
// Recipients includes all envelope recipients (to + cc + bcc).
func SMTPSend(cfg *Config, from string, recipients []string, raw []byte) error {
	addr := net.JoinHostPort(cfg.SMTPHost, strconv.Itoa(cfg.SMTPPort))

	password, err := cfg.SMTPPassword()
	if err != nil {
		return fmt.Errorf("read smtp password: %w", err)
	}

	conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		return fmt.Errorf("dial smtp %s: %w", addr, err)
	}

	c, err := smtp.NewClient(conn, cfg.SMTPHost)
	if err != nil {
		conn.Close()
		return fmt.Errorf("smtp client %s: %w", addr, err)
	}
	defer c.Close()

	if err := c.StartTLS(&tls.Config{
		InsecureSkipVerify: isLoopback(cfg.SMTPHost), //nolint:gosec // Proton Bridge uses self-signed certs on localhost
	}); err != nil {
		return fmt.Errorf("smtp starttls: %w", err)
	}

	auth := smtp.PlainAuth("", cfg.SMTPUser, password, cfg.SMTPHost)
	if err := c.Auth(auth); err != nil {
		return fmt.Errorf("smtp auth: %w", err)
	}

	if err := c.Mail(from); err != nil {
		return fmt.Errorf("smtp MAIL FROM: %w", err)
	}
	for i, rcpt := range recipients {
		if err := c.Rcpt(rcpt); err != nil {
			// Omit the address from the error to avoid leaking BCC recipients
			// in logs or tool output. The SMTP server's error text (in %w)
			// may still contain the address, but we don't add it ourselves.
			return fmt.Errorf("smtp RCPT TO recipient %d/%d: %w", i+1, len(recipients), err)
		}
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
	addr := net.JoinHostPort(cfg.SMTPHost, strconv.Itoa(cfg.SMTPPort))
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}
