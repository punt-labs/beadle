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

// TestResolveDaemonOwnerKeyID_MissingConfigIsSilent proves the fix for the
// bug where os.IsNotExist(err) never matched daemon.LoadConfig's wrapped
// error (fmt.Errorf("read config %s: %w", path, err)) -- os.IsNotExist does
// not unwrap %w chains, so every daemon start with no daemon.json (the
// common, expected case for any daemon that has not opted into signing
// enforcement) logged at Error on every single startup. errors.Is does
// unwrap correctly; this asserts both the empty return value and, more
// importantly, that nothing was logged at Error for the absent-file case.
func TestResolveDaemonOwnerKeyID_MissingConfigIsSilent(t *testing.T) {
	h := &capturingHandler{}
	logger := slog.New(h)

	keyID := resolveDaemonOwnerKeyID(filepath.Join(t.TempDir(), "daemon.json"), &identity.Resolver{}, logger)

	assert.Empty(t, keyID)
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

	keyID := resolveDaemonOwnerKeyID(path, &identity.Resolver{}, logger)

	assert.Empty(t, keyID)
	assert.True(t, h.hasLevel(slog.LevelError),
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

	keyID := resolveDaemonOwnerKeyID(path, &identity.Resolver{}, logger)

	assert.Empty(t, keyID)
	assert.True(t, h.hasLevel(slog.LevelError))
}
