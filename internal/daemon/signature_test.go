package daemon

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// shortGPGHome creates a GPG homedir with a path short enough for
// gpg-agent's Unix socket (108-byte limit), per docs/TESTING.md.
func shortGPGHome(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "bg-*")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	home := filepath.Join(dir, "g")
	require.NoError(t, os.Mkdir(home, 0o700))
	return home
}

func gpgBinary(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("gpg")
	if err != nil {
		t.Skip("gpg not installed")
	}
	return bin
}

// genOwnerKey generates an ephemeral keypair in home and returns its full
// 40-hex fingerprint. expire is a gpg --quick-generate-key expiration
// spec, e.g. "1y" or "0" for non-expiring.
func genOwnerKey(t *testing.T, gpgBin, home, email, expire string) string {
	t.Helper()
	cmd := exec.Command(gpgBin, "--homedir", home, "--batch", "--no-tty",
		"--pinentry-mode", "loopback", "--passphrase", "",
		"--quick-generate-key", fmt.Sprintf("Test <%s>", email), "default", "default", expire)
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Run(), "key generation failed for %s", email)
	return fingerprintOf(t, gpgBin, home, email)
}

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
	t.Fatalf("no fingerprint found for %s", keyID)
	return ""
}

// signCanonical detach-signs data using signerEmail's key in home,
// returning the ASCII-armored signature.
func signCanonical(t *testing.T, gpgBin, home, signerEmail string, data []byte) string {
	t.Helper()
	cmd := exec.Command(gpgBin, "--homedir", home, "--batch", "--no-tty",
		"--pinentry-mode", "loopback", "--passphrase", "",
		"--detach-sign", "--armor", "-u", signerEmail)
	cmd.Stdin = bytes.NewReader(data)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	require.NoError(t, cmd.Run(), stderr.String())
	return stdout.String()
}

func newFixtureCommand() *Command {
	return &Command{
		Name:         "wall",
		Description:  "Broadcast a message",
		Runner:       "claude",
		Mode:         "passthrough",
		Prompt:       "do the thing",
		OutputSchema: "text",
	}
}

func TestVerifySignature_Success(t *testing.T) {
	gpgBin := gpgBinary(t)
	home := shortGPGHome(t)
	const ownerEmail = "owner-success@example.com"
	fpr := genOwnerKey(t, gpgBin, home, ownerEmail, "1y")
	t.Setenv("GNUPGHOME", home)

	cmd := newFixtureCommand()
	canon, err := CanonicalCommandBytes(cmd)
	require.NoError(t, err)
	cmd.Signature = signCanonical(t, gpgBin, home, ownerEmail, canon)

	err = VerifySignature(cmd, gpgBin, fpr)
	assert.NoError(t, err)
}

func TestVerifySignature_MissingSignature(t *testing.T) {
	cmd := newFixtureCommand()
	cmd.Signature = ""

	err := VerifySignature(cmd, "gpg", strings.Repeat("A", 40))
	require.Error(t, err)
	var sigErr *SignatureError
	require.ErrorAs(t, err, &sigErr)
	assert.Equal(t, ReasonMissing, sigErr.Reason)
}

func TestVerifySignature_InvalidFingerprintFormat(t *testing.T) {
	tests := []struct {
		name       string
		ownerKeyID string
	}{
		{name: "short key id", ownerKeyID: "6E628EF53FBAF570"},
		{name: "email address", ownerKeyID: "owner@example.com"},
		{name: "0x-prefixed", ownerKeyID: "0x" + strings.Repeat("A", 40)},
		{name: "too short", ownerKeyID: strings.Repeat("A", 39)},
		{name: "too long", ownerKeyID: strings.Repeat("A", 41)},
		{name: "empty", ownerKeyID: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := newFixtureCommand()
			cmd.Signature = "deadbeef" // present, so the fingerprint check is what fails

			err := VerifySignature(cmd, "gpg", tt.ownerKeyID)
			require.Error(t, err)
			var sigErr *SignatureError
			require.ErrorAs(t, err, &sigErr)
			assert.Equal(t, ReasonInvalid, sigErr.Reason)
		})
	}
}

