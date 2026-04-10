package pgp

import (
	"bytes"
	"os"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckKeyExpiry_WithExpiry(t *testing.T) {
	gpgBin, err := exec.LookPath("gpg")
	if err != nil {
		t.Skip("gpg not installed")
	}

	home := shortGPGHome(t)
	base := []string{"--homedir", home, "--batch", "--no-tty"}

	// Generate a key with a 1-year expiry.
	script := `%no-protection
Key-Type: RSA
Key-Length: 2048
Name-Real: Expiry Test
Name-Email: expiry@example.com
Expire-Date: 1y
%commit
`
	cmd := exec.Command(gpgBin, append(base, "--gen-key")...)
	cmd.Stdin = bytes.NewBufferString(script)
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Run(), "key generation failed")

	t.Setenv("GNUPGHOME", home)

	err = CheckKeyExpiry(gpgBin, "expiry@example.com")
	assert.NoError(t, err, "key with expiry should be accepted")
}

func TestCheckKeyExpiry_WithoutExpiry(t *testing.T) {
	gpgBin, err := exec.LookPath("gpg")
	if err != nil {
		t.Skip("gpg not installed")
	}

	home := shortGPGHome(t)
	base := []string{"--homedir", home, "--batch", "--no-tty"}

	// Generate a key with no expiry (Expire-Date: 0).
	script := `%no-protection
Key-Type: RSA
Key-Length: 2048
Name-Real: No Expiry
Name-Email: noexpiry@example.com
Expire-Date: 0
%commit
`
	cmd := exec.Command(gpgBin, append(base, "--gen-key")...)
	cmd.Stdin = bytes.NewBufferString(script)
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Run(), "key generation failed")

	t.Setenv("GNUPGHOME", home)

	err = CheckKeyExpiry(gpgBin, "noexpiry@example.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no expiration date")
}

func TestParseColonExpiry(t *testing.T) {
	tests := []struct {
		name    string
		output  string
		keyID   string
		wantErr bool
		errMsg  string
	}{
		{
			name:    "expiry set",
			output:  "pub:u:2048:1:ABCDEF:1234567890:1893456000::u:::scESC:::\nsub:...\n",
			keyID:   "test@example.com",
			wantErr: false,
		},
		{
			name:    "expiry field empty",
			output:  "pub:u:2048:1:ABCDEF:1234567890:::u:::scESC:::\n",
			keyID:   "test@example.com",
			wantErr: true,
			errMsg:  "no expiration date",
		},
		{
			name:    "expiry field zero",
			output:  "pub:u:2048:1:ABCDEF:1234567890:0::u:::scESC:::\n",
			keyID:   "test@example.com",
			wantErr: true,
			errMsg:  "no expiration date",
		},
		{
			name:    "no pub record",
			output:  "sec:u:2048:1:ABCDEF:1234567890:1893456000\n",
			keyID:   "test@example.com",
			wantErr: true,
			errMsg:  "not found",
		},
		{
			name:    "empty output",
			output:  "",
			keyID:   "test@example.com",
			wantErr: true,
			errMsg:  "not found",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := parseColonExpiry(tt.output, tt.keyID)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
