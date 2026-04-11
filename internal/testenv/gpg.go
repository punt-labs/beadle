package testenv

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// ShortGPGHome creates a GPG homedir with a path short enough for
// gpg-agent's Unix socket (108-byte limit). Go's t.TempDir() paths
// are too long, so we use /tmp directly and register cleanup.
func ShortGPGHome(t testing.TB) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "bg-*")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(dir) })

	home := filepath.Join(dir, "g")
	require.NoError(t, os.Mkdir(home, 0700))
	return home
}

// GenKey creates an ephemeral GPG keypair with a 1-year expiry in the given homedir.
func GenKey(t testing.TB, gpgBin, home, name, email string) {
	t.Helper()
	base := []string{"--homedir", home, "--batch", "--no-tty"}
	script := fmt.Sprintf(`%%no-protection
Key-Type: RSA
Key-Length: 2048
Name-Real: %s
Name-Email: %s
Expire-Date: 1y
%%commit
`, name, email)

	cmd := exec.Command(gpgBin, append(base, "--gen-key")...)
	cmd.Stdin = bytes.NewBufferString(script)
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Run(), "gpg key generation failed")
}

// GenKeyNoExpiry creates an ephemeral GPG keypair without an expiration date.
func GenKeyNoExpiry(t testing.TB, gpgBin, home, name, email string) {
	t.Helper()
	base := []string{"--homedir", home, "--batch", "--no-tty"}
	script := fmt.Sprintf(`%%no-protection
Key-Type: RSA
Key-Length: 2048
Name-Real: %s
Name-Email: %s
Expire-Date: 0
%%commit
`, name, email)

	cmd := exec.Command(gpgBin, append(base, "--gen-key")...)
	cmd.Stdin = bytes.NewBufferString(script)
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Run(), "gpg key generation failed")
}

// GenKeyWithPassphrase creates an ephemeral GPG keypair with a passphrase and 1-year expiry.
func GenKeyWithPassphrase(t testing.TB, gpgBin, home, name, email, passphrase string) {
	t.Helper()
	base := []string{"--homedir", home, "--batch", "--no-tty"}
	script := fmt.Sprintf(`%%echo Generating test key
Key-Type: RSA
Key-Length: 2048
Name-Real: %s
Name-Email: %s
Passphrase: %s
Expire-Date: 1y
%%commit
`, name, email, passphrase)

	cmd := exec.Command(gpgBin, append(base, "--gen-key")...)
	cmd.Stdin = bytes.NewBufferString(script)
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Run(), "gpg key generation failed")
}
