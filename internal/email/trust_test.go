package email

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/punt-labs/beadle/internal/channel"
)

func TestClassifyTrust_ProtonInternal(t *testing.T) {
	headers := map[string]string{
		"X-Pm-Content-Encryption": "end-to-end",
		"X-Pm-Origin":            "internal",
	}
	result := ClassifyTrustDetailed(headers, nil)

	assert.Equal(t, channel.Trusted, result.Level)
	assert.Equal(t, "end-to-end", result.Encryption)
	assert.Equal(t, "internal", result.Origin)
	assert.False(t, result.HasSig)
}

func TestClassifyTrust_ExternalNoSig(t *testing.T) {
	headers := map[string]string{
		"X-Pm-Content-Encryption": "on-delivery",
		"X-Pm-Origin":            "external",
	}
	result := ClassifyTrustDetailed(headers, nil)

	assert.Equal(t, channel.Unverified, result.Level)
	assert.Equal(t, "tls", result.Encryption)
	assert.False(t, result.HasSig)
}

func TestClassifyTrust_WithPGPSignature(t *testing.T) {
	headers := map[string]string{
		"Content-Type": "multipart/signed; protocol=\"application/pgp-signature\"",
	}
	result := ClassifyTrustDetailed(headers, nil)

	assert.Equal(t, channel.Unverified, result.Level) // Not yet verified
	assert.True(t, result.HasSig)
}

func TestClassifyTrust_EmptyHeaders(t *testing.T) {
	result := ClassifyTrustDetailed(map[string]string{}, nil)

	assert.Equal(t, channel.Unverified, result.Level)
	assert.False(t, result.HasSig)
}

func TestClassifyTrust_PGPSigInBody(t *testing.T) {
	headers := map[string]string{}
	raw := []byte("Content-Type: application/pgp-signature\r\n\r\nsig data")

	result := ClassifyTrustDetailed(headers, raw)

	assert.Equal(t, channel.Unverified, result.Level)
	assert.True(t, result.HasSig)
}
