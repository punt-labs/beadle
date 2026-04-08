package secret

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withRunners swaps the package-level pass and secret-tool runners for
// the duration of a test and restores them afterward. Returns a
// teardown that must be deferred.
func withRunners(t *testing.T, pass, secretTool func(string) (string, error)) func() {
	t.Helper()
	origPass := passRunner
	origSecretTool := secretToolRunner
	passRunner = pass
	secretToolRunner = secretTool
	return func() {
		passRunner = origPass
		secretToolRunner = origSecretTool
	}
}

func TestKeychainGet_PassWins(t *testing.T) {
	defer withRunners(t,
		func(name string) (string, error) {
			require.Equal(t, "imap-password", name)
			return "from-pass", nil
		},
		func(string) (string, error) {
			t.Fatal("secret-tool runner should not be called when pass succeeds")
			return "", nil
		},
	)()

	val, err := keychainGet("imap-password")
	require.NoError(t, err)
	assert.Equal(t, "from-pass", val)
}

func TestKeychainGet_FallbackToSecretTool(t *testing.T) {
	defer withRunners(t,
		func(string) (string, error) {
			return "", errors.New("pass: entry not in store")
		},
		func(name string) (string, error) {
			require.Equal(t, "imap-password", name)
			return "from-secret-tool", nil
		},
	)()

	val, err := keychainGet("imap-password")
	require.NoError(t, err)
	assert.Equal(t, "from-secret-tool", val)
}

func TestKeychainGet_EmptyPassFallsThrough(t *testing.T) {
	// An empty string from pass (no error) should not be returned —
	// it falls through to secret-tool. This guards against a
	// misconfigured pass entry masking a working secret-tool entry.
	defer withRunners(t,
		func(string) (string, error) {
			return "", nil
		},
		func(string) (string, error) {
			return "from-secret-tool", nil
		},
	)()

	val, err := keychainGet("imap-password")
	require.NoError(t, err)
	assert.Equal(t, "from-secret-tool", val)
}

func TestKeychainGet_BothFail(t *testing.T) {
	defer withRunners(t,
		func(string) (string, error) {
			return "", errors.New("pass: entry not in store")
		},
		func(string) (string, error) {
			return "", errors.New("secret-tool: no matching entry")
		},
	)()

	_, err := keychainGet("missing")
	require.Error(t, err)
	// Should surface the last error seen (secret-tool's) — the
	// resolution chain in Get() treats any non-nil err as "try next
	// backend", so the exact text is informational.
	assert.Contains(t, err.Error(), "secret-tool")
}

func TestKeychainGet_BothEmpty(t *testing.T) {
	// Both runners return empty string with no error (e.g. because
	// the binaries are not installed and their error paths returned
	// nil). Expect a synthesized "no backend available" error so the
	// resolution chain can fall through cleanly.
	defer withRunners(t,
		func(string) (string, error) { return "", nil },
		func(string) (string, error) { return "", nil },
	)()

	_, err := keychainGet("missing")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no Linux keychain backend available")
}

func TestKeychainGet_PassWhitespaceTrimmed(t *testing.T) {
	// Real pass output has a trailing newline; the runner strips it.
	// Verify that keychainGet round-trips the trimmed value.
	defer withRunners(t,
		func(string) (string, error) {
			// Runner already trims; simulate the post-trim contract.
			return "s3cret", nil
		},
		func(string) (string, error) {
			t.Fatal("secret-tool runner should not be called")
			return "", nil
		},
	)()

	val, err := keychainGet("imap-password")
	require.NoError(t, err)
	assert.Equal(t, "s3cret", val)
}

func TestRealPassGet_NotInstalled(t *testing.T) {
	// Force exec.LookPath("pass") to fail by clearing PATH. The
	// runner must return a non-nil error without panicking, and the
	// error must identify pass as the missing binary.
	t.Setenv("PATH", "")

	_, err := realPassGet("imap-password")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pass not installed")
}

func TestRealSecretToolGet_NotInstalled(t *testing.T) {
	t.Setenv("PATH", "")

	_, err := realSecretToolGet("imap-password")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "secret-tool not installed")
}

func TestLinuxKeychainBackendNames_Empty(t *testing.T) {
	// With PATH cleared, neither pass nor secret-tool resolves, so
	// keychainBackendNames should return nil/empty. This guards the
	// Available() report on a fresh machine.
	t.Setenv("PATH", "")

	names := keychainBackendNames()
	assert.Empty(t, names)
}

func TestKeychainAvailable_Empty(t *testing.T) {
	t.Setenv("PATH", "")

	assert.False(t, keychainAvailable())
}
