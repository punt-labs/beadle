package daemon

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/punt-labs/beadle/internal/testenv"
	"github.com/punt-labs/beadle/internal/testserver"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// mockMissionCreator records Create calls for test assertions.
type mockMissionCreator struct {
	calls []EmailMeta
}

func (m *mockMissionCreator) Create(meta EmailMeta) (string, error) {
	m.calls = append(m.calls, meta)
	return "m-test-" + meta.MessageID, nil
}

func TestOnNewMail(t *testing.T) {
	gpgBin, err := exec.LookPath("gpg")
	if err != nil {
		t.Skip("gpg not installed")
	}

	tests := []struct {
		name         string
		messages     []testMsg
		contacts     []testContact
		wantMissions int
		wantSubjects []string
	}{
		{
			name: "pgp-verified rwx sender creates mission",
			messages: []testMsg{
				{from: "jim@punt-labs.com", subject: "Schedule meeting", signed: true},
			},
			contacts: []testContact{
				{name: "Jim", addr: "jim@punt-labs.com", perm: "rwx"},
			},
			wantMissions: 1,
			wantSubjects: []string{"Schedule meeting"},
		},
		{
			name: "proton-only trusted rwx sender rejected without PGP",
			messages: []testMsg{
				{from: "jim@punt-labs.com", subject: "Schedule meeting", trusted: true},
			},
			contacts: []testContact{
				{name: "Jim", addr: "jim@punt-labs.com", perm: "rwx"},
			},
			wantMissions: 0,
		},
		{
			name: "unverified rwx sender rejected",
			messages: []testMsg{
				{from: "jim@punt-labs.com", subject: "Schedule meeting"},
			},
			contacts: []testContact{
				{name: "Jim", addr: "jim@punt-labs.com", perm: "rwx"},
			},
			wantMissions: 0,
		},
		{
			name: "rw- sender skipped even with PGP",
			messages: []testMsg{
				{from: "bob@example.com", subject: "Hello", signed: true},
			},
			contacts: []testContact{
				{name: "Bob", addr: "bob@example.com", perm: "rw-"},
			},
			wantMissions: 0,
		},
		{
			name: "unknown sender skipped",
			messages: []testMsg{
				{from: "stranger@example.com", subject: "Spam", signed: true},
			},
			contacts: []testContact{
				{name: "Jim", addr: "jim@punt-labs.com", perm: "rwx"},
			},
			wantMissions: 0,
		},
		{
			name: "mixed: one pgp rwx, one rw-, one unknown",
			messages: []testMsg{
				{from: "jim@punt-labs.com", subject: "Do this", signed: true},
				{from: "bob@example.com", subject: "Read this", signed: true},
				{from: "stranger@example.com", subject: "Buy now", signed: true},
			},
			contacts: []testContact{
				{name: "Jim", addr: "jim@punt-labs.com", perm: "rwx"},
				{name: "Bob", addr: "bob@example.com", perm: "rw-"},
			},
			wantMissions: 1,
			wantSubjects: []string{"Do this"},
		},
		{
			name: "pgp-verified r-x sender creates mission",
			messages: []testMsg{
				{from: "ops@example.com", subject: "Deploy", signed: true},
			},
			contacts: []testContact{
				{name: "Ops", addr: "ops@example.com", perm: "r-x"},
			},
			wantMissions: 1,
			wantSubjects: []string{"Deploy"},
		},
		{
			name:         "no messages",
			messages:     nil,
			contacts:     nil,
			wantMissions: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := testenv.New(t, "test@test.com")
			fix := testserver.NewFixture(t)
			env.WriteConfig(fix.Config)
			dialer := testserver.TestDialer{Password: "testpass"}

			for _, c := range tt.contacts {
				env.AddContact(c.name, c.addr, c.perm)
			}

			for _, m := range tt.messages {
				switch {
				case m.signed:
					fix.AddRawMessage("INBOX", buildPGPSignedRFC822(t, gpgBin, m.from, m.subject, "body"))
				case m.trusted:
					fix.AddRawMessage("INBOX", buildTrustedRFC822(m.from, m.subject, "body"))
				default:
					fix.AddMessage("INBOX", m.from, m.subject, "body")
				}
			}

			mock := &mockMissionCreator{}
			handler := NewMailHandler(t.Context(), env.Resolver, dialer, mock, nil, nil, discardLogger(), 0, nil, nil)

			handler.OnNewMail(uint32(len(tt.messages)))

			assert.Equal(t, tt.wantMissions, len(mock.calls), "mission count")

			for i, want := range tt.wantSubjects {
				require.Greater(t, len(mock.calls), i, "not enough missions created")
				assert.Equal(t, want, mock.calls[i].Subject)
			}
		})
	}
}

