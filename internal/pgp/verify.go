// Package pgp handles PGP signature verification by shelling out to the gpg binary.
//
// This avoids importing a Go PGP library — gpg is already available and handles
// keyring management, trust models, and signature formats.
package pgp

import (
	"bytes"
	"fmt"
	"io"
	"mime"
	"net/mail"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// VerifyResult holds the outcome of a PGP signature verification.
type VerifyResult struct {
	Valid       bool   `json:"valid"`
	KeyID      string `json:"key_id,omitempty"`
	Signer     string `json:"signer,omitempty"`
	Output     string `json:"output"`
	KeyImported bool  `json:"key_imported"`
}

// Verify checks the PGP signature on a raw RFC822 message.
//
// It extracts the signed body and detached signature from a multipart/signed
// message (RFC 3156 / PGP/MIME), imports any attached public key, and runs
// gpg --verify in an isolated GNUPGHOME.
func Verify(gpgBinary string, raw []byte) (*VerifyResult, error) {
	signedData, sigBytes, pubkeyBytes, err := extractSignedParts(raw)
	if err != nil {
		return nil, err
	}

	// Create isolated GPG homedir under /tmp to keep the path short.
	// gpg-agent communicates via Unix socket, which has a 108-byte path limit.
	// os.MkdirTemp("") on macOS yields /var/folders/... paths that exceed this.
	tmpDir, err := os.MkdirTemp("/tmp", "bg-*")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	gpgHome := filepath.Join(tmpDir, "gnupg")
	if err := os.Mkdir(gpgHome, 0700); err != nil {
		return nil, fmt.Errorf("create gpg home: %w", err)
	}

	gpgBase := []string{"--homedir", gpgHome, "--batch", "--no-tty"}

	result := &VerifyResult{}

	// Import public key if attached to the message
	if pubkeyBytes != nil {
		keyFile := filepath.Join(tmpDir, "sender.asc")
		if err := os.WriteFile(keyFile, pubkeyBytes, 0600); err != nil {
			return nil, fmt.Errorf("write key file: %w", err)
		}

		args := append(gpgBase, "--import", keyFile)
		cmd := exec.Command(gpgBinary, args...)
		cmd.Stderr = io.Discard
		if cmd.Run() == nil {
			result.KeyImported = true
		}
	}

	// If no key was attached, try exporting from the system keyring.
	// This is a one-way bridge: we read from ~/.gnupg but never write to it.
	if !result.KeyImported {
		exportAll(gpgBinary, gpgHome)
	}

	// Write signed data and signature to temp files
	bodyFile := filepath.Join(tmpDir, "signed_body")
	if err := os.WriteFile(bodyFile, signedData, 0600); err != nil {
		return nil, fmt.Errorf("write body file: %w", err)
	}

	sigFile := filepath.Join(tmpDir, "signature.asc")
	if err := os.WriteFile(sigFile, sigBytes, 0600); err != nil {
		return nil, fmt.Errorf("write sig file: %w", err)
	}

	// Verify
	args := append(gpgBase, "--verify", sigFile, bodyFile)
	cmd := exec.Command(gpgBinary, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	err = cmd.Run()
	result.Output = strings.TrimSpace(stderr.String())
	result.Valid = err == nil

	// Parse signer info from gpg output
	for _, line := range strings.Split(result.Output, "\n") {
		if strings.Contains(line, "Good signature from") {
			result.Signer = extractQuoted(line)
		}
		if strings.Contains(line, "using") && strings.Contains(line, "key") {
			parts := strings.Fields(line)
			if len(parts) > 0 {
				result.KeyID = parts[len(parts)-1]
			}
		}
	}

	return result, nil
}

// extractSignedParts extracts the signed body, detached signature, and optional
// public key from a multipart/signed (RFC 3156) message.
func extractSignedParts(raw []byte) (signedData, signature, pubkey []byte, err error) {
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("parse message: %w", err)
	}

	ct := msg.Header.Get("Content-Type")
	mediaType, params, _ := mime.ParseMediaType(ct)

	if mediaType != "multipart/signed" {
		// Check if there's a pgp-signature part somewhere
		if strings.Contains(string(raw), "application/pgp-signature") {
			return nil, nil, nil, fmt.Errorf("found PGP signature but message is %s, not multipart/signed — cannot determine signed bytes", mediaType)
		}
		return nil, nil, nil, fmt.Errorf("message is %s, not multipart/signed, and contains no PGP signature", mediaType)
	}

	boundary := params["boundary"]
	if boundary == "" {
		return nil, nil, nil, fmt.Errorf("multipart/signed but no boundary found")
	}

	// Split raw bytes on boundary to get verbatim signed part.
	// We must use raw bytes (not re-serialized) to preserve the exact
	// bytes that were signed, including CRLF line endings.
	boundaryBytes := []byte("--" + boundary)
	parts := bytes.Split(raw, boundaryBytes)
	// parts[0] = preamble, [1] = first part (signed data), [2] = second part (signature), ...

	if len(parts) < 3 {
		return nil, nil, nil, fmt.Errorf("could not split multipart/signed into parts")
	}

	// Signed data: strip leading CRLF after boundary, trailing CRLF before next boundary
	signedData = parts[1]
	if bytes.HasPrefix(signedData, []byte("\r\n")) {
		signedData = signedData[2:]
	}
	if bytes.HasSuffix(signedData, []byte("\r\n")) {
		signedData = signedData[:len(signedData)-2]
	}

	// The signature is in parts[2] — parse it as a mini MIME part
	sigPart := parts[2]
	// Find the blank line separating headers from body
	if idx := bytes.Index(sigPart, []byte("\r\n\r\n")); idx >= 0 {
		signature = bytes.TrimSpace(sigPart[idx+4:])
	} else if idx := bytes.Index(sigPart, []byte("\n\n")); idx >= 0 {
		signature = bytes.TrimSpace(sigPart[idx+2:])
	} else {
		return nil, nil, nil, fmt.Errorf("could not extract signature payload")
	}

	// Remove trailing boundary closer if present
	if idx := bytes.Index(signature, boundaryBytes); idx >= 0 {
		signature = bytes.TrimSpace(signature[:idx])
	}

	// Look for attached public key in remaining parts
	for i := 3; i < len(parts); i++ {
		if bytes.Contains(parts[i], []byte("application/pgp-keys")) {
			if idx := bytes.Index(parts[i], []byte("\r\n\r\n")); idx >= 0 {
				pubkey = bytes.TrimSpace(parts[i][idx+4:])
			}
		}
	}

	return signedData, signature, pubkey, nil
}

// exportAll exports all public keys from the system keyring into the
// isolated homedir. This lets us verify signatures from senders whose
// keys we've previously imported, without requiring the key to be
// attached to the message.
func exportAll(gpgBinary, gpgHome string) {
	// Export from system keyring (default GNUPGHOME)
	export := exec.Command(gpgBinary, "--batch", "--no-tty", "--export")
	var keyData bytes.Buffer
	export.Stdout = &keyData
	export.Stderr = io.Discard
	if export.Run() != nil || keyData.Len() == 0 {
		return
	}

	// Import into isolated keyring
	importCmd := exec.Command(gpgBinary, "--homedir", gpgHome, "--batch", "--no-tty", "--import")
	importCmd.Stdin = &keyData
	importCmd.Stderr = io.Discard
	importCmd.Run() //nolint:errcheck // best-effort
}

func extractQuoted(s string) string {
	start := strings.IndexByte(s, '"')
	if start < 0 {
		return ""
	}
	end := strings.IndexByte(s[start+1:], '"')
	if end < 0 {
		return ""
	}
	return s[start+1 : start+1+end]
}
