package main

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

func init() {
	rootCmd.AddCommand(enableCmd)
	rootCmd.AddCommand(disableCmd)
}

// repoRoot returns the top-level directory of the git repository containing the
// working directory. enable and disable write inside that root, so they refuse
// to run outside a repo rather than depositing files in an arbitrary directory.
// It never falls back to the working directory; it only reports which failure
// occurred — git ran and refused (not a repo) versus git could not run at all.
func repoRoot() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return "", fmt.Errorf("not in a git repository (run from inside a repo): %s",
				strings.TrimSpace(string(exit.Stderr)))
		}
		return "", fmt.Errorf("running git rev-parse (is git installed and on PATH?): %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}
