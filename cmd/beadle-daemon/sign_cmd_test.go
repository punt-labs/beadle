package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/punt-labs/beadle/internal/daemon"
	"github.com/punt-labs/beadle/internal/identity"
	"github.com/punt-labs/beadle/internal/testenv"
)

// gpgBinary returns the gpg binary path or fails the test naming the
// install remedy. Per docs/TESTING.md, a missing external dependency is a
// test failure, never a t.Skip.
func gpgBinary(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("gpg")
	if err != nil {
		t.Fatalf("gpg not found on PATH: install it (apt install gnupg / brew install gnupg): %v", err)
	}
	return bin
}

const minimalCommandYAML = `name: sysreport
description: report disk and cpu
runner: cli
binary: beadle-sysreport
output_schema: text
`

// unconfiguredDaemon returns a dataDir/resolver pair with no daemon.json --
// checkSignerMatchesAuthorizer has nothing to compare --signer against, so
// runSign proceeds unconditionally. Every test in this file that is not
// specifically exercising the authorizer-mismatch check uses this, so a
// real ~/.punt-labs/beadle/daemon.json on the machine running go test is
// never consulted.
func unconfiguredDaemon(t *testing.T) (string, *identity.Resolver) {
	t.Helper()
	dataDir := t.TempDir()
	return dataDir, identity.NewResolver(t.TempDir(), dataDir, "")
}

// TestRunSign_RoundTrip is table-driven over every shipped example recipe,
// not a single hand-picked fixture: the fixpoint property this test proves
// (decode(sign(decode(file))) canonicalizes to what was actually signed)
// must hold for a recipe with comments, defaults left to ValidateCommand
// (examples/recipes/docs-ask.yaml sets no `mode`), and multiple field
// shapes -- not just a minimal five-scalar fixture, which could pass this
// test even if the general case were broken. filepath.Glob means a newly
// added example recipe is covered automatically.
func TestRunSign_RoundTrip(t *testing.T) {
	recipes, err := filepath.Glob("../../examples/recipes/*.yaml")
	require.NoError(t, err)
	require.NotEmpty(t, recipes, "expected at least one example recipe under examples/recipes/")

	for _, recipePath := range recipes {
		t.Run(filepath.Base(recipePath), func(t *testing.T) {
			gpgBin := gpgBinary(t)
			home := testenv.ShortGPGHome(t)
			t.Setenv("GNUPGHOME", home)
			fpr := testenv.GenOwnerKey(t, gpgBin, home, "sign-test@example.com", "1y")

			original, err := os.ReadFile(recipePath)
			require.NoError(t, err)
			path := filepath.Join(t.TempDir(), filepath.Base(recipePath))
			require.NoError(t, os.WriteFile(path, original, 0o600))

			dataDir, resolver := unconfiguredDaemon(t)

			var out bytes.Buffer
			err = runSign(&out, dataDir, resolver, path, fpr, gpgBin, false)
			require.NoError(t, err)
			assert.Contains(t, out.String(), "round-trip verified")

			signed, err := os.ReadFile(path)
			require.NoError(t, err)
			// A splice, not a re-marshal: the signed file starts with the
			// exact original bytes (neither example recipe carries a
			// pre-existing signature key for stripTopLevelKey to remove).
			assert.True(t, bytes.HasPrefix(signed, original),
				"signed output must begin with the original file's exact bytes")

			command, err := daemon.DecodeCommandFile(path)
			require.NoError(t, err)
			assert.NotEmpty(t, command.Signature)

			// The signature this command produced must independently
			// verify through daemon.VerifySignature -- the exact function
			// beadle-daemon itself calls when loading command files.
			require.NoError(t, daemon.VerifySignature(command, gpgBin, fpr))
		})
	}
}

