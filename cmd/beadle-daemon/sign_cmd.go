package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/punt-labs/beadle/internal/daemon"
	"github.com/punt-labs/beadle/internal/pgp"
	"github.com/punt-labs/beadle/internal/secret"
)

var (
	signSigner    string
	signGPGBinary string
)

// signFingerprintPattern mirrors internal/daemon/signature.go's own
// (unexported) fingerprintPattern -- requiring a full 40-hex fingerprint
// here, rather than an email or short key ID gpg would also accept for
// `-u`, means the exact value handed to `--signer` is also the exact value
// handed to VerifySignature's ownerKeyID below: one identifier, used for
// both signing and the round-trip check, with nothing to independently
// resolve or mismatch.
var signFingerprintPattern = regexp.MustCompile(`^[0-9A-Fa-f]{40}$`)

// signPassphraseCredential is the secret.Get name for the signing key's
// passphrase -- named once so runSign's resolve call and its error message
// (below) cannot drift apart on what the credential is actually called.
const signPassphraseCredential = "gpg-passphrase"

var signCmd = &cobra.Command{
	Use:   "sign <command-file.yaml>",
	Short: "Sign a command file so beadle-daemon will load it",
	Long: "Canonicalize a command file the same way beadle-daemon verifies it,\n" +
		"sign the canonical bytes with the system GPG keyring, write the\n" +
		"signature back into the file, and prove the round trip verifies\n" +
		"before leaving the signed file on disk.",
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runSign(cmd.OutOrStdout(), args[0], signSigner, signGPGBinary)
	},
}

func init() {
	signCmd.Flags().StringVar(&signSigner, "signer", "",
		"full 40-hex OpenPGP fingerprint to sign with (required)")
	signCmd.Flags().StringVar(&signGPGBinary, "gpg-binary", "gpg", "gpg binary to use")
	rootCmd.AddCommand(signCmd)
}

// runSign reads path as a daemon.Command, signs its canonical bytes
// (daemon.CanonicalCommandBytes -- the SAME function VerifySignature uses)
// with signer's key from the system GPG keyring, and writes the result
// back to path. It never writes a file whose signature does not verify:
// the signed Command is checked with daemon.VerifySignature before
// anything touches disk, so a canonicalization mismatch between signing
// and verification fails loudly here instead of shipping a command file
// that silently never loads.
func runSign(w io.Writer, path, signer, gpgBinary string) error {
	if !signFingerprintPattern.MatchString(signer) {
		return fmt.Errorf("--signer %q is not a full 40-hex OpenPGP fingerprint", signer)
	}

	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}

	var command daemon.Command
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&command); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}

	canon, err := daemon.CanonicalCommandBytes(&command)
	if err != nil {
		return fmt.Errorf("canonicalize %s: %w", path, err)
	}

	passphrase, err := secret.Get(signPassphraseCredential)
	if err != nil && !errors.Is(err, secret.ErrNotFound) {
		return fmt.Errorf("resolve signing passphrase: %w", err)
	}
	// secret.ErrNotFound leaves passphrase == "", the correct value for a
	// key with no passphrase set -- not every operator key has one.

	sig, err := pgp.DetachSignBody(gpgBinary, signer, passphrase, canon)
	if err != nil {
		return wrapSignError(path, passphrase, err)
	}
	command.Signature = string(sig)

	// Prove the round trip BEFORE writing anything: a signed Command that
	// does not verify against its own signer must never reach disk.
	if err := daemon.VerifySignature(&command, gpgBinary, signer); err != nil {
		return fmt.Errorf("signed %s but the round-trip verification failed -- refusing to write: %w", path, err)
	}

	out, err := yaml.Marshal(&command)
	if err != nil {
		return fmt.Errorf("marshal signed command: %w", err)
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}

	if _, err := fmt.Fprintf(w, "signed %s (key %s, round-trip verified)\n", path, signer); err != nil {
		return fmt.Errorf("write output: %w", err)
	}
	return nil
}

// wrapSignError wraps a pgp.DetachSignBody failure for path. When the
// resolved passphrase was empty, gpg's own error text ("No passphrase
// given") tells an operator nothing about where a passphrase would come
// from -- so this names the credential chain and the fix instead of
// leaving them to read source. Never includes passphrase itself, or even
// its length: only the fact that resolution came back empty.
func wrapSignError(path, passphrase string, cause error) error {
	if passphrase != "" {
		return fmt.Errorf("sign %s: %w", path, cause)
	}
	// The env var name below is internal/secret's "BEADLE_" + upper-snake
	// mapping of signPassphraseCredential (its mapping function is
	// unexported, and the credential name is fixed, so it is named here
	// rather than derived).
	return fmt.Errorf(
		"sign %s: %w\n\n"+
			"no passphrase was resolved for credential %q -- if this key needs one, "+
			"set it via the credential chain (OS keychain, or "+
			"~/.punt-labs/beadle/secrets/%s mode 600, or the BEADLE_GPG_PASSPHRASE "+
			"environment variable); a key with no passphrase needs none of this",
		path, cause, signPassphraseCredential, signPassphraseCredential,
	)
}
