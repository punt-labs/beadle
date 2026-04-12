package email

import (
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTrySendChain_SignedBlocksResendFallback(t *testing.T) {
	// Configure signing but with no SMTP available (port 0 won't connect).
	// TrySendChain should fail rather than silently sending unsigned via Resend.
	cfg := &Config{
		FromAddress: "test@example.com",
		IMAPHost:    "127.0.0.1",
		SMTPPort:    0, // unreachable
		GPGBinary:   "gpg",
		GPGSigner:   "test@example.com",
	}

	logger := slog.Default()
	_, err := TrySendChain(cfg, logger,
		[]string{"to@example.com"}, nil, nil,
		"Subject", "Body", "",
		nil, nil,
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pgp-signed email requires SMTP")
}

func TestTrySendChain_EncryptionRequiresSigning(t *testing.T) {
	cfg := &Config{
		FromAddress: "test@example.com",
		IMAPHost:    "127.0.0.1",
		SMTPPort:    0,
		GPGBinary:   "gpg",
		// GPGSigner intentionally empty — signing not configured.
	}

	logger := slog.Default()
	_, err := TrySendChain(cfg, logger,
		[]string{"to@example.com"}, nil, nil,
		"Subject", "Body", "",
		nil, []string{"ABCD1234"},
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "encryption requires signing")
}