// TestOnNewMail_PGPKeyMismatch verifies that a PGP-signed message from a
// contact with a registered GPGKeyID is rejected when the signing key
// doesn't match. Finding 2: key-to-sender binding.
func TestOnNewMail_PGPKeyMismatch(t *testing.T) {
	gpgBin, err := exec.LookPath("gpg")
	if err != nil {
		t.Skip("gpg not installed")
	}

	env := testenv.New(t, "test@test.com")
	fix := testserver.NewFixture(t)
	env.WriteConfig(fix.Config)
	dialer := testserver.TestDialer{Password: "testpass"}

	// Add contact with a GPGKeyID that won't match the signing key.
	env.AddContact("Jim", "jim@punt-labs.com", "rwx")

	// Inject a GPGKeyID that doesn't match the test signing key.
	// We need to write the contacts file directly to include the GPGKeyID.
	writeContactWithGPGKey(t, env, "Jim", "jim@punt-labs.com", "rwx", "DEADBEEF12345678")

	fix.AddRawMessage("INBOX", buildPGPSignedRFC822(t, gpgBin, "jim@punt-labs.com", "Test", "body"))

	mock := &mockMissionCreator{}
	handler := NewMailHandler(t.Context(), env.Resolver, dialer, mock, nil, nil, discardLogger(), 0, nil, nil)
	handler.OnNewMail(1)

	assert.Equal(t, 0, len(mock.calls), "mission should not be created: key mismatch")
}

// TestOnNewMail_PGPKeyMatch verifies that a PGP-signed message from a
// contact with a matching GPGKeyID is accepted. Finding 2.
func TestOnNewMail_PGPKeyMatch(t *testing.T) {
	gpgBin, err := exec.LookPath("gpg")
	if err != nil {
		t.Skip("gpg not installed")
	}

	env := testenv.New(t, "test@test.com")
	fix := testserver.NewFixture(t)
	env.WriteConfig(fix.Config)
	dialer := testserver.TestDialer{Password: "testpass"}

	// Build a signed message and extract the key ID from it.
	raw, keyID := buildPGPSignedRFC822WithKeyID(t, gpgBin, "jim@punt-labs.com", "Match test", "body")

	// Register contact with the correct key ID.
	writeContactWithGPGKey(t, env, "Jim", "jim@punt-labs.com", "rwx", keyID)

	fix.AddRawMessage("INBOX", raw)

	mock := &mockMissionCreator{}
	handler := NewMailHandler(t.Context(), env.Resolver, dialer, mock, nil, nil, discardLogger(), 0, nil, nil)
	handler.OnNewMail(1)

	assert.Equal(t, 1, len(mock.calls), "mission should be created: key matches")
}

type testMsg struct {
	from    string
	subject string
	trusted bool // if true, include Proton E2E headers (no PGP)
	signed  bool // if true, include real PGP-signed envelope
}

type testContact struct {
	name string
	addr string
	perm string
}

// buildTrustedRFC822 returns raw RFC822 bytes with Proton E2E trust headers
// but no PGP signature. These headers are SMTP-injectable and should NOT
// be sufficient for x-bit execution.
func buildTrustedRFC822(from, subject, body string) []byte {
	return []byte(fmt.Sprintf(
		"From: %s\r\nSubject: %s\r\nDate: %s\r\nMessage-ID: <%d@test>\r\n"+
			"X-Pm-Content-Encryption: end-to-end\r\nX-Pm-Origin: internal\r\n"+
			"Content-Type: text/plain\r\n\r\n%s",
		from, subject, time.Now().Format(time.RFC1123Z), time.Now().UnixNano(), body,
	))
}

// buildPGPSignedRFC822 generates a real PGP-signed multipart/signed
// RFC822 message with an attached public key. Uses an ephemeral GPG keypair.
func buildPGPSignedRFC822(t *testing.T, gpgBin, from, subject, body string) []byte {
	t.Helper()
	raw, _ := buildPGPSignedMessage(t, gpgBin, from, subject, body)
	return raw
}

// buildPGPSignedRFC822WithKeyID generates a real PGP-signed message and
// returns both the raw bytes and the signing key ID.
func buildPGPSignedRFC822WithKeyID(t *testing.T, gpgBin, from, subject, body string) ([]byte, string) {
	t.Helper()
	return buildPGPSignedMessage(t, gpgBin, from, subject, body)
}

