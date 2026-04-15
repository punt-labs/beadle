package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// EmailMeta holds the metadata extracted from an email that triggered a mission.
type EmailMeta struct {
	MessageID string
	From      string
	Subject   string
}

// BuildContract generates a mission contract YAML string from email metadata.
// Email provenance is recorded in inputs.trigger (structured metadata for audit).
// The subject is in inputs.trigger, NOT in success_criteria — success_criteria
// uses fixed text that directs the worker to read the email via beadle-email tools.
// All user-controlled values are double-quoted via escapeYAMLValue.
func BuildContract(meta EmailMeta) string {
	return fmt.Sprintf(`leader: claude
worker: bwk
evaluator:
  handle: mdm
inputs:
  trigger:
    type: email
    message_id: %s
    from: %s
    subject: %s
  files: []
write_set:
  - daemon output
success_criteria:
  - "Complete the task described in the triggering email. Read the email via beadle-email tools using the message ID in inputs.trigger."
budget:
  rounds: 1
  reflection_after_each: false
`, escapeYAMLValue(meta.MessageID),
		escapeYAMLValue(meta.From),
		escapeYAMLValue(meta.Subject))
}

// escapeYAMLValue returns a double-quoted YAML scalar with proper escaping.
// Always quotes to avoid type ambiguity (bare "99" parses as integer,
// bare "true" parses as boolean).
// NUL bytes are stripped and the value is capped at 500 characters to
// limit adversarial input from email subjects.
func escapeYAMLValue(s string) string {
	s = strings.ReplaceAll(s, "\x00", "")
	if utf8.RuneCountInString(s) > 500 {
		runes := []rune(s)
		s = string(runes[:500])
	}
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

	if err := os.MkdirAll(c.TmpDir, 0o700); err != nil {
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
	// Parse the "created: m-... worker=... evaluator=..." line for the ID.
	// Fallback: if no "created:" line, use the last non-empty line trimmed.
	missionID := ""
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "created: ") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				missionID = parts[1]
			}
			break
		}
	}
	if missionID == "" {
		// Fallback: last non-empty line (handles plain ID output).
		for _, line := range strings.Split(string(out), "\n") {
			line = strings.TrimSpace(line)
			if line != "" && !strings.HasPrefix(line, "ethos:") {
				missionID = line
			}
		}
	}
	if missionID == "" {
		return "", fmt.Errorf("ethos mission create: no mission ID in output: %s", strings.TrimSpace(string(out)))
	}
	if !ValidMissionID(missionID) {
		return "", fmt.Errorf("ethos mission create: invalid mission ID %q in output", missionID)
	}
	return missionID, nil
}
