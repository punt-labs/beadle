// Package testenv creates complete identity environments for integration tests.
// It sets up temp directories mimicking the ethos/beadle sidecar layout.
package testenv

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/punt-labs/beadle/internal/contacts"
	"github.com/punt-labs/beadle/internal/email"
	"github.com/punt-labs/beadle/internal/identity"
	"github.com/stretchr/testify/require"
)

// Env is a complete identity test environment backed by temp directories.
type Env struct {
	EthosDir  string
	BeadleDir string
	RepoDir   string
	Resolver  *identity.Resolver
	Email     string // the identity email
	idDir     string // identity-scoped dir under beadleDir
	t         testing.TB
}

// New creates a test environment for the given email address.
// It sets up ethos identity files, a beadle identity directory,
// and a Resolver pointed at all of them.
//
// WARNING: Uses t.Setenv to override HOME and BEADLE_IMAP_PASSWORD.
// This modifies process-global state and is incompatible with t.Parallel().
func New(t testing.TB, emailAddr string) *Env {
	t.Helper()

	// Create a fake HOME so paths.DataDir() and paths.EthosDir() resolve
	// to our temp dirs (they use os.UserHomeDir → $HOME).
	// Set BEADLE_IMAP_PASSWORD so credential resolution works without
	// keychain access. Needed by SMTPSend (which calls cfg.IMAPPassword()
	// on the disk-loaded config, not through TestDialer).
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	t.Setenv("BEADLE_IMAP_PASSWORD", "testpass")

	ethosDir := filepath.Join(fakeHome, ".punt-labs", "ethos")
	beadleDir := filepath.Join(fakeHome, ".punt-labs", "beadle")
	repoDir := t.TempDir()

	handle := "testuser"

	// Write ethos identity YAML.
	idDir := filepath.Join(ethosDir, "identities")
	require.NoError(t, os.MkdirAll(idDir, 0o750))
	idYAML := "handle: " + handle + "\nname: Test User\nemail: " + emailAddr + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(idDir, handle+".yaml"), []byte(idYAML), 0o640))

	// Write repo-local ethos config.
	repoEthosDir := filepath.Join(repoDir, ".punt-labs", "ethos")
	require.NoError(t, os.MkdirAll(repoEthosDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(repoEthosDir, "config.yaml"), []byte("agent: "+handle+"\n"), 0o640))

	// Create beadle identity directory.
	beadleIDDir := filepath.Join(beadleDir, "identities", emailAddr)
	require.NoError(t, os.MkdirAll(beadleIDDir, 0o750))

	// Write empty contacts.
	require.NoError(t, os.WriteFile(filepath.Join(beadleIDDir, "contacts.json"), []byte("[]"), 0o640))

	resolver := identity.NewResolver(ethosDir, beadleDir, repoDir)

	return &Env{
		EthosDir:  ethosDir,
		BeadleDir: beadleDir,
		RepoDir:   repoDir,
		Resolver:  resolver,
		Email:     emailAddr,
		idDir:     beadleIDDir,
		t:         t,
	}
}

// AddContact adds a contact to the identity-scoped contacts file.
func (e *Env) AddContact(name, addr, permissions string) {
	e.t.Helper()

	contactsPath := filepath.Join(e.idDir, "contacts.json")
	store := contacts.NewStore(contactsPath)
	require.NoError(e.t, store.Load())

	perm, err := contacts.ParsePermission(permissions)
	require.NoError(e.t, err)

	c := contacts.Contact{
		Name:  name,
		Email: addr,
		Permissions: map[string]string{
			strings.ToLower(e.Email): perm.String(),
		},
	}
	_, err = store.Add(c)
	require.NoError(e.t, err)
}

// WriteConfig writes an email.json config file to the identity directory.
func (e *Env) WriteConfig(cfg *email.Config) {
	e.t.Helper()

	data, err := json.MarshalIndent(map[string]any{
		"imap_host":    cfg.IMAPHost,
		"imap_port":    cfg.IMAPPort,
		"imap_user":    cfg.IMAPUser,
		"smtp_port":    cfg.SMTPPort,
		"from_address": cfg.FromAddress,
	}, "", "  ")
	require.NoError(e.t, err)

	require.NoError(e.t, os.WriteFile(filepath.Join(e.idDir, "email.json"), data, 0o640))
}

// IdentityDir returns the beadle identity directory path.
func (e *Env) IdentityDir() string {
	return e.idDir
}
