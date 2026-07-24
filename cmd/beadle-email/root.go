package main

import (
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
func repoRoot() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", fmt.Errorf("not in a git repository — run from inside a repo")
	}
	return strings.TrimSpace(string(out)), nil
}
