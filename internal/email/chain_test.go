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
		Threading{},
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
		Threading{},
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "encryption requires signing")
}

func TestResendRequest_RepoTagHeaders(t *testing.T) {
	tag := RepoTag{Slug: "punt-labs/beadle", Agent: "claude"}
	req, err := resendRequest(
		[]string{"to@example.com"}, nil, nil,
		"[punt-labs/beadle] Hi", "body", "", nil, tag,
		Threading{InReplyTo: "<orig@host>", References: []string{"<root@host>", "<orig@host>"}},
	)
	require.NoError(t, err)
	assert.Equal(t,
		map[string]string{
			HeaderRepo:    "punt-labs/beadle",
			HeaderAgent:   "claude",
			"In-Reply-To": "<orig@host>",
			"References":  "<root@host> <orig@host>",
		},
		req.Headers,
		"Resend request must carry the X-Beadle-* and threading headers")
	assert.Equal(t, "[punt-labs/beadle] Hi", req.Subject)

	// An empty tag and empty threading leave the Resend headers unset.
	req, err = resendRequest(
		[]string{"to@example.com"}, nil, nil,
		"Hi", "body", "", nil, RepoTag{},
		Threading{},
	)
	require.NoError(t, err)
	assert.Nil(t, req.Headers, "empty tag must not set Resend headers")
}

// TestResendRequest_RejectsHeaderInjection proves the Resend JSON path applies
// the same CR/LF rejection as the raw-MIME path: a tag or threading value with
// CR/LF is refused with an error and no injected header reaches the request.
// Threading values originate from untrusted inbound mail, so both send paths
// must defend identically.
func TestResendRequest_RejectsHeaderInjection(t *testing.T) {
	tests := []struct {
		name      string
		tag       RepoTag
		threading Threading
	}{
		{"repo slug CRLF", RepoTag{Slug: "punt-labs/beadle\r\nBcc: evil@x"}, Threading{}},
		{"repo agent CRLF", RepoTag{Slug: "punt-labs/beadle", Agent: "claude\nBcc: evil@x"}, Threading{}},
		{"in-reply-to CRLF", RepoTag{}, Threading{InReplyTo: "<a@x>\r\nBcc: evil@x"}},
		{"references CRLF", RepoTag{}, Threading{References: []string{"<a@x>\nBcc: evil@x"}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req, err := resendRequest(
				[]string{"to@example.com"}, nil, nil,
				"Hi", "body", "", nil, tc.tag, tc.threading,
			)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "CR/LF")
			assert.Nil(t, req.Headers, "no header may reach the Resend request on rejection")
		})
	}
}