func TestVerifySignature_WrongKey(t *testing.T) {
	gpgBin := gpgBinary(t)
	home := shortGPGHome(t)
	const ownerEmail = "owner-wrongkey@example.com"
	const otherEmail = "other-wrongkey@example.com"
	ownerFpr := genOwnerKey(t, gpgBin, home, ownerEmail, "1y")
	genOwnerKey(t, gpgBin, home, otherEmail, "1y")
	t.Setenv("GNUPGHOME", home)

	cmd := newFixtureCommand()
	canon, err := CanonicalCommandBytes(cmd)
	require.NoError(t, err)
	// Signed by a key other than the configured owner's.
	cmd.Signature = signCanonical(t, gpgBin, home, otherEmail, canon)

	err = VerifySignature(cmd, gpgBin, ownerFpr)
	require.Error(t, err)
	var sigErr *SignatureError
	require.ErrorAs(t, err, &sigErr)
	assert.Equal(t, ReasonWrongKey, sigErr.Reason)
}

func TestVerifySignature_KeyExpired(t *testing.T) {
	gpgBin := gpgBinary(t)
	home := shortGPGHome(t)
	const ownerEmail = "owner-expired@example.com"
	fpr := genOwnerKey(t, gpgBin, home, ownerEmail, "0") // "0" = never expires -> rejected
	t.Setenv("GNUPGHOME", home)

	cmd := newFixtureCommand()
	canon, err := CanonicalCommandBytes(cmd)
	require.NoError(t, err)
	cmd.Signature = signCanonical(t, gpgBin, home, ownerEmail, canon)

	err = VerifySignature(cmd, gpgBin, fpr)
	require.Error(t, err)
	var sigErr *SignatureError
	require.ErrorAs(t, err, &sigErr)
	assert.Equal(t, ReasonKeyExpired, sigErr.Reason)
}

func TestVerifySignature_Tampered(t *testing.T) {
	gpgBin := gpgBinary(t)
	home := shortGPGHome(t)
	const ownerEmail = "owner-tampered@example.com"
	fpr := genOwnerKey(t, gpgBin, home, ownerEmail, "1y")
	t.Setenv("GNUPGHOME", home)

	cmd := newFixtureCommand()
	canon, err := CanonicalCommandBytes(cmd)
	require.NoError(t, err)
	cmd.Signature = signCanonical(t, gpgBin, home, ownerEmail, canon)

	// Tamper with the command after signing: the canonical bytes at
	// verify time no longer match what was signed.
	cmd.Description = "tampered after signing"

	err = VerifySignature(cmd, gpgBin, fpr)
	require.Error(t, err)
	var sigErr *SignatureError
	require.ErrorAs(t, err, &sigErr)
	assert.Equal(t, ReasonInvalid, sigErr.Reason)
}

func TestCanonicalCommandBytes_ClearsSignature(t *testing.T) {
	cmd := &Command{Name: "x", Signature: "deadbeef", Prompt: "p", OutputSchema: "text"}

	data, err := CanonicalCommandBytes(cmd)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "deadbeef")

	// CanonicalCommandBytes must not mutate its argument.
	assert.Equal(t, "deadbeef", cmd.Signature)
}

// TestCanonicalCommandBytes_MapKeyOrderDeterminism guards the one field
// where Go map iteration order could otherwise break determinism:
// Command.OutputSchema, typed any, can hold a decoded map[string]any.
// Two fixtures declare the same output_schema keys in different source
// order; CanonicalCommandBytes must produce byte-identical output for
// both.
func TestCanonicalCommandBytes_MapKeyOrderDeterminism(t *testing.T) {
	yamlA := `name: schema-order
prompt: hello
output_schema:
  alpha: {type: string}
  beta: {type: number}
  gamma: {type: boolean}
budget:
  rounds: 1
`
	yamlB := `name: schema-order
prompt: hello
output_schema:
  gamma: {type: boolean}
  alpha: {type: string}
  beta: {type: number}
budget:
  rounds: 1
`
	var cmdA, cmdB Command
	require.NoError(t, yaml.Unmarshal([]byte(yamlA), &cmdA))
	require.NoError(t, yaml.Unmarshal([]byte(yamlB), &cmdB))

	bytesA, err := CanonicalCommandBytes(&cmdA)
	require.NoError(t, err)
	bytesB, err := CanonicalCommandBytes(&cmdB)
	require.NoError(t, err)

	assert.Equal(t, bytesA, bytesB)
}

