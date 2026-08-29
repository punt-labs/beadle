// Package enable turns beadle's CLAUDE.md guidance composition on and off in a
// repo. It is the single implementation both surfaces call — the beadle-email
// CLI (enable/disable) and the MCP enable tool — so a marker written by one is
// byte-identical to a marker written by the other (§2.14). Neither surface runs
// git: the marker is a working-tree change the user commits through a PR.
package enable

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/punt-labs/beadle/internal/claudemd"
)

// ImportLine is the canonical @-import a repo CLAUDE.md carries when beadle is
// enabled. It must be byte-identical across every tool the standard governs.
const ImportLine = "@.punt-labs/beadle/CLAUDE.md"

// Progressf reports a progress line. A nil Progressf discards progress, so a
// surface with nowhere to print (the MCP tool) passes nil and the CLI passes a
// stderr writer that honors --quiet.
type Progressf func(format string, a ...any)

func (p Progressf) printf(format string, a ...any) {
	if p != nil {
		p(format, a...)
	}
}

// RepoRoot returns the top-level directory of the git repository containing the
// working directory. enable and disable write inside that root, so they refuse
// to run outside a repo rather than depositing files in an arbitrary directory.
// It never falls back to the working directory; it only reports which failure
// occurred — git ran and refused (not a repo) versus git could not run at all.
func RepoRoot() (string, error) {
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

// Enable deposits the beadle user guide into <root>/.punt-labs/beadle/, writes
// the enabled marker, and registers the import in <root>/CLAUDE.md. It is
// idempotent, so re-running upgrades in place. The whole operation holds an
// exclusive per-repo lock so a concurrent enable and disable cannot interleave
// into the §2.11-incorrect "marker without import" state; the nested CLAUDE.md
// lock inside Register is always acquired after this one, never the reverse.
func Enable(root string, progress Progressf) error {
	root, err := canonicalRoot(root)
	if err != nil {
		return err
	}
	return claudemd.WithLock(root, func() error { return enableLocked(root, progress) })
}

func enableLocked(root string, progress Progressf) error {
	dir := filepath.Join(root, ".punt-labs", "beadle")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}

	// The guide and marker are beadle-owned: enable overwrites them wholesale
	// and a torn write self-heals on the next run, so they use plain WriteFile.
	// Only the user-owned repo CLAUDE.md below goes through the atomic+flock
	// import-writer, which exists to never corrupt bytes the user authored.
	guidePath := filepath.Join(dir, "CLAUDE.md")
	if err := os.WriteFile(guidePath, claudemd.Guide, 0o644); err != nil { // #nosec G306 -- deposited public doc, not a secret
		return fmt.Errorf("writing %s: %w", guidePath, err)
	}
	progress.printf("deposited %s\n", guidePath)

	// Register the import BEFORE the marker. The marker is the enabled-iff-import
	// signal (§2.7/§2.11), so it must be the last write: if Register fails, enable
	// errors out with no marker, never leaving the repo looking enabled while the
	// import never landed.
	hostPath := filepath.Join(root, "CLAUDE.md")
	wrote, err := claudemd.Register(hostPath, ImportLine)
	if err != nil {
		return fmt.Errorf("registering import in %s: %w", hostPath, err)
	}
	if wrote {
		progress.printf("added %s to %s\n", ImportLine, hostPath)
	} else {
		progress.printf("%s already imports beadle\n", hostPath)
	}

	markerPath := filepath.Join(dir, "enabled")
	if err := os.WriteFile(markerPath, nil, 0o644); err != nil { // #nosec G306 -- committed-via-PR marker, not a secret
		return fmt.Errorf("writing %s: %w", markerPath, err)
	}

	progress.printf("beadle enabled\n")
	return nil
}

// Disable removes the import from <root>/CLAUDE.md and deletes the enabled
// marker, leaving the rest of .punt-labs/beadle/ dormant. When purge is set it
// removes the whole directory instead.
//
// The order mirrors Enable in reverse. Enable adds the import then the marker,
// so "marker present ⟹ import present" (§2.11); Disable must therefore clear the
// marker BEFORE removing the import. A partial failure then leaves at worst an
// orphan import with no marker (audit-flaggable), never a marker whose import is
// already gone — the state that would make a repo look enabled while it is not.
//
// It holds the same exclusive per-repo lock as Enable, so the two are mutually
// exclusive and a concurrent pair reaches one consistent end state.
func Disable(root string, purge bool, progress Progressf) error {
	root, err := canonicalRoot(root)
	if err != nil {
		return err
	}
	return claudemd.WithLock(root, func() error { return disableLocked(root, purge, progress) })
}

func disableLocked(root string, purge bool, progress Progressf) error {
	dir := filepath.Join(root, ".punt-labs", "beadle")

	// Clear the enabled signal first. Under --purge the whole directory (which
	// contains the marker) goes now, for the same reason.
	if purge {
		if err := os.RemoveAll(dir); err != nil {
			return fmt.Errorf("purging %s: %w", dir, err)
		}
		progress.printf("purged %s\n", dir)
	} else {
		markerPath := filepath.Join(dir, "enabled")
		if err := os.Remove(markerPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("removing %s: %w", markerPath, err)
		}
	}

	// Prune the import and, if that empties the file, remove it — both under one
	// hold of Lock B inside claudemd. The emptiness check and the removal share
	// the flock, so a concurrent registrar cannot refill the file between the
	// prune and a separate delete.
	hostPath := filepath.Join(root, "CLAUDE.md")
	pruned, removed, err := claudemd.PruneAndDiscardEmpty(hostPath, ImportLine)
	if err != nil {
		return fmt.Errorf("removing import from %s: %w", hostPath, err)
	}
	if pruned {
		progress.printf("removed %s from %s\n", ImportLine, hostPath)
	}
	if removed {
		progress.printf("removed empty %s\n", hostPath)
	}

	progress.printf("beadle disabled\n")
	return nil
}

// canonicalRoot resolves the repo root through EvalSymlinks so different
// spellings of the same repo (a symlinked parent) key the per-repo lock
// identically and the guide, marker, and CLAUDE.md paths are all built from one
// canonical root.
func canonicalRoot(root string) (string, error) {
	canon, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolving repo root %q: %w", root, err)
	}
	return canon, nil
}
