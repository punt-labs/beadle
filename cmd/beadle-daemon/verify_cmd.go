package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/punt-labs/beadle/internal/daemon"
	"github.com/punt-labs/beadle/internal/identity"
	"github.com/punt-labs/beadle/internal/paths"
)

var (
	verifySigner    string
	verifyGPGBinary string
)

var verifyCmd = &cobra.Command{
	Use:   "verify <command-file.yaml>",
	Short: "Check a command file's signature, the same way beadle-daemon would",
	Long: "Verify a command file's GPG signature against the authorizer key\n" +
		"named in daemon.json -- the exact key beadle-daemon itself trusts\n" +
		"when it loads command files -- or against an explicit --signer\n" +
		"fingerprint. Reports the outcome (good, missing, wrong-key,\n" +
		"key-expired, invalid) and exits non-zero on anything but good.",
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runVerify(cmd.OutOrStdout(), args[0], verifySigner, verifyGPGBinary)
	},
}

func init() {
	verifyCmd.Flags().StringVar(&verifySigner, "signer", "",
		"full 40-hex OpenPGP fingerprint to verify against (default: resolve from daemon.json, the same key beadle-daemon itself trusts)")
	verifyCmd.Flags().StringVar(&verifyGPGBinary, "gpg-binary", "gpg", "gpg binary to use")
	rootCmd.AddCommand(verifyCmd)
}

// runVerify checks path's signature against signer, resolving signer from
// daemon.json (the exact key beadle-daemon's own loadCommand trusts) when
// the caller passed none. It reports the outcome and returns an error for
// anything but a good signature, so an operator asking "why isn't my
// recipe loading" gets an answer without starting the daemon and reading
// logs.
func runVerify(w io.Writer, path, signer, gpgBinary string) error {
	if signer != "" && !signFingerprintPattern.MatchString(signer) {
		return fmt.Errorf("--signer %q is not a full 40-hex OpenPGP fingerprint", signer)
	}

	if signer == "" {
		dataDir, err := paths.DataDir()
		if err != nil {
			return fmt.Errorf("resolve data dir: %w", err)
		}
		resolver, err := newResolver()
		if err != nil {
			return fmt.Errorf("create resolver: %w", err)
		}
		signer, err = resolveVerifySigner(dataDir, resolver)
		if err != nil {
			return err
		}
	}

	command, err := daemon.DecodeCommandFile(path)
	if err != nil {
		return err
	}

	verErr := daemon.VerifySignature(command, gpgBinary, signer)
	if verErr == nil {
		if _, err := fmt.Fprintf(w, "%s: good (key %s)\n", path, signer); err != nil {
			return fmt.Errorf("write output: %w", err)
		}
		return nil
	}

	var sigErr *daemon.SignatureError
	if errors.As(verErr, &sigErr) {
		// %w, not %s, so a caller of runVerify can recover the
		// *SignatureError via errors.As from the returned error -- the
		// two lines below this used to format sigErr into plain text and
		// discard the chain, leaving errors.As unable to recover it.
		return fmt.Errorf("%s: %w", path, sigErr)
	}
	// An operational failure (gpg couldn't run, homedir couldn't be
	// created) is not a signature verdict at all -- say so distinctly
	// rather than reporting it as if VerifySignature had reached one.
	return fmt.Errorf("%s: could not verify: %w", path, verErr)
}

// resolveVerifySigner reads dataDir/daemon.json and resolves its
// owner_handle/owner_gpg_key_id the same way beadle-daemon's own
// resolveDaemonOwnerKeyID does (main.go), but as a plain CLI error instead
// of a logged, fail-silent-on-absence outcome -- verify has no reason to
// stay quiet about a daemon.json that isn't there, the way the daemon's
// own startup path does for its "ordinary, unconfigured" case.
func resolveVerifySigner(dataDir string, resolver *identity.Resolver) (string, error) {
	configPath := filepath.Join(dataDir, "daemon.json")
	cfg, err := daemon.LoadConfig(configPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("no daemon.json at %s -- run `beadle-daemon init` first, or pass --signer explicitly", configPath)
		}
		return "", fmt.Errorf("read %s: %w", configPath, err)
	}
	fpr, err := cfg.ResolveOwnerKeyID(resolver)
	if err != nil {
		return "", fmt.Errorf("resolve authorizer key from %s: %w", configPath, err)
	}
	return fpr, nil
}
