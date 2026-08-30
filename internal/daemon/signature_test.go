package daemon

import (
	"bytes"
	"errors"
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

// TestVerifySignature is table-driven, one subtest per SignatureReason
// plus a genuine end-to-end success case. Each case's setup produces a
// *Command and an ownerKeyID against real gpg state (ephemeral keys under
// an isolated GNUPGHOME) or, for the fingerprint-format cases, no gpg
// state at all — the check fails before gpg is ever invoked.
func TestVerifySignature(t *testing.T) {
	gpgBin := gpgBinary(t)

	tests := []struct {
		name    string
		setup   func(t *testing.T) (cmd *Command, ownerKeyID string)
		wantNil bool
		reason  SignatureReason
	}{
		{
			name: "success",
			setup: func(t *testing.T) (*Command, string) {
				home := shortGPGHome(t)
				const email = "owner-success@example.com"
				fpr := genOwnerKey(t, gpgBin, home, email, "1y")
				t.Setenv("GNUPGHOME", home)

				cmd := newFixtureCommand()
				canon, err := CanonicalCommandBytes(cmd)
				require.NoError(t, err)
				cmd.Signature = signCanonical(t, gpgBin, home, email, canon)
				return cmd, fpr
			},
			wantNil: true,
		},
		{
			name: "missing signature",
			setup: func(*testing.T) (*Command, string) {
				cmd := newFixtureCommand()
				cmd.Signature = ""
				return cmd, strings.Repeat("A", 40)
			},
			reason: ReasonMissing,
		},
		{
			name: "wrong key — signed by an unrelated keypair",
			setup: func(t *testing.T) (*Command, string) {
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
				return cmd, ownerFpr
			},
			reason: ReasonWrongKey,
		},
		{
			name: "key expired — non-expiring owner key",
			setup: func(t *testing.T) (*Command, string) {
				home := shortGPGHome(t)
				const email = "owner-expired@example.com"
				fpr := genOwnerKey(t, gpgBin, home, email, "0") // "0" = never expires
				t.Setenv("GNUPGHOME", home)

				cmd := newFixtureCommand()
				canon, err := CanonicalCommandBytes(cmd)
				require.NoError(t, err)
				cmd.Signature = signCanonical(t, gpgBin, home, email, canon)
				return cmd, fpr
			},
			reason: ReasonKeyExpired,
		},
		{
			name: "invalid — tampered after signing",
			setup: func(t *testing.T) (*Command, string) {
				home := shortGPGHome(t)
				const email = "owner-tampered@example.com"
				fpr := genOwnerKey(t, gpgBin, home, email, "1y")
				t.Setenv("GNUPGHOME", home)

				cmd := newFixtureCommand()
				canon, err := CanonicalCommandBytes(cmd)
				require.NoError(t, err)
				cmd.Signature = signCanonical(t, gpgBin, home, email, canon)

				// Tamper after signing: the canonical bytes at verify
				// time no longer match what was signed.
				cmd.Description = "tampered after signing"
				return cmd, fpr
			},
			reason: ReasonInvalid,
		},
		{
			// A well-formed fingerprint that does not correspond to any
			// key in the ambient keyring at all — distinct from "wrong
			// key" above, where the owner's own key does exist but the
			// signature was made by someone else. gpg's --export of an
			// absent key exits 0 with empty output (verified against real
			// gpg), so importOwnerKey skips the import step entirely and
			// assertSingleOwnerKey's own list-keys lookup is what fails.
			name: "owner key absent from ambient keyring entirely",
			setup: func(t *testing.T) (*Command, string) {
				home := shortGPGHome(t)
				t.Setenv("GNUPGHOME", home)

				cmd := newFixtureCommand()
				cmd.Signature = "-----BEGIN PGP SIGNATURE-----\nnot a real signature\n-----END PGP SIGNATURE-----\n"
				return cmd, strings.Repeat("B", 40)
			},
			reason: ReasonInvalid,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, ownerKeyID := tt.setup(t)

			err := VerifySignature(cmd, gpgBin, ownerKeyID)
			if tt.wantNil {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			var sigErr *SignatureError
			require.ErrorAs(t, err, &sigErr)
			assert.Equal(t, tt.reason, sigErr.Reason)
		})
	}
}

// TestVerifySignature_InvalidFingerprintFormat covers the precondition
// VerifySignature enforces on ownerKeyID before touching gpg at all: it
// must be a full 40-hex OpenPGP fingerprint. No gpg state is needed —
// every case fails at the regex check.
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

// TestSignatureError_Error covers the Error() method's message format
// directly. Every other test in this file inspects sigErr.Reason via
// errors.As and never formats the error, so the "command signature %s: %s"
// template itself was previously never exercised.
func TestSignatureError_Error(t *testing.T) {
	err := &SignatureError{Reason: ReasonWrongKey, Detail: "NO_PUBKEY ABCDEF"}
	assert.Equal(t, "command signature wrong-key: NO_PUBKEY ABCDEF", err.Error())
}

// TestVerifySignature_MissingGPGBinary covers the exported VerifySignature
// entry point when gpg is entirely absent from the system -- as opposed to
// TestVerifyDetachedSignature_ExecStartFailure, which exercises the same
// failure mode at the narrower internal verifyDetachedSignature helper.
// VerifySignature reaches the missing binary earlier, in importOwnerKey's
// export step, and that path wraps the *exec.Error in a plain error rather
// than special-casing it the way verifyDetachedSignature does -- this test
// confirms the public entry point still surfaces it as an unwrapped
// operational failure, never as a *SignatureError.
func TestVerifySignature_MissingGPGBinary(t *testing.T) {
	cmd := newFixtureCommand()
	cmd.Signature = "-----BEGIN PGP SIGNATURE-----\nnot a real signature\n-----END PGP SIGNATURE-----\n"

	err := VerifySignature(cmd, "no-such-gpg-binary-anywhere-on-path", strings.Repeat("C", 40))
	require.Error(t, err)

	var sigErr *SignatureError
	assert.False(t, errors.As(err, &sigErr), "missing gpg binary must not be classified as a *SignatureError, got: %v", err)
}

// TestVerifyDetachedSignature_ExecStartFailure covers the case gpg never
// starts at all -- binary missing from $PATH -- as distinct from gpg
// running and exiting non-zero (the normal outcome for a bad signature,
// which classifyStatusLines handles from stdout regardless of exit code).
// gpgBinary is ordinarily a bare name ("gpg", the config default) resolved
// via exec.LookPath, so a name LookPath can't find is the realistic
// failure: it must surface as the unwrapped operational error
// VerifySignature's doc comment promises, never as a *SignatureError.
func TestVerifyDetachedSignature_ExecStartFailure(t *testing.T) {
	tmpDir := t.TempDir()
	gpgHome := filepath.Join(tmpDir, "g")
	require.NoError(t, os.Mkdir(gpgHome, 0o700))

	err := verifyDetachedSignature("no-such-gpg-binary-anywhere-on-path", gpgHome, tmpDir, []byte("data"), "not-a-real-signature")
	require.Error(t, err)

	var sigErr *SignatureError
	assert.False(t, errors.As(err, &sigErr), "exec-start failure must not be classified as a *SignatureError, got: %v", err)

	var execErr *exec.Error
	assert.True(t, errors.As(err, &execErr), "expected an *exec.Error wrapped in the returned error, got: %v", err)
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

// erroringMarshaler implements yaml.Marshaler and always fails. yaml.v3
// panics on a genuinely unmarshalable Go value (e.g. a chan), so the only
// realistic, non-panicking way to reach CanonicalCommandBytes' own error
// branch is a value whose MarshalYAML method itself returns an error --
// exactly the case this fixture exists to exercise.
type erroringMarshaler struct{}

func (erroringMarshaler) MarshalYAML() (any, error) {
	return nil, errors.New("boom")
}

func TestCanonicalCommandBytes_MarshalError(t *testing.T) {
	cmd := &Command{Name: "x", Prompt: "p", OutputSchema: erroringMarshaler{}}

	_, err := CanonicalCommandBytes(cmd)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "marshal canonical command")
}

// TestVerifySignature_CanonicalizeError covers VerifySignature's own
// wrapping of a CanonicalCommandBytes failure (line "canonicalize command
// for verification"), distinct from TestCanonicalCommandBytes_MarshalError
// above, which only reaches CanonicalCommandBytes directly. This is an
// operational failure, not a signature verdict, so it must not be a
// *SignatureError.
func TestVerifySignature_CanonicalizeError(t *testing.T) {
	cmd := newFixtureCommand()
	cmd.OutputSchema = erroringMarshaler{}
	cmd.Signature = "present"

	err := VerifySignature(cmd, "gpg", strings.Repeat("D", 40))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "canonicalize command for verification")

	var sigErr *SignatureError
	assert.False(t, errors.As(err, &sigErr), "a canonicalization failure must not be classified as a *SignatureError, got: %v", err)
}

