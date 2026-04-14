package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// EmailMeta holds the metadata extracted from an email that triggered a mission.
type EmailMeta struct {
	MessageID string
	From      string
	Subject   string
}

// BuildContract generates a mission contract YAML string from email metadata.
// User-controlled fields (message_id, from, subject) are always double-quoted
// via escapeYAMLValue to prevent type ambiguity and YAML injection.
// Template literals (leader, worker, etc.) are safe unquoted.
func BuildContract(meta EmailMeta) string {
	// YAML is simple enough to template directly.
	// Using fmt.Sprintf avoids a yaml library dependency for a fixed structure.
	// inputs.trigger is not yet in ethos schema (beadle-40k).
	// Email provenance is recorded in the ticket reference and daemon log.
	// Worker/evaluator must be valid ethos identities with distinct roles.
	return fmt.Sprintf(`leader: claude
worker: bwk
evaluator:
  handle: mdm
inputs:
  ticket: %s
  files: []
write_set:
  - daemon output
success_criteria:
  - %s
budget:
  rounds: 1
  reflection_after_each: false
`, escapeYAMLValue("email:"+meta.MessageID+":"+meta.From),
		escapeYAMLValue(meta.Subject))
}

// escapeYAMLValue returns a double-quoted YAML scalar with proper escaping.
// Always quotes to avoid type ambiguity (bare "99" parses as integer,
// bare "true" parses as boolean).
func escapeYAMLValue(s string) string {
	escaped := strings.ReplaceAll(s, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `"`, `\"`)
	escaped = strings.ReplaceAll(escaped, "\n", `\n`)
	escaped = strings.ReplaceAll(escaped, "\r", `\r`)
	escaped = strings.ReplaceAll(escaped, "\t", `\t`)
	return `"` + escaped + `"`
}

// EthosMissionCreator creates missions by writing a contract YAML to a temp
// file and invoking `ethos mission create`.
type EthosMissionCreator struct {
	TmpDir string // directory for temp contract files
}

// Create writes the contract to a temp file and calls ethos mission create.
// Returns the mission ID parsed from stdout.
func (c *EthosMissionCreator) Create(meta EmailMeta) (string, error) {
	contract := BuildContract(meta)

	if err := os.MkdirAll(c.TmpDir, 0o750); err != nil {
		return "", fmt.Errorf("create tmp dir %s: %w", c.TmpDir, err)
	}

	f, err := os.CreateTemp(c.TmpDir, "mission-*.yaml")
	if err != nil {
		return "", fmt.Errorf("create temp contract file: %w", err)
	}
	tmpPath := f.Name()
	defer os.Remove(tmpPath) // clean up temp file regardless of outcome

	if _, err := f.WriteString(contract); err != nil {
		f.Close()
		return "", fmt.Errorf("write contract to %s: %w", tmpPath, err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("close contract file %s: %w", tmpPath, err)
	}

	absPath, err := filepath.Abs(tmpPath)
	if err != nil {
		os.Remove(tmpPath)
		return "", fmt.Errorf("resolve absolute path for %s: %w", tmpPath, err)
	}

	out, err := exec.Command("ethos", "mission", "create", "--file", absPath).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("ethos mission create: %w: %s", err, strings.TrimSpace(string(out)))
	}

	// ethos may print deprecation warnings before the "created:" line.
	// Parse only the line containing the mission ID.
	missionID := ""
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "created: ") {
			// "created: m-2026-04-14-010 worker=bwk evaluator=mdm"
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				missionID = parts[1]
			}
			break
		}
	}
	if missionID == "" {
		return "", fmt.Errorf("ethos mission create: no mission ID in output: %s", strings.TrimSpace(string(out)))
	}
	return missionID, nil
}
