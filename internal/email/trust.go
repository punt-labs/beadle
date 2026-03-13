package email

import (
	"mime"
	"strings"

	"github.com/punt-labs/beadle/internal/channel"
)

// TrustResult explains a trust classification.
type TrustResult struct {
	Level      channel.TrustLevel `json:"level"`
	Reason     string             `json:"reason"`
	Encryption string             `json:"encryption"`
	Origin     string             `json:"origin"`
	HasSig     bool               `json:"has_signature"`
}

// ClassifyTrust determines the trust level of a message based on headers and MIME structure.
//
// Four cases:
//  1. Trusted    — Proton-to-Proton (X-Pm-Content-Encryption: end-to-end + X-Pm-Origin: internal)
//  2. Verified   — External + valid PGP signature (determined after gpg --verify)
//  3. Untrusted  — External + invalid PGP signature (determined after gpg --verify)
//  4. Unverified — External + no signature
//
// Cases 2 and 3 require PGP verification, which ClassifyTrust cannot do alone.
// When a signature is present but not yet verified, this returns Unverified
// with HasSig=true, signaling the caller to run PGP verification.
func ClassifyTrust(headers map[string]string, raw []byte) channel.TrustLevel {
	result := ClassifyTrustDetailed(headers, raw)
	return result.Level
}

// ClassifyTrustDetailed returns the full trust analysis.
func ClassifyTrustDetailed(headers map[string]string, raw []byte) TrustResult {
	enc := strings.ToLower(headers["X-Pm-Content-Encryption"])
	origin := strings.ToLower(headers["X-Pm-Origin"])

	// Case 1: Proton-to-Proton
	if strings.Contains(enc, "end-to-end") && origin == "internal" {
		return TrustResult{
			Level:      channel.Trusted,
			Reason:     "Proton-to-Proton end-to-end encrypted message",
			Encryption: "end-to-end",
			Origin:     "internal",
			HasSig:     false,
		}
	}

	// Check for PGP signature in Content-Type or MIME parts
	ct := headers["Content-Type"]
	hasSig := hasPGPSignature(ct, raw)

	if hasSig {
		// Signature present but not yet verified — caller must run PGP verification
		// to promote to Verified or demote to Untrusted
		return TrustResult{
			Level:      channel.Unverified,
			Reason:     "PGP signature present but not yet verified — call verify_signature to determine trust",
			Encryption: "tls",
			Origin:     "external",
			HasSig:     true,
		}
	}

	// Case 4: No signature
	return TrustResult{
		Level:      channel.Unverified,
		Reason:     "External message with no PGP signature",
		Encryption: "tls",
		Origin:     "external",
		HasSig:     false,
	}
}

func hasPGPSignature(contentType string, raw []byte) bool {
	if contentType != "" {
		mediaType, _, _ := mime.ParseMediaType(contentType)
		if mediaType == "multipart/signed" {
			return true
		}
	}

	// Fallback: scan raw bytes for PGP signature markers
	return strings.Contains(string(raw), "application/pgp-signature")
}