func TestWithCLocale(t *testing.T) {
	in := []string{"PATH=/bin", "LC_ALL=en_US.UTF-8", "LANG=en_US.UTF-8"}

	out := withCLocale(in)

	assert.Contains(t, out, "PATH=/bin")
	assert.Contains(t, out, "LANG=en_US.UTF-8")
	assert.Contains(t, out, "LC_ALL=C")
	assert.NotContains(t, out, "LC_ALL=en_US.UTF-8")

	count := 0
	for _, e := range out {
		if strings.HasPrefix(e, "LC_ALL=") {
			count++
		}
	}
	assert.Equal(t, 1, count, "exactly one LC_ALL entry, the replacement")
}

func TestClassifyStatusLines(t *testing.T) {
	tests := []struct {
		name       string
		output     string
		stderr     string
		wantNil    bool
		wantReason SignatureReason
		wantDetail string // substring; empty means not checked
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
		{
			// The default arm is the one place gpg's stderr is worth
			// surfacing: an unrecognized outcome is exactly when a human
			// reading the audit log later needs gpg's own diagnostic text,
			// not just the (empty) status-fd output.
			name:       "unrecognized status line carries gpg stderr in the detail",
			output:     "[GNUPG:] TRUST_UNDEFINED 0 pgp owner@example.com\n",
			stderr:     "gpg: WARNING: something gpg printed to stderr\n",
			wantReason: ReasonInvalid,
			wantDetail: "gpg: WARNING: something gpg printed to stderr",
		},
		{
			// A line carrying the "[GNUPG:] " prefix but nothing after it
			// (or only whitespace) has fields == nil after strings.Fields,
			// exercising the len(fields) == 0 skip -- distinct from a line
			// that lacks the prefix altogether, above.
			name:       "status prefix with no fields",
			output:     "[GNUPG:] \n[GNUPG:] GOODSIG_TYPO\n",
			wantReason: ReasonInvalid,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := classifyStatusLines(tt.output, tt.stderr)
			if tt.wantNil {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			var sigErr *SignatureError
			require.ErrorAs(t, err, &sigErr)
			assert.Equal(t, tt.wantReason, sigErr.Reason)
			if tt.wantDetail != "" {
				assert.Contains(t, sigErr.Detail, tt.wantDetail)
			}
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
		{
			// A pub record with nothing after it at all -- no paired fpr
			// line follows because there are no more lines.
			name:   "pub is the last line, no paired fpr line follows",
			output: "pub:u:255:22:B9FD49C7EA2984C2:1788060399:1819596399::u:::scESC:::::ed25519:::0:",
			want:   0,
		},
		{
			// The line immediately after pub exists but isn't an fpr
			// record at all.
			name: "line after pub is not an fpr record",
			output: "pub:u:255:22:B9FD49C7EA2984C2:1788060399:1819596399::u:::scESC:::::ed25519:::0:\n" +
				"uid:u::::1788060399::842D16BA::Owner <owner@example.com>::::::::::0:\n",
			want: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, countOwnerKeyMatches(tt.output, fpr))
		})
	}
}

