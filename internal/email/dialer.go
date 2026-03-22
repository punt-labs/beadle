package email

import "log/slog"

// Dialer creates an IMAP client connection. The production implementation
// calls Dial(); tests can substitute an implementation that connects to
// an in-process server.
type Dialer interface {
	Dial(cfg *Config, logger *slog.Logger) (*Client, error)
}

// DefaultDialer is the production dialer that connects via TCP+STARTTLS.
type DefaultDialer struct{}

// Dial connects to the IMAP server configured in cfg.
func (DefaultDialer) Dial(cfg *Config, logger *slog.Logger) (*Client, error) {
	return Dial(cfg, logger)
}