// buildPGPSignedMessage is the shared implementation for building PGP-signed
// RFC822 messages with ephemeral GPG keys.
func buildPGPSignedMessage(t *testing.T, gpgBin, from, subject, body string) (raw []byte, keyID string) {
	t.Helper()

	home := testenv.ShortGPGHome(t)
	base := []string{"--homedir", home, "--batch", "--no-tty"}

	// Generate ephemeral key. Use from address as the email.
	email := from
	testenv.GenKey(t, gpgBin, home, "Test Signer", email)

	// Extract key ID.
	listCmd := exec.Command(gpgBin, append(base, "--list-keys", "--with-colons", email)...)
	var listBuf bytes.Buffer
	listCmd.Stdout = &listBuf
	listCmd.Stderr = os.Stderr
	require.NoError(t, listCmd.Run())

	for _, line := range bytes.Split(listBuf.Bytes(), []byte("\n")) {
		parts := bytes.Split(line, []byte(":"))
		if len(parts) >= 5 && string(parts[0]) == "pub" {
			keyID = string(parts[4])
			break
		}
	}
	require.NotEmpty(t, keyID, "failed to extract key ID")

	// Build the signed body part.
	bodyPart := "Content-Type: text/plain; charset=utf-8\r\n" +
		"Content-Transfer-Encoding: 7bit\r\n" +
		"\r\n" +
		body + "\r\n"

	// Detach-sign the body.
	signCmd := exec.Command(gpgBin, append(base, "--detach-sign", "--armor", "-u", email)...)
	signCmd.Stdin = bytes.NewBufferString(bodyPart)
	var sigBuf bytes.Buffer
	signCmd.Stdout = &sigBuf
	signCmd.Stderr = os.Stderr
	require.NoError(t, signCmd.Run())

	// Export public key.
	exportCmd := exec.Command(gpgBin, append(base, "--export", "--armor", email)...)
	var keyBuf bytes.Buffer
	exportCmd.Stdout = &keyBuf
	require.NoError(t, exportCmd.Run())

	// Assemble multipart/signed RFC 3156 message.
	boundary := "TestBoundary12345"
	var msg bytes.Buffer
	fmt.Fprintf(&msg, "From: %s\r\n", from)
	fmt.Fprintf(&msg, "To: test@test.com\r\n")
	fmt.Fprintf(&msg, "Subject: %s\r\n", subject)
	fmt.Fprintf(&msg, "Date: %s\r\n", time.Now().Format(time.RFC1123Z))
	fmt.Fprintf(&msg, "Message-ID: <%d@test>\r\n", time.Now().UnixNano())
	fmt.Fprintf(&msg, "MIME-Version: 1.0\r\n")
	fmt.Fprintf(&msg, "Content-Type: multipart/signed; boundary=%s; micalg=pgp-sha256; protocol=\"application/pgp-signature\"\r\n", boundary)
	fmt.Fprintf(&msg, "\r\n")
	fmt.Fprintf(&msg, "--%s\r\n", boundary)
	msg.WriteString(bodyPart)
	fmt.Fprintf(&msg, "\r\n--%s\r\n", boundary)
	fmt.Fprintf(&msg, "Content-Type: application/pgp-signature; name=\"signature.asc\"\r\n")
	fmt.Fprintf(&msg, "\r\n")
	msg.Write(bytes.TrimSpace(sigBuf.Bytes()))
	fmt.Fprintf(&msg, "\r\n--%s\r\n", boundary)
	fmt.Fprintf(&msg, "Content-Type: application/pgp-keys\r\n")
	fmt.Fprintf(&msg, "\r\n")
	msg.Write(bytes.TrimSpace(keyBuf.Bytes()))
	fmt.Fprintf(&msg, "\r\n--%s--\r\n", boundary)

	return msg.Bytes(), keyID
}

// writeContactWithGPGKey overwrites the contacts file to include a GPGKeyID.
// This is needed because testenv.AddContact doesn't support GPGKeyID.
func writeContactWithGPGKey(t *testing.T, env *testenv.Env, name, addr, perm, gpgKeyID string) {
	t.Helper()

	contactsJSON := fmt.Sprintf(`[{"name":%q,"email":%q,"gpg_key_id":%q,"permissions":{%q:%q}}]`,
		name, addr, gpgKeyID, env.Email, perm)

	contactsPath := env.IdentityDir() + "/contacts.json"
	require.NoError(t, os.WriteFile(contactsPath, []byte(contactsJSON), 0o640))
}
