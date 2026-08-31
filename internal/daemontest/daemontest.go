// Package daemontest provides fixtures for testing internal/daemon's
// mail-triggered pipeline: a gate-3 command-signing helper, a gate-4 ethos
// presence check, PGP message builders, and a fake worker spawner.
//
// It is a separate package from internal/testenv because internal/daemon's
// own white-box tests (package daemon) already import internal/testenv --
// a helper in testenv that itself imported internal/daemon would close
// that into an import cycle ("import cycle not allowed in test"), confirmed
// by trial build. The same constraint means package daemon's own
// _test.go files cannot import this package either (it imports daemon,
// same cycle) -- this package exists specifically for
// internal/daemon/harness_test.go, which must therefore be an external
// test package (`package daemon_test`), not the white-box convention every
// other file in internal/daemon uses. Everything here depends only on
// internal/daemon's exported surface, so a standalone package costs
// nothing and matches the precedent internal/testserver and
// internal/testenv already set: general-purpose test fixtures live in
// their own importable package, not inside the package under test.
package daemontest

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/punt-labs/beadle/internal/daemon"
	"github.com/punt-labs/beadle/internal/testenv"
)

// EthosVersion is the pinned ethos CLI version this test tier requires --
// kept in sync by hand with Makefile's ETHOS_VERSION (see
// docs/daemon-test-harness.md's "Pinning ethos"). It appears only in
// EthosOrFatal's human-facing failure message, never in an assertion.
const EthosVersion = "v4.16.0"

// EthosOrFatal returns the ethos binary's path, or fails t naming the
// install remedy. Per this test tier's hard constraint, a missing
// dependency is a test failure naming what to install, never a t.Skip --
// exactly the failure mode that let beadle-8gt's regression coverage skip
// silently in CI (docs/daemon-test-harness.md). `make test`/`make check`
// provision ethos via the tools-ethos Makefile target, so this should only
// ever fire on a developer machine that bypassed make.
func EthosOrFatal(t testing.TB) string {
	t.Helper()
	path, err := exec.LookPath("ethos")
	if err != nil {
		t.Fatalf("ethos not found on PATH: install it via `go install github.com/punt-labs/ethos/v4/cmd/ethos@%s` (or run `make tools-ethos`): %v", EthosVersion, err)
	}
	return path
}

// SignCommand detach-signs cmd's canonical bytes (daemon.CanonicalCommandBytes)
// using signerEmail's key in the gpg homedir home, and returns a copy of cmd
// with Signature set to the resulting armored signature. It never mutates
// cmd. home must already hold signerEmail's keypair (see
// testenv.GenKey/GenKeyNoExpiry).
func SignCommand(t testing.TB, gpgBin, home, signerEmail string, cmd *daemon.Command) *daemon.Command {
	t.Helper()

	canon, err := daemon.CanonicalCommandBytes(cmd)
	require.NoError(t, err)

	sigCmd := exec.Command(gpgBin, "--homedir", home, "--batch", "--no-tty",
		"--pinentry-mode", "loopback", "--passphrase", "",
		"--detach-sign", "--armor", "-u", signerEmail)
	sigCmd.Stdin = bytes.NewReader(canon)
	var stdout, stderr bytes.Buffer
	sigCmd.Stdout = &stdout
	sigCmd.Stderr = &stderr
	require.NoError(t, sigCmd.Run(), "gpg detach-sign failed: %s", stderr.String())

	signed := *cmd
	signed.Signature = stdout.String()
	return &signed
}

// FakeSpawnerCall records one FakeSpawner.Run invocation.
type FakeSpawnerCall struct {
	MissionID        string
	MCPConfigPath    string
	SystemPromptPath string
	EnvOverrides     map[string]string
}

// FakeSpawner implements daemon.Spawner without spawning a real Claude Code
// subprocess -- the one seam a daemon pipeline test must fake, since a real
// spawn is unbounded in time, cost, and non-determinism. It records every
// call so a test can assert the pipeline reached (or never reached) the
// worker-spawn step, and returns Result (or Err, if set) as the canned
// outcome.
type FakeSpawner struct {
	mu     sync.Mutex
	calls  []FakeSpawnerCall
	Result daemon.WorkerResult
	Err    error
}

// Run implements daemon.Spawner. It is safe for concurrent use, since
// OnNewMail spawns each pipeline in its own goroutine.
func (s *FakeSpawner) Run(_ context.Context, missionID, mcpConfigPath, systemPromptPath string, envOverrides map[string]string) (daemon.WorkerResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.calls = append(s.calls, FakeSpawnerCall{
		MissionID:        missionID,
		MCPConfigPath:    mcpConfigPath,
		SystemPromptPath: systemPromptPath,
		EnvOverrides:     envOverrides,
	})
	if s.Err != nil {
		return daemon.WorkerResult{}, s.Err
	}
	result := s.Result
	result.MissionID = missionID
	return result, nil
}

// Calls returns a copy of every call recorded so far, safe to read
// concurrently with Run.
func (s *FakeSpawner) Calls() []FakeSpawnerCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]FakeSpawnerCall(nil), s.calls...)
}

