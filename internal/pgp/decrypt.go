package pgp

import (
	"bytes"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"os"
	"os/exec"
	"strings"
)

// DecryptResult holds the outcome of PGP decryption.
type DecryptResult struct {
	Plaintext []byte `json:"plaintext"`        // Decrypted content (may be a full MIME message)
	KeyID     string `json:"key_id,omitempty"` // Recipient key used for decryption
	Signed    bool   `json:"signed"`           // Whether the decrypted content was also signed
	Signer    string `json:"signer,omitempty"` // Signer identity if signed
	Output    string `json:"output"`           // gpg stderr output
}

// Decrypt decrypts a PGP/MIME encrypted message (RFC 3156).
//
// It extracts the encrypted payload from a multipart/encrypted message,
// runs gpg --decrypt, and returns the plaintext. Unlike Verify, Decrypt
// uses the default GNUPGHOME because the agent's private key lives in
// the system keyring.
func Decrypt(gpgBinary, passphrase string, raw []byte) (*DecryptResult, error) {
	ciphertext, err := extractEncryptedPayload(raw)
	if err != nil {
		return nil, err
	}

	// Write passphrase to a temp file so gpg can read it via --passphrase-file.
	// Same pattern as detachSign — avoids exposing passphrase in ps output.
	f, err := os.CreateTemp("", "beadle-pp-*")
	if err != nil {
		return nil, fmt.Errorf("create passphrase file: %w", err)
	}
	defer os.Remove(f.Name())

	if _, err := f.WriteString(passphrase); err != nil {
		f.Close()
		return nil, fmt.Errorf("write passphrase file: %w", err)
	}
	f.Close()

	cmd := exec.Command(gpgBinary,
		"--batch", "--no-tty",
		"--pinentry-mode", "loopback",
		"--passphrase-file", f.Name(),
		"--decrypt",
	)

	var stdout, stderr bytes.Buffer
	cmd.Stdin = bytes.NewReader(ciphertext)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()
	output := strings.TrimSpace(stderr.String())
	if err != nil {
		return nil, fmt.Errorf("gpg decrypt: %w: %s", err, output)
	}

	result := &DecryptResult{
		Plaintext: stdout.Bytes(),
		Output:    output,
	}

	// Parse gpg stderr for key ID and signer info.
	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, "Good signature from") {
			result.Signed = true
			result.Signer = extractQuoted(line)
		}
		if strings.Contains(line, "encrypted with") {
			parts := strings.Fields(line)
			for i, p := range parts {
				if p == "ID" && i+1 < len(parts) {
					result.KeyID = strings.TrimSuffix(parts[i+1], ",")
				}
			}
		}
	}

	return result, nil
}

// IsEncrypted reports whether raw RFC822 bytes are a PGP/MIME encrypted
// message (Content-Type: multipart/encrypted with protocol=application/pgp-encrypted).
func IsEncrypted(raw []byte) bool {
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return false
	}
	ct := msg.Header.Get("Content-Type")
	if ct == "" {
		return false
	}
	mediaType, params, err := mime.ParseMediaType(ct)
	if err != nil {
		return false
	}
	return mediaType == "multipart/encrypted" &&
		strings.Contains(params["protocol"], "pgp-encrypted")
}

// extractEncryptedPayload extracts the encrypted data part from a
// multipart/encrypted (RFC 3156) message. The first part is the version
// identifier (application/pgp-encrypted), the second is the ciphertext
// (application/octet-stream).
func extractEncryptedPayload(raw []byte) ([]byte, error) {
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("parse message: %w", err)
	}

	ct := msg.Header.Get("Content-Type")
	mediaType, params, _ := mime.ParseMediaType(ct)

	if mediaType != "multipart/encrypted" {
		return nil, fmt.Errorf("message is %s, not multipart/encrypted", mediaType)
	}

	boundary := params["boundary"]
	if boundary == "" {
		return nil, fmt.Errorf("multipart/encrypted but no boundary found")
	}

	// Split raw bytes on boundary, same approach as extractSignedParts.
	boundaryBytes := []byte("--" + boundary)
	parts := bytes.Split(raw, boundaryBytes)
	// parts[0] = preamble, [1] = version part, [2] = encrypted data

	if len(parts) < 3 {
		return nil, fmt.Errorf("could not split multipart/encrypted into parts")
	}

	// The encrypted data is in parts[2]. Parse as a mini MIME part to
	// extract the body after the headers.
	dataPart := parts[2]
	var body []byte
	if idx := bytes.Index(dataPart, []byte("\r\n\r\n")); idx >= 0 {
		body = bytes.TrimSpace(dataPart[idx+4:])
	} else if idx := bytes.Index(dataPart, []byte("\n\n")); idx >= 0 {
		body = bytes.TrimSpace(dataPart[idx+2:])
	} else {
		body = bytes.TrimSpace(dataPart)
	}

	// Remove trailing boundary closer if present
	if idx := bytes.Index(body, boundaryBytes); idx >= 0 {
		body = bytes.TrimSpace(body[:idx])
	}

	if len(body) == 0 {
		return nil, fmt.Errorf("encrypted data part is empty")
	}

	// gpg --decrypt needs the PGP message block. If the raw-split
	// approach found it, use it. Otherwise fall back to mime/multipart.
	if bytes.Contains(body, []byte("-----BEGIN PGP MESSAGE-----")) {
		start := bytes.Index(body, []byte("-----BEGIN PGP MESSAGE-----"))
		end := bytes.Index(body, []byte("-----END PGP MESSAGE-----"))
		if end > start {
			return body[start : end+len("-----END PGP MESSAGE-----")], nil
		}
		return body, nil
	}

	return extractEncryptedPayloadMultipart(msg.Body, boundary)
}

// extractEncryptedPayloadMultipart uses mime/multipart as a fallback
// when raw byte splitting doesn't find the PGP message marker.
func extractEncryptedPayloadMultipart(body io.Reader, boundary string) ([]byte, error) {
	mr := multipart.NewReader(body, boundary)
	idx := 0
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read multipart: %w", err)
		}
		// Second part (idx=1) is the encrypted data
		if idx == 1 {
			data, err := io.ReadAll(part)
			if err != nil {
				return nil, fmt.Errorf("read encrypted part: %w", err)
			}
			return data, nil
		}
		idx++
	}
	return nil, fmt.Errorf("encrypted data part not found")
}
