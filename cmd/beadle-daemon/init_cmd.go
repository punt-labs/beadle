package main

import (
	"encoding/json"
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
	initHandle      string
	initFingerprint string
	initForce       bool
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Write daemon.json, naming the key that authorizes command files",
	Long: "Write ~/.punt-labs/beadle/daemon.json, naming the single GPG key\n" +
		"beadle-daemon trusts to authorize command files (see `sign`). Per the\n" +
		"zero-agent-authority invariant, no command loads until this exists.",
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		dataDir, err := paths.DataDir()
		if err != nil {
			return fmt.Errorf("resolve data dir: %w", err)
		}
		resolver, err := newResolver()
		if err != nil {
			return fmt.Errorf("create resolver: %w", err)
		}
		return runInit(cmd.OutOrStdout(), dataDir, resolver, initHandle, initFingerprint, initForce)
	},
}

func init() {
	initCmd.Flags().StringVar(&initHandle, "handle", "",
		"ethos handle whose beadle extension names the authorizer's gpg_key_id")
	initCmd.Flags().StringVar(&initFingerprint, "fingerprint", "",
		"a literal 40-hex OpenPGP fingerprint naming the authorizer directly")
	initCmd.Flags().BoolVar(&initForce, "force", false, "overwrite an existing daemon.json")
	rootCmd.AddCommand(initCmd)
}

// runInit validates that exactly one of handle or fingerprint resolves to a
// usable authorizer fingerprint, then writes dataDir/daemon.json naming it.
// It refuses to overwrite an existing file unless force is set, and writes
// nothing at all if the authorizer config does not resolve — a daemon.json
// that names an unresolvable owner is worse than no daemon.json, since
// resolveDaemonOwnerKeyID (main.go) treats both as "command loading
// disabled" but only the latter is the ordinary, silent case.
func runInit(w io.Writer, dataDir string, resolver *identity.Resolver, handle, fingerprint string, force bool) error {
	if (handle == "") == (fingerprint == "") {
		return fmt.Errorf("specify exactly one of --handle or --fingerprint")
	}

	cfg := &daemon.Config{OwnerHandle: handle, OwnerGPGKeyID: fingerprint}
	resolvedFpr, err := cfg.ResolveOwnerKeyID(resolver)
	if err != nil {
		return fmt.Errorf("validate authorizer config: %w", err)
	}

	configPath := filepath.Join(dataDir, "daemon.json")
	if !force {
		if _, statErr := os.Stat(configPath); statErr == nil {
			return fmt.Errorf("%s already exists; pass --force to overwrite", configPath)
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return fmt.Errorf("stat %s: %w", configPath, statErr)
		}
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal daemon config: %w", err)
	}
	data = append(data, '\n')

	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", dataDir, err)
	}
	if err := os.WriteFile(configPath, data, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", configPath, err)
	}

	if _, err := fmt.Fprintf(w, "wrote %s (authorizer key %s)\n", configPath, resolvedFpr); err != nil {
		return fmt.Errorf("write output: %w", err)
	}
	return nil
}