// GenOwnerKey generates an ephemeral keypair in home for email and returns
// its full 40-hex fingerprint, the shape daemon.VerifySignature's
// ownerKeyID parameter requires. expire is a gpg
// --quick-generate-key expiration spec, e.g. "1y" or "0" for non-expiring.
func GenOwnerKey(t testing.TB, gpgBin, home, email, expire string) string {
	t.Helper()
	cmd := exec.Command(gpgBin, "--homedir", home, "--batch", "--no-tty",
		"--pinentry-mode", "loopback", "--passphrase", "",
		"--quick-generate-key", fmt.Sprintf("Test <%s>", email), "default", "default", expire)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	require.NoError(t, cmd.Run(), "key generation failed for %s: %s", email, stderr.String())

	listCmd := exec.Command(gpgBin, "--homedir", home, "--batch", "--no-tty",
		"--list-keys", "--with-colons", "--", email)
	var stdout bytes.Buffer
	listCmd.Stdout = &stdout
	require.NoError(t, listCmd.Run())
	for _, line := range strings.Split(stdout.String(), "\n") {
		fields := strings.Split(line, ":")
		if len(fields) > 9 && fields[0] == "fpr" {
			return fields[9]
		}
	}
	t.Fatalf("no fingerprint found for %s", email)
	return ""
}

// BuildPGPSignedMessage returns a real PGP-signed multipart/signed RFC822
// message (RFC 3156) with the signing key attached, generating a fresh
// ephemeral GPG keypair for from. It returns both the raw bytes and the
// signing key's short key ID.
//
// This intentionally parallels internal/daemon/handler_test.go's unexported
// buildPGPSignedMessage rather than sharing code with it: an external
// daemon_test package cannot reach an unexported symbol in package daemon's
// own test files, and this package cannot import package daemon's test
// files either (only a normal import of the daemon package itself, which
// never pulls in _test.go content) without recreating the same import
// cycle documented in this file's package comment.
func BuildPGPSignedMessage(t testing.TB, gpgBin, from, subject, body string) (raw []byte, keyID string) {
	t.Helper()

	home := testenv.ShortGPGHome(t)
	base := []string{"--homedir", home, "--batch", "--no-tty"}

	testenv.GenKey(t, gpgBin, home, "Test Signer", from)

	listCmd := exec.Command(gpgBin, append(base, "--list-keys", "--with-colons", from)...)
	var listBuf bytes.Buffer
	listCmd.Stdout = &listBuf
	require.NoError(t, listCmd.Run())
	for _, line := range bytes.Split(listBuf.Bytes(), []byte("\n")) {
		parts := bytes.Split(line, []byte(":"))
		if len(parts) >= 5 && string(parts[0]) == "pub" {
			keyID = string(parts[4])
			break
		}
	}
	require.NotEmpty(t, keyID, "failed to extract key ID")

	bodyPart := "Content-Type: text/plain; charset=utf-8\r\n" +
		"Content-Transfer-Encoding: 7bit\r\n" +
		"\r\n" + body + "\r\n"

	signCmd := exec.Command(gpgBin, append(base, "--detach-sign", "--armor", "-u", from)...)
	signCmd.Stdin = bytes.NewBufferString(bodyPart)
	var sigBuf bytes.Buffer
	signCmd.Stdout = &sigBuf
	require.NoError(t, signCmd.Run())

	exportCmd := exec.Command(gpgBin, append(base, "--export", "--armor", from)...)
	var keyBuf bytes.Buffer
	exportCmd.Stdout = &keyBuf
	require.NoError(t, exportCmd.Run())

	boundary := "DaemonTestBoundary12345"
	var msg bytes.Buffer
	fmt.Fprintf(&msg, "From: %s\r\n", from)
	fmt.Fprintf(&msg, "To: test@test.com\r\n")
	fmt.Fprintf(&msg, "Subject: %s\r\n", subject)
	fmt.Fprintf(&msg, "Date: %s\r\n", time.Now().Format(time.RFC1123Z))
	fmt.Fprintf(&msg, "Message-ID: <%d@test>\r\n", time.Now().UnixNano())
	fmt.Fprintf(&msg, "MIME-Version: 1.0\r\n")
	fmt.Fprintf(&msg, "Content-Type: multipart/signed; boundary=%s; micalg=pgp-sha256; protocol=\"application/pgp-signature\"\r\n", boundary)
	fmt.Fprintf(&msg, "\r\n--%s\r\n", boundary)
	msg.WriteString(bodyPart)
	fmt.Fprintf(&msg, "\r\n--%s\r\n", boundary)
	fmt.Fprintf(&msg, "Content-Type: application/pgp-signature; name=\"signature.asc\"\r\n\r\n")
	msg.Write(bytes.TrimSpace(sigBuf.Bytes()))
	fmt.Fprintf(&msg, "\r\n--%s\r\n", boundary)
	fmt.Fprintf(&msg, "Content-Type: application/pgp-keys\r\n\r\n")
	msg.Write(bytes.TrimSpace(keyBuf.Bytes()))
	fmt.Fprintf(&msg, "\r\n--%s--\r\n", boundary)

	return msg.Bytes(), keyID
}

// BuildTrustedMessage returns raw RFC822 bytes carrying Proton E2E trust
// headers but no PGP signature -- SMTP-injectable, so never sufficient on
// its own for x-bit execution (see internal/daemon/handler.go's
// verifyTrust).
func BuildTrustedMessage(from, subject, body string) []byte {
	return []byte(fmt.Sprintf(
		"From: %s\r\nSubject: %s\r\nDate: %s\r\nMessage-ID: <%d@test>\r\n"+
			"X-Pm-Content-Encryption: end-to-end\r\nX-Pm-Origin: internal\r\n"+
			"Content-Type: text/plain\r\n\r\n%s",
		from, subject, time.Now().Format(time.RFC1123Z), time.Now().UnixNano(), body,
	))
}
