package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/punt-labs/beadle/internal/identity"
)

// capturingHandler is a slog.Handler that records every log.Record it
// receives, so a test can assert on the level and message a call site
// logged, not just resolveDaemonOwnerKeyID's return value.
type capturingHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *capturingHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *capturingHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r)
	return nil
}

func (h *capturingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *capturingHandler) WithGroup(string) slog.Handler      { return h }

func (h *capturingHandler) hasLevel(level slog.Level) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, r := range h.records {
		if r.Level == level {
			return true
		}
	}
	return false
}

// hasRecord reports whether a record at level with exactly msg was
// captured. Distinguishing by message, not just level, matters here
// specifically: resolveDaemonOwnerKeyID has two distinct Error-level call
// sites ("daemon config unreadable..." vs "signature policy
// unavailable..."), and hasLevel alone cannot tell a test that the wrong
// one fired -- a bug that swapped which branch logs which message would
// still pass a hasLevel(LevelError) assertion.
func (h *capturingHandler) hasRecord(level slog.Level, msg string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, r := range h.records {
		if r.Level == level && r.Message == msg {
			return true
		}
	}
	return false
}

// TestResolveDaemonOwnerKeyID_MissingConfigIsSilent proves the "zero agent
// authority" behavior (docs/ARCHITECTURE.md): an absent daemon.json now
// disables command loading, the same as a present-but-unresolvable one,
// because nothing is authorized to run unless the operator has explicitly
// configured who may authorize it. What distinguishes the two is logging,
// not the outcome -- an absent daemon.json is the common, expected,
// unconfigured case, so it must stay silent at Error level, unlike a
// present-but-broken config which does log at Error. errors.Is (not
// os.IsNotExist, which does not unwrap %w chains) is required to detect the
// absent-file case correctly, since daemon.LoadConfig wraps the underlying
// os.ReadFile error.
func TestResolveDaemonOwnerKeyID_MissingConfigIsSilent(t *testing.T) {
	h := &capturingHandler{}
	logger := slog.New(h)

	keyID, loadEnabled := resolveDaemonOwnerKeyID(filepath.Join(t.TempDir(), "daemon.json"), &identity.Resolver{}, logger)

	assert.Empty(t, keyID)
	assert.False(t, loadEnabled, "an absent daemon.json means nothing has authorized command execution -- command loading must be disabled")
	assert.False(t, h.hasLevel(slog.LevelError),
		"an absent daemon.json is the common, unconfigured case and must not log at Error")
}

// TestResolveDaemonOwnerKeyID_UnreadableConfigLogsError covers the
// opposite branch: a daemon.json that exists but cannot be parsed is a
// real misconfiguration, not the common unconfigured case, and must log
// loudly.
func TestResolveDaemonOwnerKeyID_UnreadableConfigLogsError(t *testing.T) {
	h := &capturingHandler{}
	logger := slog.New(h)

	path := filepath.Join(t.TempDir(), "daemon.json")
	require.NoError(t, os.WriteFile(path, []byte("not valid json"), 0o600))

	keyID, loadEnabled := resolveDaemonOwnerKeyID(path, &identity.Resolver{}, logger)

	assert.Empty(t, keyID)
	assert.False(t, loadEnabled, "an unreadable daemon.json is misconfigured, not unconfigured -- command loading must be disabled entirely")
	assert.True(t, h.hasRecord(slog.LevelError, "daemon config unreadable, command loading disabled"),
		"a present but unparseable daemon.json is a real misconfiguration and must log at Error")
}

// TestResolveDaemonOwnerKeyID_UnresolvableOwnerLogsError covers the third
// branch: a well-formed daemon.json whose owner_handle/owner_gpg_key_id
// does not resolve to a valid fingerprint (here, neither field set) also
// logs at Error and disables command loading, never silently.
func TestResolveDaemonOwnerKeyID_UnresolvableOwnerLogsError(t *testing.T) {
	h := &capturingHandler{}
	logger := slog.New(h)

	path := filepath.Join(t.TempDir(), "daemon.json")
	require.NoError(t, os.WriteFile(path, []byte(`{}`), 0o600))

	keyID, loadEnabled := resolveDaemonOwnerKeyID(path, &identity.Resolver{}, logger)

	assert.Empty(t, keyID)
	assert.False(t, loadEnabled, "an unresolvable owner config must disable command loading entirely")
	assert.True(t, h.hasRecord(slog.LevelError, "signature policy unavailable, command loading disabled"))
}

