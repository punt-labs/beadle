package daemon

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/punt-labs/beadle/internal/pgp"
)

// SignatureReason categorizes why a command file's signature failed to
// verify.
type SignatureReason string

// SignatureReason values. GOODSIG maps to a nil error, never to one of
// these — see VerifySignature.
const (
	ReasonMissing    SignatureReason = "missing"     // no signature present
	ReasonInvalid    SignatureReason = "invalid"     // BADSIG, ERRSIG, REVKEYSIG, or an unrecognized outcome
	ReasonWrongKey   SignatureReason = "wrong-key"   // NO_PUBKEY: not signed by the owner's key
	ReasonKeyExpired SignatureReason = "key-expired" // owner key non-expiring or expired
)

// SignatureError reports why a command file's signature failed to verify.
// Reason is one of the SignatureReason constants above; Detail carries
// gpg's own status-line output or other context for logs and audit
// entries.
type SignatureError struct {
	Reason SignatureReason
	Detail string
}

func (e *SignatureError) Error() string {
	return fmt.Sprintf("command signature %s: %s", e.Reason, e.Detail)
}

// fingerprintPattern matches a full 40-hex-character OpenPGP fingerprint —
// no "0x" prefix, no internal spaces, no short or long key ID.
var fingerprintPattern = regexp.MustCompile(`^[0-9A-Fa-f]{40}$`)

// CanonicalCommandBytes returns the deterministic YAML encoding of cmd used
// as the signed payload for command-file signatures: a copy of cmd with
// Signature cleared to "". A future signing tool and VerifySignature both
// call this function, so they can never independently drift on what
// "canonical" means. yaml.v3 marshals struct fields in declaration order
// and sorts map keys, so the result is stable regardless of how the
// source file was formatted, commented, ordered, or how OutputSchema's
// map keys happened to iterate.
func CanonicalCommandBytes(cmd *Command) ([]byte, error) {
	canon := *cmd
	canon.Signature = ""
	data, err := yaml.Marshal(&canon)
	if err != nil {
		return nil, fmt.Errorf("marshal canonical command: %w", err)
	}
	return data, nil
}

// VerifySignature checks that cmd was authorized by the owner identified by
// ownerKeyID (a full OpenPGP fingerprint): it reconstructs the canonical
// bytes that were signed (cmd with Signature cleared), imports ownerKeyID
// alone into an isolated GNUPGHOME, confirms exactly one key with that
// exact fingerprint landed there, checks that key's expiry against the
// same isolated homedir, and runs gpg --verify --status-fd against
// cmd.Signature. It never reads from or writes to the caller's own GPG
// keyring, and it fails closed on every branch, including its own
// operational errors.
//
// A nil error means: the owner, and only the owner, authorized this exact
// command definition, with a key that has not expired. Any other outcome
// returns a non-nil error; a failure specific to the signature itself is a
// *SignatureError, while an operational failure (a homedir that can't be
// created, a gpg binary that can't run) is returned unwrapped.
func VerifySignature(cmd *Command, gpgBinary, ownerKeyID string) error {
	if !fingerprintPattern.MatchString(ownerKeyID) {
		return &SignatureError{
			Reason: ReasonInvalid,
			Detail: fmt.Sprintf("owner key id %q is not a full 40-hex OpenPGP fingerprint", ownerKeyID),
		}
	}
	if cmd.Signature == "" {
		return &SignatureError{Reason: ReasonMissing, Detail: "command file has no signature"}
	}

	canon, err := CanonicalCommandBytes(cmd)
	if err != nil {
		return fmt.Errorf("canonicalize command for verification: %w", err)
	}

	tmpDir, gpgHome, cleanup, err := newIsolatedKeyring()
	if err != nil {
		return fmt.Errorf("prepare isolated gpg homedir: %w", err)
	}
	defer cleanup()

	if err := importOwnerKey(gpgBinary, gpgHome, ownerKeyID); err != nil {
		return err
	}

	if err := pgp.CheckKeyExpiry(gpgBinary, ownerKeyID, pgp.Homedir(gpgHome)); err != nil {
		return &SignatureError{Reason: ReasonKeyExpired, Detail: err.Error()}
	}

	return verifyDetachedSignature(gpgBinary, gpgHome, tmpDir, canon, cmd.Signature)
}