// TestAssertSingleOwnerKey_ExecStartFailure covers the case gpg never
// starts at all -- binary missing from $PATH -- as distinct from gpg
// running and exiting non-zero (the normal outcome when ownerKeyID simply
// isn't in the keyring, which countOwnerKeyMatches handles from stdout
// regardless of exit code). It must surface as the unwrapped operational
// error VerifySignature's doc comment promises, never as a *SignatureError.
func TestAssertSingleOwnerKey_ExecStartFailure(t *testing.T) {
	home := shortGPGHome(t)

	err := assertSingleOwnerKey("no-such-gpg-binary-anywhere-on-path", home, strings.Repeat("A", 40))
	require.Error(t, err)

	var sigErr *SignatureError
	assert.False(t, errors.As(err, &sigErr), "exec-start failure must not be classified as a *SignatureError, got: %v", err)

	var execErr *exec.Error
	assert.True(t, errors.As(err, &execErr), "expected an *exec.Error wrapped in the returned error, got: %v", err)
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

// TestAssertSingleOwnerKey_EmailQueryFindsZeroExactMatches covers the
// switch's n == 0 arm on a *successful* gpg list-keys run — as opposed to
// TestAssertSingleOwnerKey_AmbiguityIsSignatureError above, where gpg's own
// exit status is already non-zero for a query that matches nothing.
// Querying by email rather than fingerprint is exactly what
// VerifySignature's upstream fingerprint-format check exists to prevent
// (see the design doc's §1 "Key identifier format"): here two real keys
// share one email, so gpg finds and lists both (exit 0), but
// countOwnerKeyMatches only counts a pub record whose own fpr equals the
// query string exactly — an email never does — so the count is zero and
// assertSingleOwnerKey fails closed with "not found," never silently
// picking one of the two matches.
func TestAssertSingleOwnerKey_EmailQueryFindsZeroExactMatches(t *testing.T) {
	gpgBin := gpgBinary(t)
	home := shortGPGHome(t)
	const email = "shared-ambiguous@example.com"

	genOwnerKey(t, gpgBin, home, email, "1y")
	cmd := exec.Command(gpgBin, "--homedir", home, "--batch", "--no-tty",
		"--pinentry-mode", "loopback", "--passphrase", "",
		"--quick-generate-key", fmt.Sprintf("Second Owner <%s>", email), "default", "default", "1y")
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Run(), "second key generation failed")

	err := assertSingleOwnerKey(gpgBin, home, email)
	require.Error(t, err)
	var sigErr *SignatureError
	require.ErrorAs(t, err, &sigErr)
	assert.Equal(t, ReasonInvalid, sigErr.Reason)
	assert.Contains(t, sigErr.Detail, "not found")
}
