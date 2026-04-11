package email

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"strconv"
	"time"
)

// SMTPSend delivers a raw RFC 822 message via SMTP. Uses implicit TLS for
// port 465, STARTTLS otherwise.
//
// SMTP authentication uses smtp_user and smtp_password when configured,
// falling back to IMAP credentials for backward compatibility.
// Recipients includes all envelope recipients (to + cc + bcc).
func SMTPSend(cfg *Config, from string, recipients []string, raw []byte) error {
	host := cfg.SMTPEffectiveHost()
	addr := net.JoinHostPort(host, strconv.Itoa(cfg.SMTPPort))

	password, err := cfg.SMTPPassword()
	if err != nil {
		return fmt.Errorf("read smtp password: %w", err)
	}

	tlsCfg := &tls.Config{
		ServerName:         host,
		InsecureSkipVerify: isLoopback(host), //nolint:gosec // Proton Bridge uses self-signed certs on localhost
	}

	var c *smtp.Client

	if cfg.SMTPPort == 465 {
		// Implicit TLS (SMTPS)
		conn, dialErr := tls.DialWithDialer(
			&net.Dialer{Timeout: 10 * time.Second},
			"tcp", addr, tlsCfg,
		)
		if dialErr != nil {
			return fmt.Errorf("dial smtps %s: %w", addr, dialErr)
		}
		c, err = smtp.NewClient(conn, host)
		if err != nil {
			conn.Close()
			return fmt.Errorf("smtp client %s: %w", addr, err)
		}
	} else {
		// STARTTLS — Proton Bridge on localhost, or explicit config
		conn, dialErr := net.DialTimeout("tcp", addr, 10*time.Second)
		if dialErr != nil {
			return fmt.Errorf("dial smtp %s: %w", addr, dialErr)
		}
		c, err = smtp.NewClient(conn, host)
		if err != nil {
			conn.Close()
			return fmt.Errorf("smtp client %s: %w", addr, err)
		}
		if err := c.StartTLS(tlsCfg); err != nil {
			c.Close()
			return fmt.Errorf("smtp starttls: %w", err)
		}
	}
	defer c.Close()

	auth := smtp.PlainAuth("", cfg.SMTPEffectiveUser(), password, host)
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
	addr := net.JoinHostPort(cfg.SMTPEffectiveHost(), strconv.Itoa(cfg.SMTPPort))
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}