// newIsolatedKeyring creates a fresh GNUPGHOME under /tmp (short enough to
// stay under gpg-agent's 108-byte Unix-socket path limit, matching
// internal/pgp/verify.go's pattern) and returns its scratch directory, the
// homedir itself, and a cleanup func that removes both.
func newIsolatedKeyring() (tmpDir, gpgHome string, cleanup func(), err error) {
	tmpDir, err = os.MkdirTemp("/tmp", "bg-*")
	if err != nil {
		return "", "", nil, fmt.Errorf("create temp dir: %w", err)
	}
	gpgHome = filepath.Join(tmpDir, "gnupg")
	if err = os.Mkdir(gpgHome, 0o700); err != nil {
		_ = os.RemoveAll(tmpDir)
		return "", "", nil, fmt.Errorf("create gpg home: %w", err)
	}
	return tmpDir, gpgHome, func() { _ = os.RemoveAll(tmpDir) }, nil
}

// importOwnerKey exports ownerKeyID alone from the ambient keyring — a
// one-way, read-only bridge, mirroring internal/pgp/verify.go's exportAll
// comment — and imports it into gpgHome. It then asserts exactly one key
// with that exact fingerprint landed there: never a silent "verify against
// whatever's there."
func importOwnerKey(gpgBinary, gpgHome, ownerKeyID string) error {
	export := exec.Command(gpgBinary, "--batch", "--no-tty", "--export", "--", ownerKeyID)
	var keyData, exportErr bytes.Buffer
	export.Stdout = &keyData
	export.Stderr = &exportErr
	if err := export.Run(); err != nil {
		return fmt.Errorf("export owner key %s: %w: %s", ownerKeyID, err, exportErr.String())
	}

	if keyData.Len() > 0 {
		importCmd := exec.Command(gpgBinary, "--homedir", gpgHome, "--batch", "--no-tty", "--import")
		importCmd.Stdin = &keyData
		var importErr bytes.Buffer
		importCmd.Stderr = &importErr
		if err := importCmd.Run(); err != nil {
			return fmt.Errorf("import owner key %s into isolated keyring: %w: %s", ownerKeyID, err, importErr.String())
		}
	}

	return assertSingleOwnerKey(gpgBinary, gpgHome, ownerKeyID)
}

// assertSingleOwnerKey lists keys in gpgHome filtered to ownerKeyID and
// requires exactly one pub record whose paired fpr record equals
// ownerKeyID. Zero or more than one match is a *SignatureError.
func assertSingleOwnerKey(gpgBinary, gpgHome, ownerKeyID string) error {
	cmd := exec.Command(gpgBinary, "--homedir", gpgHome, "--batch", "--no-tty",
		"--list-keys", "--with-colons", "--", ownerKeyID)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	// gpg --list-keys exits non-zero when ownerKeyID legitimately isn't in
	// the keyring -- an expected outcome that countOwnerKeyMatches below
	// turns into "zero matches." Only a process that never started at all
	// (binary missing or not executable) is an operational failure.
	var execErr *exec.Error
	if err := cmd.Run(); errors.As(err, &execErr) {
		return fmt.Errorf("run gpg list-keys for owner key %s: %w", ownerKeyID, err)
	}

	switch n := countOwnerKeyMatches(stdout.String(), ownerKeyID); {
	case n == 0:
		return &SignatureError{
			Reason: ReasonInvalid,
			Detail: fmt.Sprintf("owner key %s not found in isolated keyring after import", ownerKeyID),
		}
	case n > 1:
		return &SignatureError{
			Reason: ReasonInvalid,
			Detail: fmt.Sprintf("owner key %s is ambiguous: %d matching keys imported", ownerKeyID, n),
		}
	default:
		return nil
	}
}

// countOwnerKeyMatches counts pub records in gpg --with-colons output whose
// paired fpr record (the line immediately following) equals ownerKeyID,
// case-insensitively.
func countOwnerKeyMatches(output, ownerKeyID string) int {
	matches := 0
	lines := strings.Split(output, "\n")
	for i, line := range lines {
		fields := strings.Split(line, ":")
		if len(fields) == 0 || fields[0] != "pub" {
			continue
		}
		if i+1 >= len(lines) {
			continue
		}
		fprFields := strings.Split(lines[i+1], ":")
		if len(fprFields) < 10 || fprFields[0] != "fpr" {
			continue
		}
		if strings.EqualFold(fprFields[9], ownerKeyID) {
			matches++
		}
	}
	return matches
}