// TestRunSign_PreservesComments is the regression test for FIX 2 finding
// 2 (.tmp/FIXBRIEF-recipe-tooling.md): a full struct re-marshal drops
// every comment in the source file. The splice approach never re-marshals
// anything but the signature field, so an author's comment must survive
// signing verbatim.
func TestRunSign_PreservesComments(t *testing.T) {
	gpgBin := gpgBinary(t)
	home := testenv.ShortGPGHome(t)
	t.Setenv("GNUPGHOME", home)
	fpr := testenv.GenOwnerKey(t, gpgBin, home, "comments@example.com", "1y")

	const recipe = `# This file is UNSIGNED. Sign it before beadle-daemon will load it.
name: commented
runner: cli
binary: beadle-sysreport
output_schema: text
`
	path := filepath.Join(t.TempDir(), "recipe.yaml")
	require.NoError(t, os.WriteFile(path, []byte(recipe), 0o600))

	dataDir, resolver := unconfiguredDaemon(t)
	var out bytes.Buffer
	require.NoError(t, runSign(&out, dataDir, resolver, path, fpr, gpgBin, false))

	signed, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(signed), "# This file is UNSIGNED.")
}

// TestRunSign_PreservesLeadingNewlineInPrompt is the regression test for
// FIX 2 finding 3: yaml.v3 re-marshaling a decoded struct loses a
// *leading* newline in a string scalar -- a "\nAnswer." prompt would
// decode, re-marshal, and re-decode to "Answer.", silently altering text
// the daemon would execute. The splice approach never re-marshals content
// fields, so this must survive signing byte-for-byte.
func TestRunSign_PreservesLeadingNewlineInPrompt(t *testing.T) {
	gpgBin := gpgBinary(t)
	home := testenv.ShortGPGHome(t)
	t.Setenv("GNUPGHOME", home)
	fpr := testenv.GenOwnerKey(t, gpgBin, home, "leading-newline@example.com", "1y")

	const recipe = `name: leading-newline
runner: cli
binary: beadle-sysreport
output_schema: text
prompt: "\nAnswer."
`
	path := filepath.Join(t.TempDir(), "recipe.yaml")
	require.NoError(t, os.WriteFile(path, []byte(recipe), 0o600))

	dataDir, resolver := unconfiguredDaemon(t)
	var out bytes.Buffer
	require.NoError(t, runSign(&out, dataDir, resolver, path, fpr, gpgBin, false))

	command, err := daemon.DecodeCommandFile(path)
	require.NoError(t, err)
	assert.Equal(t, "\nAnswer.", command.Prompt)
}

