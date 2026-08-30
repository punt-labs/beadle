package pgp

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
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

// TestCheckKeyExpiry_MissingGPGBinary covers the case gpg is entirely
// absent from the system -- a missing-dependency failure, distinct from
// gpg running and reporting a real key problem. No gpg installation is
// required for this test: it deliberately names a binary that cannot
// exist, so it runs even on a machine with no gpg at all.
func TestCheckKeyExpiry_MissingGPGBinary(t *testing.T) {
	err := CheckKeyExpiry("no-such-gpg-binary-anywhere-on-path", "someone@example.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "gpg list-keys")
}

// fingerprintOf returns the 40-hex fingerprint gpg assigned to keyID in home.
func fingerprintOf(t *testing.T, gpgBin, home, keyID string) string {
	t.Helper()
	cmd := exec.Command(gpgBin, "--homedir", home, "--batch", "--no-tty",
		"--list-keys", "--with-colons", "--", keyID)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Run())

	for _, line := range strings.Split(stdout.String(), "\n") {
		fields := strings.Split(line, ":")
		if len(fields) > 9 && fields[0] == "fpr" {
			return fields[9]
		}
	}
	t.Fatalf("no fingerprint found for %s in %s", keyID, home)
	return ""
}

func TestCheckKeyExpiry_HomedirOption(t *testing.T) {
	gpgBin, err := exec.LookPath("gpg")
	if err != nil {
		t.Skip("gpg not installed")
	}

	// Two separate homedirs. The key exists only in "isolated" — checking
	// it without Homedir, against whatever default GNUPGHOME is ambient
	// for the test process, must not find it.
	ambient := shortGPGHome(t)
	isolated := shortGPGHome(t)
	base := []string{"--homedir", isolated, "--batch", "--no-tty"}
	genKey(t, gpgBin, base, "Homedir Option Test", "homedir-opt@example.com")

	t.Setenv("GNUPGHOME", ambient)

	err = CheckKeyExpiry(gpgBin, "homedir-opt@example.com")
	require.Error(t, err, "key must not be visible in the ambient homedir")

	err = CheckKeyExpiry(gpgBin, "homedir-opt@example.com", Homedir(isolated))
	assert.NoError(t, err, "Homedir option should redirect the check to the isolated keyring")
}

func TestCheckKeyExpiry_SigningSubkey(t *testing.T) {
	gpgBin, err := exec.LookPath("gpg")
	if err != nil {
		t.Skip("gpg not installed")
	}

	tests := []struct {
		name          string
		subkeyExpire  string
		wantErr       bool
		wantErrSubstr string
	}{
		{name: "signing subkey with expiry", subkeyExpire: "6m", wantErr: false},
		{name: "signing subkey without expiry", subkeyExpire: "0", wantErr: true, wantErrSubstr: "signing subkey"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := shortGPGHome(t)

			genCmd := exec.Command(gpgBin, "--homedir", home, "--batch", "--no-tty",
				"--pinentry-mode", "loopback", "--passphrase", "",
				"--quick-generate-key", "Subkey Expiry Test <subkey-expiry@example.com>",
				"default", "default", "1y")
			genCmd.Stderr = os.Stderr
			require.NoError(t, genCmd.Run(), "primary key generation failed")

			fpr := fingerprintOf(t, gpgBin, home, "subkey-expiry@example.com")

			addCmd := exec.Command(gpgBin, "--homedir", home, "--batch", "--no-tty",
				"--pinentry-mode", "loopback", "--passphrase", "",
				"--quick-add-key", fpr, "ed25519", "sign", tt.subkeyExpire)
			addCmd.Stderr = os.Stderr
			require.NoError(t, addCmd.Run(), "signing subkey generation failed")

			err := CheckKeyExpiry(gpgBin, fpr, Homedir(home))
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErrSubstr)
			} else {
				assert.NoError(t, err)
			}
		})
	}
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
		{
			// Two pub records with the same email — keyID is ambiguous.
			name: "multiple pub records",
			output: "pub:u:2048:1:AAAAAA:1234567890:1893456000::u:::scESC:::\n" +
				"pub:u:2048:1:BBBBBB:1234567890:1893456000::u:::scESC:::\n",
			keyID:   "test@example.com",
			wantErr: true,
			errMsg:  "ambiguous",
		},
		{
			// Primary key expiry is fine; the sub record has no signing
			// capability (encrypt-only "e"), so its empty expiry is ignored.
			name: "non-signing subkey without expiry is ignored",
			output: "pub:u:2048:1:ABCDEF:1234567890:1893456000::u:::scESC:::\n" +
				"sub:u:2048:1:111111:1234567890::::::e:::\n",
			keyID:   "test@example.com",
			wantErr: false,
		},
		{
			name: "signing subkey with expiry",
			output: "pub:u:2048:1:ABCDEF:1234567890:1893456000::u:::scESC:::\n" +
				"sub:u:2048:1:111111:1234567890:1893456000:::::s:::\n",
			keyID:   "test@example.com",
			wantErr: false,
		},
		{
			name: "signing subkey without expiry",
			output: "pub:u:2048:1:ABCDEF:1234567890:1893456000::u:::scESC:::\n" +
				"sub:u:2048:1:111111:1234567890::::::s:::\n",
			keyID:   "test@example.com",
			wantErr: true,
			errMsg:  "signing subkey with no expiration date",
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