func TestClassifyStatusLines(t *testing.T) {
	tests := []struct {
		name       string
		output     string
		wantNil    bool
		wantReason SignatureReason
	}{
		{
			name:    "goodsig",
			output:  "[GNUPG:] NEWSIG\n[GNUPG:] GOODSIG ABCDEF Owner <owner@example.com>\n",
			wantNil: true,
		},
		{
			name:       "badsig",
			output:     "[GNUPG:] BADSIG ABCDEF Owner <owner@example.com>\n",
			wantReason: ReasonInvalid,
		},
		{
			name:       "errsig alone",
			output:     "[GNUPG:] ERRSIG ABCDEF 22 10 00 1234567890 9 ABCDEF\n",
			wantReason: ReasonInvalid,
		},
		{
			name:       "revkeysig",
			output:     "[GNUPG:] REVKEYSIG ABCDEF Owner <owner@example.com>\n",
			wantReason: ReasonInvalid,
		},
		{
			name:       "no_pubkey alone",
			output:     "[GNUPG:] NO_PUBKEY ABCDEF\n",
			wantReason: ReasonWrongKey,
		},
		{
			// Real gpg emits ERRSIG before NO_PUBKEY for a missing key.
			// NO_PUBKEY must still win.
			name:       "errsig then no_pubkey — no_pubkey wins",
			output:     "[GNUPG:] ERRSIG ABCDEF 22 10 00 1234567890 9 ABCDEF\n[GNUPG:] NO_PUBKEY ABCDEF\n",
			wantReason: ReasonWrongKey,
		},
		{
			name:       "no_pubkey then errsig — no_pubkey still wins",
			output:     "[GNUPG:] NO_PUBKEY ABCDEF\n[GNUPG:] ERRSIG ABCDEF 22 10 00 1234567890 9 ABCDEF\n",
			wantReason: ReasonWrongKey,
		},
		{
			// Proves the default arm fires for a status keyword this
			// switch does not recognize — never an implicit nil.
			name:       "unrecognized status line",
			output:     "[GNUPG:] TRUST_UNDEFINED 0 pgp owner@example.com\n",
			wantReason: ReasonInvalid,
		},
		{
			name:       "no status lines at all",
			output:     "gpg: some human-readable line, no [GNUPG:] prefix\n",
			wantReason: ReasonInvalid,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := classifyStatusLines(tt.output)
			if tt.wantNil {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			var sigErr *SignatureError
			require.ErrorAs(t, err, &sigErr)
			assert.Equal(t, tt.wantReason, sigErr.Reason)
		})
	}
}

func TestCountOwnerKeyMatches(t *testing.T) {
	const fpr = "C987540DFAEA8D216FE8EEB5B9FD49C7EA2984C2"

	tests := []struct {
		name   string
		output string
		want   int
	}{
		{name: "no keys", output: "", want: 0},
		{
			name: "one match",
			output: "pub:u:255:22:B9FD49C7EA2984C2:1788060399:1819596399::u:::scESC:::::ed25519:::0:\n" +
				"fpr:::::::::" + fpr + ":\n",
			want: 1,
		},
		{
			name: "pub record with a different fingerprint",
			output: "pub:u:255:22:AAAAAAAAAAAAAAAA:1788060399:1819596399::u:::scESC:::::ed25519:::0:\n" +
				"fpr:::::::::0000000000000000000000000000000000000000:\n",
			want: 0,
		},
		{
			name:   "case-insensitive match",
			output: "pub:u:255:22:B9FD49C7EA2984C2:1788060399:1819596399::u:::scESC:::::ed25519:::0:\nfpr:::::::::" + strings.ToLower(fpr) + ":\n",
			want:   1,
		},
		{
			// A full-fingerprint collision cannot occur with real gpg
			// output — fingerprints are cryptographically unique. This
			// fixture exercises the ambiguity guard the same way
			// pgp.parseColonExpiry's own tests exercise theirs: with a
			// fabricated multi-match fixture, not a real collision.
			name: "two matches (fabricated)",
			output: "pub:u:255:22:B9FD49C7EA2984C2:1788060399:1819596399::u:::scESC:::::ed25519:::0:\n" +
				"fpr:::::::::" + fpr + ":\n" +
				"pub:u:255:22:B9FD49C7EA2984C2:1788060399:1819596399::u:::scESC:::::ed25519:::0:\n" +
				"fpr:::::::::" + fpr + ":\n",
			want: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, countOwnerKeyMatches(tt.output, fpr))
		})
	}
}

func TestAssertSingleOwnerKey_AmbiguityIsSignatureError(t *testing.T) {
	// countOwnerKeyMatches is exercised directly above; this confirms
	// assertSingleOwnerKey's zero-match path (a real, reachable case: the
	// owner key simply isn't present) surfaces as a *SignatureError with
	// ReasonInvalid, per the design's ambiguity-guard contract.
	gpgBin := gpgBinary(t)
	home := shortGPGHome(t)

	err := assertSingleOwnerKey(gpgBin, home, strings.Repeat("A", 40))
	require.Error(t, err)
	var sigErr *SignatureError
	require.ErrorAs(t, err, &sigErr)
	assert.Equal(t, ReasonInvalid, sigErr.Reason)
}