// verifyDetachedSignature writes canon and armoredSig to scratch files
// under tmpDir and runs gpg --verify against them in gpgHome, classifying
// the result from gpg's machine-readable status-fd output.
func verifyDetachedSignature(gpgBinary, gpgHome, tmpDir string, canon []byte, armoredSig string) error {
	dataFile := filepath.Join(tmpDir, "command.canonical")
	if err := os.WriteFile(dataFile, canon, 0o600); err != nil {
		return fmt.Errorf("write canonical command bytes: %w", err)
	}

	sigFile := filepath.Join(tmpDir, "signature.asc")
	if err := os.WriteFile(sigFile, []byte(armoredSig), 0o600); err != nil {
		return fmt.Errorf("write signature: %w", err)
	}

	cmd := exec.Command(gpgBinary,
		"--homedir", gpgHome, "--batch", "--no-tty",
		"--status-fd", "1",
		"--verify", sigFile, dataFile,
	)
	cmd.Env = withCLocale(os.Environ())
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	// gpg --verify exits non-zero for a bad signature; that outcome is
	// expected and classifyStatusLines reads it from stdout regardless of
	// exit code. But if gpg itself never started (binary missing or not
	// executable), Run returns an *exec.Error before any status-fd output
	// exists — that is an operational failure, not a signature verdict.
	var execErr *exec.Error
	if err := cmd.Run(); errors.As(err, &execErr) {
		return fmt.Errorf("run gpg verify: %w", err)
	}

	return classifyStatusLines(stdout.String(), stderr.String())
}

// withCLocale returns env with LC_ALL pinned to "C", replacing any existing
// value. Pinned as defense in depth even though --status-fd output does
// not itself vary by locale.
func withCLocale(env []string) []string {
	out := make([]string, 0, len(env)+1)
	for _, e := range env {
		if strings.HasPrefix(e, "LC_ALL=") {
			continue
		}
		out = append(out, e)
	}
	return append(out, "LC_ALL=C")
}

// classifyStatusLines maps gpg's [GNUPG:]-prefixed status-fd output to a
// verification outcome. NO_PUBKEY takes precedence over BADSIG/ERRSIG/
// REVKEYSIG: gpg emits ERRSIG alongside NO_PUBKEY when the signer's key is
// missing, and "missing key" is the more specific diagnosis of the two.
// Every branch except GOODSIG constructs a non-nil *SignatureError,
// including the default case for a status line this switch does not
// recognize — there is no implicit fallthrough to nil. stderr is gpg's own
// diagnostic output; it is folded into the default arm's detail only, since
// that is the one outcome where a human trying to diagnose "unrecognized
// gpg verification outcome" from an audit-log entry alone would want it.
func classifyStatusLines(output, stderr string) error {
	var noPubkeyLine, invalidLine, goodLine string

	for _, line := range strings.Split(output, "\n") {
		rest, ok := strings.CutPrefix(line, "[GNUPG:] ")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			continue
		}
		switch fields[0] {
		case "GOODSIG":
			goodLine = line
		case "NO_PUBKEY":
			noPubkeyLine = line
		case "BADSIG", "ERRSIG", "REVKEYSIG":
			if invalidLine == "" {
				invalidLine = line
			}
		}
	}

	switch {
	case noPubkeyLine != "":
		return &SignatureError{Reason: ReasonWrongKey, Detail: noPubkeyLine}
	case goodLine != "":
		return nil
	case invalidLine != "":
		return &SignatureError{Reason: ReasonInvalid, Detail: invalidLine}
	default:
		detail := fmt.Sprintf("unrecognized gpg verification outcome: %q", strings.TrimSpace(output))
		if s := strings.TrimSpace(stderr); s != "" {
			detail += fmt.Sprintf("; gpg stderr: %q", s)
		}
		return &SignatureError{Reason: ReasonInvalid, Detail: detail}
	}
}