// TestResolveDaemonOwnerKeyID_DirectFingerprintResolves is the success
// path: a well-formed daemon.json with a valid owner_gpg_key_id resolves
// to that fingerprint, with nothing logged at Error.
func TestResolveDaemonOwnerKeyID_DirectFingerprintResolves(t *testing.T) {
	h := &capturingHandler{}
	logger := slog.New(h)

	const fpr = "0123456789ABCDEF0123456789ABCDEF01234567"
	path := filepath.Join(t.TempDir(), "daemon.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"owner_gpg_key_id": "`+fpr+`"}`), 0o600))

	keyID, loadEnabled := resolveDaemonOwnerKeyID(path, &identity.Resolver{}, logger)

	assert.Equal(t, fpr, keyID)
	assert.True(t, loadEnabled)
	assert.False(t, h.hasLevel(slog.LevelError))
}

// TestResolveDaemonOwnerKeyID_AmbiguousConfigDisablesLoading covers the
// "both fields set" branch of a present-but-misconfigured daemon.json: an
// ambiguous config disables command loading entirely, the same as any
// other unresolvable owner config.
func TestResolveDaemonOwnerKeyID_AmbiguousConfigDisablesLoading(t *testing.T) {
	h := &capturingHandler{}
	logger := slog.New(h)

	const fpr = "0123456789ABCDEF0123456789ABCDEF01234567"
	path := filepath.Join(t.TempDir(), "daemon.json")
	require.NoError(t, os.WriteFile(path,
		[]byte(`{"owner_handle": "operator", "owner_gpg_key_id": "`+fpr+`"}`), 0o600))

	keyID, loadEnabled := resolveDaemonOwnerKeyID(path, &identity.Resolver{}, logger)

	assert.Empty(t, keyID)
	assert.False(t, loadEnabled, "owner_handle and owner_gpg_key_id both set is ambiguous -- command loading must be disabled entirely")
	assert.True(t, h.hasRecord(slog.LevelError, "signature policy unavailable, command loading disabled"))
}

// TestResolveDaemonOwnerKeyID_MalformedFingerprintDisablesLoading covers
// the "malformed fingerprint" branch: a syntactically well-formed
// daemon.json whose owner_gpg_key_id is not a full 40-hex fingerprint also
// disables command loading entirely, never falls back to unsigned loading.
func TestResolveDaemonOwnerKeyID_MalformedFingerprintDisablesLoading(t *testing.T) {
	h := &capturingHandler{}
	logger := slog.New(h)

	path := filepath.Join(t.TempDir(), "daemon.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"owner_gpg_key_id": "not-a-fingerprint"}`), 0o600))

	keyID, loadEnabled := resolveDaemonOwnerKeyID(path, &identity.Resolver{}, logger)

	assert.Empty(t, keyID)
	assert.False(t, loadEnabled, "a malformed fingerprint is a real misconfiguration -- command loading must be disabled entirely")
	assert.True(t, h.hasRecord(slog.LevelError, "signature policy unavailable, command loading disabled"))
}

// TestLoadDaemonCommands_DisabledNeverCallsLoadCommands is the regression
// test covering the present-but-unresolvable-config case: when enforcement
// was requested but could not be resolved (loadCommandsEnabled == false),
// daemon.LoadCommands must never run at all -- not run with ownerKeyID ==
// "" (which would silently load every command file unsigned), and not run
// at all. cmdDir here contains a command file that WOULD load successfully
// if LoadCommands were called with an empty ownerKeyID (verification
// skipped) -- so if this test's commands map came back non-empty, that
// would prove the bug reappeared: LoadCommands was invoked despite
// loadCommandsEnabled being false. See
// TestLoadDaemonCommands_AbsentConfigNeverCallsLoadCommands for the
// companion absent-config case, which reaches loadCommandsEnabled == false
// through resolveDaemonOwnerKeyID's other branch.
func TestLoadDaemonCommands_DisabledNeverCallsLoadCommands(t *testing.T) {
	h := &capturingHandler{}
	logger := slog.New(h)

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "wall.yaml"), []byte(`name: wall
prompt: hello
output_schema: text
budget:
  rounds: 1
`), 0o600))

	commands := loadDaemonCommands(dir, "gpg", "", false, logger)

	assert.Empty(t, commands, "command loading must stay disabled -- a loadable file in cmdDir must not appear")
	assert.True(t, h.hasRecord(slog.LevelWarn, "command loading disabled: signing enforcement could not be resolved"))
}

// TestLoadDaemonCommands_EnabledLoadsCommands is the companion success
// path: loadCommandsEnabled == true calls through to daemon.LoadCommands
// and returns what it loads.
func TestLoadDaemonCommands_EnabledLoadsCommands(t *testing.T) {
	h := &capturingHandler{}
	logger := slog.New(h)

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "wall.yaml"), []byte(`name: wall
prompt: hello
output_schema: text
budget:
  rounds: 1
`), 0o600))

	commands := loadDaemonCommands(dir, "gpg", "", true, logger)

	assert.Contains(t, commands, "wall")
	assert.False(t, h.hasRecord(slog.LevelWarn, "command loading disabled: signing enforcement could not be resolved"))
}

// TestLoadDaemonCommands_AbsentConfigNeverCallsLoadCommands proves the
// end-to-end effect of the "zero agent authority" fix: with no daemon.json
// present at all -- the ordinary case for an operator who has not set up
// signing enforcement -- resolveDaemonOwnerKeyID's loadCommandsEnabled
// result must still disable daemon.LoadCommands entirely when fed into
// loadDaemonCommands. cmdDir contains a command file that WOULD load
// successfully if LoadCommands were called with an empty ownerKeyID
// (verification skipped) -- so a non-empty commands map here would prove
// the pre-fix backdoor reappeared: an unconfigured daemon loading and
// running unsigned commands. Unlike
// TestLoadDaemonCommands_DisabledNeverCallsLoadCommands, which drives
// loadCommandsEnabled == false through a present-but-unresolvable
// daemon.json, this test drives it through the absent-file branch.
func TestLoadDaemonCommands_AbsentConfigNeverCallsLoadCommands(t *testing.T) {
	h := &capturingHandler{}
	logger := slog.New(h)

	cmdDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(cmdDir, "wall.yaml"), []byte(`name: wall
prompt: hello
output_schema: text
budget:
  rounds: 1
`), 0o600))

	ownerKeyID, loadEnabled := resolveDaemonOwnerKeyID(filepath.Join(t.TempDir(), "daemon.json"), &identity.Resolver{}, logger)
	require.False(t, loadEnabled)
	require.Empty(t, ownerKeyID)
	require.False(t, h.hasLevel(slog.LevelError), "an absent daemon.json must not log at Error")

	commands := loadDaemonCommands(cmdDir, "gpg", ownerKeyID, loadEnabled, logger)

	assert.Empty(t, commands, "an unconfigured daemon must never load commands, signed or not")
}