// TestRunSign_RejectsInvalidRecipe proves sign validates shape before
// signing: an invalid recipe must never sign green and fail only later,
// at daemon startup, far from the mistake. Nothing must be written.
func TestRunSign_RejectsInvalidRecipe(t *testing.T) {
	gpgBin := gpgBinary(t)
	home := testenv.ShortGPGHome(t)
	t.Setenv("GNUPGHOME", home)
	fpr := testenv.GenOwnerKey(t, gpgBin, home, "invalid-recipe@example.com", "1y")

	// runner: cli requires binary or steps; this has neither.
	const recipe = "name: broken\nrunner: cli\noutput_schema: text\n"
	path := filepath.Join(t.TempDir(), "recipe.yaml")
	require.NoError(t, os.WriteFile(path, []byte(recipe), 0o600))

	dataDir, resolver := unconfiguredDaemon(t)
	var out bytes.Buffer
	err := runSign(&out, dataDir, resolver, path, fpr, gpgBin, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid and cannot be signed")

	unchanged, readErr := os.ReadFile(path)
	require.NoError(t, readErr)
	assert.Equal(t, recipe, string(unchanged))
	_, statErr := os.Stat(path + ".signing")
	assert.True(t, os.IsNotExist(statErr), "nothing must be written for an invalid recipe")
}

// TestRunSign_RefusesSignerMismatchWithoutForce proves sign compares
// --signer against daemon.json's authorizer key and refuses a mismatch:
// signing with any other key would only produce a file beadle-daemon
// refuses to load, discovered later and further from the mistake.
func TestRunSign_RefusesSignerMismatchWithoutForce(t *testing.T) {
	gpgBin := gpgBinary(t)
	home := testenv.ShortGPGHome(t)
	t.Setenv("GNUPGHOME", home)
	authorizerFpr := testenv.GenOwnerKey(t, gpgBin, home, "authorizer@example.com", "1y")
	otherFpr := testenv.GenOwnerKey(t, gpgBin, home, "other-signer@example.com", "1y")

	dataDir := t.TempDir()
	resolver := identity.NewResolver(t.TempDir(), dataDir, "")
	var initOut bytes.Buffer
	require.NoError(t, runInit(&initOut, dataDir, resolver, "", authorizerFpr, "gpg", false, true))

	path := filepath.Join(t.TempDir(), "sysreport.yaml")
	require.NoError(t, os.WriteFile(path, []byte(minimalCommandYAML), 0o600))

	var out bytes.Buffer
	err := runSign(&out, dataDir, resolver, path, otherFpr, gpgBin, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not match the authorizer key")
	assert.Contains(t, err.Error(), authorizerFpr)
	assert.Contains(t, err.Error(), otherFpr)

	unchanged, readErr := os.ReadFile(path)
	require.NoError(t, readErr)
	assert.Equal(t, minimalCommandYAML, string(unchanged))
}

// TestRunSign_ForceOverridesSignerMismatch is the companion happy path:
// --force signs anyway and warns, rather than refusing outright.
func TestRunSign_ForceOverridesSignerMismatch(t *testing.T) {
	gpgBin := gpgBinary(t)
	home := testenv.ShortGPGHome(t)
	t.Setenv("GNUPGHOME", home)
	authorizerFpr := testenv.GenOwnerKey(t, gpgBin, home, "authorizer2@example.com", "1y")
	otherFpr := testenv.GenOwnerKey(t, gpgBin, home, "other-signer2@example.com", "1y")

	dataDir := t.TempDir()
	resolver := identity.NewResolver(t.TempDir(), dataDir, "")
	var initOut bytes.Buffer
	require.NoError(t, runInit(&initOut, dataDir, resolver, "", authorizerFpr, "gpg", false, true))

	path := filepath.Join(t.TempDir(), "sysreport.yaml")
	require.NoError(t, os.WriteFile(path, []byte(minimalCommandYAML), 0o600))

	var out bytes.Buffer
	err := runSign(&out, dataDir, resolver, path, otherFpr, gpgBin, true)
	require.NoError(t, err)
	assert.Contains(t, out.String(), "warning:")
	assert.Contains(t, out.String(), "does not match")
	assert.Contains(t, out.String(), "round-trip verified")
}

func TestRunSign_RejectsNonFingerprintSigner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sysreport.yaml")
	require.NoError(t, os.WriteFile(path, []byte(minimalCommandYAML), 0o600))

	dataDir, resolver := unconfiguredDaemon(t)
	var out bytes.Buffer
	err := runSign(&out, dataDir, resolver, path, "sign-test@example.com", "gpg", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a full 40-hex OpenPGP fingerprint")
}

// TestRunSign_TamperedFileFailsVerification proves the artifact runSign
// produces is genuinely tamper-evident: a signed file whose body is edited
// after signing must fail daemon.VerifySignature -- the same check
// beadle-daemon's LoadCommands runs before trusting a command file. This is
// the regression guard the mission exists to protect: if signing and
// verification ever computed "canonical" differently, a tampered file
// could pass unnoticed.
func TestRunSign_TamperedFileFailsVerification(t *testing.T) {
	gpgBin := gpgBinary(t)
	home := testenv.ShortGPGHome(t)
	t.Setenv("GNUPGHOME", home)
	fpr := testenv.GenOwnerKey(t, gpgBin, home, "tamper-test@example.com", "1y")

	path := filepath.Join(t.TempDir(), "sysreport.yaml")
	require.NoError(t, os.WriteFile(path, []byte(minimalCommandYAML), 0o600))

	dataDir, resolver := unconfiguredDaemon(t)
	var out bytes.Buffer
	require.NoError(t, runSign(&out, dataDir, resolver, path, fpr, gpgBin, false))

	signed, err := os.ReadFile(path)
	require.NoError(t, err)
	tampered := bytes.Replace(signed, []byte("report disk and cpu"), []byte("report disk and ALSO run rm -rf"), 1)
	require.NotEqual(t, signed, tampered, "test fixture must actually change the body")

	require.NoError(t, os.WriteFile(path, tampered, 0o600))
	command, err := daemon.DecodeCommandFile(path)
	require.NoError(t, err)

	err = daemon.VerifySignature(command, gpgBin, fpr)
	require.Error(t, err)
	var sigErr *daemon.SignatureError
	require.ErrorAs(t, err, &sigErr)
	assert.Equal(t, daemon.ReasonInvalid, sigErr.Reason)
}

// fingerprintOf returns email's full 40-hex fingerprint from home's
// keyring. testenv.GenOwnerKey does the same lookup internally but only
// for keys it generated itself (always with an empty passphrase); this is
// the passphrase-protected companion, needed after testenv.GenKeyWithPassphrase.
func fingerprintOf(t *testing.T, gpgBin, home, email string) string {
	t.Helper()
	out, err := exec.Command(gpgBin, "--homedir", home, "--batch", "--no-tty",
		"--list-keys", "--with-colons", "--", email).Output()
	require.NoError(t, err)
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Split(line, ":")
		if len(fields) > 9 && fields[0] == "fpr" {
			return fields[9]
		}
	}
	t.Fatalf("no fingerprint found for %s", email)
	return ""
}

// TestRunSign_PassphraseProtectedKeyWithoutPassphraseIsActionable is the
// regression test for the "no passphrase given" defect: a real
// passphrase-protected signing key, with no BEADLE_GPG_PASSPHRASE set and
// nothing in the (real, unmocked) secret.Get chain, must fail with a
// message that names the credential and every place it can come from --
// not gpg's bare "No passphrase given", which tells an operator nothing
// about where to set one. The passphrase itself must never appear in the
// error.
//
// HOME is isolated (t.Setenv, a fresh t.TempDir()) so this test cannot
// read a real ~/.punt-labs/beadle/secrets/gpg-passphrase: without this, the
// moment an operator follows the remedy this very test asserts on, the
// test would fail on their machine -- booby-trapped by its own subject's
// documentation. secret.Get's file backend also creates
// ~/.punt-labs/beadle/secrets with os.MkdirAll as a side effect of being
// probed, so leaving HOME unisolated would additionally write into the
// real $HOME on every run of this test (go.md §6.4).
func TestRunSign_PassphraseProtectedKeyWithoutPassphraseIsActionable(t *testing.T) {
	gpgBin := gpgBinary(t)
	home := testenv.ShortGPGHome(t)
	t.Setenv("GNUPGHOME", home)
	t.Setenv("HOME", t.TempDir())
	const passphrase = "hunter2-do-not-log-me"
	testenv.GenKeyWithPassphrase(t, gpgBin, home, "Passphrase Test", "passphrase-test@example.com", passphrase)
	fpr := fingerprintOf(t, gpgBin, home, "passphrase-test@example.com")

	path := filepath.Join(t.TempDir(), "sysreport.yaml")
	require.NoError(t, os.WriteFile(path, []byte(minimalCommandYAML), 0o600))

	dataDir, resolver := unconfiguredDaemon(t)
	var out bytes.Buffer
	err := runSign(&out, dataDir, resolver, path, fpr, gpgBin, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no passphrase was resolved")
	assert.Contains(t, err.Error(), "gpg-passphrase")
	assert.Contains(t, err.Error(), "BEADLE_GPG_PASSPHRASE")
	assert.NotContains(t, err.Error(), passphrase, "the passphrase itself must never appear in an error message")

	// Nothing must have been written to path -- a failed sign leaves the
	// original, unsigned file exactly as it was.
	unchanged, readErr := os.ReadFile(path)
	require.NoError(t, readErr)
	assert.Equal(t, minimalCommandYAML, string(unchanged))
}

// TestRunSign_PassphraseProtectedKeyWithEnvPassphraseSucceeds is the
// companion happy path: the same passphrase-protected key signs
// successfully once BEADLE_GPG_PASSPHRASE resolves it, proving the
// credential chain runSign documents in its error message actually works.
// HOME is isolated for the same reason as the test above.
func TestRunSign_PassphraseProtectedKeyWithEnvPassphraseSucceeds(t *testing.T) {
	gpgBin := gpgBinary(t)
	home := testenv.ShortGPGHome(t)
	t.Setenv("GNUPGHOME", home)
	t.Setenv("HOME", t.TempDir())
	const passphrase = "hunter2-do-not-log-me"
	testenv.GenKeyWithPassphrase(t, gpgBin, home, "Passphrase Test", "passphrase-env@example.com", passphrase)
	fpr := fingerprintOf(t, gpgBin, home, "passphrase-env@example.com")
	t.Setenv("BEADLE_GPG_PASSPHRASE", passphrase)

	path := filepath.Join(t.TempDir(), "sysreport.yaml")
	require.NoError(t, os.WriteFile(path, []byte(minimalCommandYAML), 0o600))

	dataDir, resolver := unconfiguredDaemon(t)
	var out bytes.Buffer
	err := runSign(&out, dataDir, resolver, path, fpr, gpgBin, false)
	require.NoError(t, err)
	assert.Contains(t, out.String(), "round-trip verified")
}

// TestRunSign_MalformedDaemonJSONRefusesToSign proves checkSignerMatchesAuthorizer
// does not treat every resolveVerifySigner failure as "nothing to compare
// against" -- only an absent daemon.json is ignorable. A daemon.json that
// exists but names an ambiguous or unresolvable authorizer must refuse to
// sign, not sign happily and leave the daemon to discover the problem later.
func TestRunSign_MalformedDaemonJSONRefusesToSign(t *testing.T) {
	dataDir := t.TempDir()
	resolver := identity.NewResolver(t.TempDir(), dataDir, "")

	// Both owner_handle and owner_gpg_key_id set is the ambiguous case
	// ResolveOwnerKeyID refuses outright (config.go).
	configPath := filepath.Join(dataDir, "daemon.json")
	require.NoError(t, os.WriteFile(configPath,
		[]byte(`{"owner_handle":"someone","owner_gpg_key_id":"0123456789ABCDEF0123456789ABCDEF01234567"}`),
		0o600))

	path := filepath.Join(t.TempDir(), "sysreport.yaml")
	require.NoError(t, os.WriteFile(path, []byte(minimalCommandYAML), 0o600))

	var out bytes.Buffer
	err := runSign(&out, dataDir, resolver, path, "0123456789ABCDEF0123456789ABCDEF01234567", "gpg", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ambiguous")

	unchanged, readErr := os.ReadFile(path)
	require.NoError(t, readErr)
	assert.Equal(t, minimalCommandYAML, string(unchanged))
	_, statErr := os.Stat(path + ".signing")
	assert.True(t, os.IsNotExist(statErr), "nothing must be written when the authorizer config cannot be resolved")
}

func TestRunSign_MissingFile(t *testing.T) {
	dataDir, resolver := unconfiguredDaemon(t)
	var out bytes.Buffer
	err := runSign(&out, dataDir, resolver, filepath.Join(t.TempDir(), "nope.yaml"), "0123456789ABCDEF0123456789ABCDEF01234567", "gpg", false)
	require.Error(t, err)
}
