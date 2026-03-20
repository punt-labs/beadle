package paths

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDataDir(t *testing.T) {
	dir, err := DataDir()
	require.NoError(t, err)
	assert.Contains(t, dir, ".punt-labs")
	assert.Contains(t, dir, "beadle")
}

func TestEthosDir(t *testing.T) {
	dir, err := EthosDir()
	require.NoError(t, err)
	assert.Contains(t, dir, ".punt-labs")
	assert.Contains(t, dir, "ethos")
}

func TestIdentityDir(t *testing.T) {
	dir, err := IdentityDir("claude@punt-labs.com")
	require.NoError(t, err)
	assert.Contains(t, dir, "identities")
	assert.Contains(t, dir, "claude@punt-labs.com")
}

func TestIdentityConfigPath(t *testing.T) {
	p, err := IdentityConfigPath("claude@punt-labs.com")
	require.NoError(t, err)
	assert.Contains(t, p, "identities/claude@punt-labs.com/email.json")
}

func TestIdentityContactsPath(t *testing.T) {
	p, err := IdentityContactsPath("claude@punt-labs.com")
	require.NoError(t, err)
	assert.Contains(t, p, "identities/claude@punt-labs.com/contacts.json")
}
