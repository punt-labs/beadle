package pgp

import (
	"bytes"
	"fmt"
	"os/exec"
)

// Encrypt encrypts data to the given recipient key IDs using gpg --encrypt.
// Uses the default GNUPGHOME because recipient public keys must be in the
// system keyring (same as Decrypt). Returns armored PGP message bytes.
//
// When selfKeyID is non-empty, the message is also encrypted to self so
// the sender can read their own sent mail.
func Encrypt(gpgBinary string, recipientKeyIDs []string, selfKeyID string, data []byte) ([]byte, error) {
	if len(recipientKeyIDs) == 0 {
		return nil, fmt.Errorf("encrypt: no recipient key IDs")
	}

	args := []string{"--batch", "--no-tty", "--encrypt", "--armor", "--trust-model", "always"}
	for _, kid := range recipientKeyIDs {
		args = append(args, "--recipient", kid)
	}
	if selfKeyID != "" {
		args = append(args, "--recipient", selfKeyID)
	}

	cmd := exec.Command(gpgBinary, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdin = bytes.NewReader(data)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("gpg encrypt: %w: %s", err, stderr.String())
	}

	return stdout.Bytes(), nil
}
