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
		nil, nil, RepoTag{},
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
		nil, []string{"ABCD1234"}, RepoTag{},
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "encryption requires signing")
}

func TestResendRequest_RepoTagHeaders(t *testing.T) {
	tag := RepoTag{Slug: "punt-labs/beadle", Agent: "claude"}
	req := resendRequest(
		[]string{"to@example.com"}, nil, nil,
		"[punt-labs/beadle] Hi", "body", "", nil, tag,
	)
	assert.Equal(t,
		map[string]string{HeaderRepo: "punt-labs/beadle", HeaderAgent: "claude"},
		req.Headers,
		"Resend request must carry the X-Beadle-* headers")
	assert.Equal(t, "[punt-labs/beadle] Hi", req.Subject)

	// An empty tag leaves the Resend headers unset.
	req = resendRequest(
		[]string{"to@example.com"}, nil, nil,
		"Hi", "body", "", nil, RepoTag{},
	)
	assert.Nil(t, req.Headers, "empty tag must not set Resend headers")
}
