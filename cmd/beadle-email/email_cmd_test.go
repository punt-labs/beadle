package main

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/punt-labs/beadle/internal/channel"
	"github.com/punt-labs/beadle/internal/email"
	"github.com/punt-labs/beadle/internal/paths"
)

// TestChangeStatus proves a mutating command reports "not_found" when nothing
// changed, so mark/move JSON never reads as success for a stale UID.
func TestChangeStatus(t *testing.T) {
	assert.Equal(t, "not_found", changeStatus(0, "marked"))
	assert.Equal(t, "marked", changeStatus(1, "marked"))
	assert.Equal(t, "not_found", changeStatus(0, "moved"))
	assert.Equal(t, "moved", changeStatus(3, "moved"))
}

// TestMoveResultMap proves the move JSON schema is consistent: destination is
// present whether the message moved or was not found, so consumers need not
// special-case the not-found result.
func TestMoveResultMap(t *testing.T) {
	notFound := moveResultMap("9999", "INBOX", "Archive", 0)
	assert.Equal(t, "not_found", notFound["status"])
	assert.Equal(t, "Archive", notFound["destination"], "not-found result must still carry destination")
	assert.Equal(t, 0, notFound["moved"])

	moved := moveResultMap("7", "INBOX", "Archive", 1)
	assert.Equal(t, "moved", moved["status"])
	assert.Equal(t, "Archive", moved["destination"])
	assert.Equal(t, 1, moved["moved"])
}

// TestListCmd_RejectsBadPaging proves list validates count and offset before
// opening a connection, so a bad value is a deterministic error, not a silent
// empty result. The checks run first in RunE, so no server is contacted.
func TestListCmd_RejectsBadPaging(t *testing.T) {
	orig := struct {
		count, offset int
	}{listCount, listOffset}
	t.Cleanup(func() { listCount, listOffset = orig.count, orig.offset })

	listOffset = 0
	listCount = 0
	require.ErrorContains(t, listCmd.RunE(listCmd, nil), "--count must be positive")

	listCount = -5
	require.ErrorContains(t, listCmd.RunE(listCmd, nil), "--count must be positive")

	listCount = 10
	listOffset = -1
	require.ErrorContains(t, listCmd.RunE(listCmd, nil), "--offset must be non-negative")
}

// TestSearchCmd_RejectsBadPaging proves search validates count and offset up
// front, after the criteria check and before connecting.
func TestSearchCmd_RejectsBadPaging(t *testing.T) {
	orig := struct {
		from          string
		count, offset int
	}{searchFrom, searchCount, searchOffset}
	t.Cleanup(func() {
		searchFrom, searchCount, searchOffset = orig.from, orig.count, orig.offset
	})

	searchFrom = "alice@test.com" // satisfy the "at least one criterion" gate
	searchOffset = 0
	searchCount = 0
	require.ErrorContains(t, searchCmd.RunE(searchCmd, nil), "--count must be positive")

	searchCount = 10
	searchOffset = -2
	require.ErrorContains(t, searchCmd.RunE(searchCmd, nil), "--offset must be non-negative")
}

func sampleMessages() []channel.MessageSummary {
	when := time.Date(2026, 7, 25, 9, 0, 0, 0, time.UTC)
	return []channel.MessageSummary{
		{ID: "7", From: "alice@test.com", Date: when, Subject: "hello"},
	}
}

// TestPrintMessages_PastEndPageShowsTotal proves the CLI surfaces the true
// total on a page past the end, rather than a bare empty result that reads as
// an empty mailbox.
func TestPrintMessages_PastEndPageShowsTotal(t *testing.T) {
	var g globalOpts
	var buf bytes.Buffer
	g.printMessages(&buf, &email.ListResult{Total: 25})

	out := buf.String()
	assert.Contains(t, out, "showing 0 of 25 messages")
	assert.Contains(t, out, "page past end")
	assert.NotEmpty(t, out, "a past-end page must not print nothing")
}

// TestPrintMessages_DegradedNoticeUnderQuiet proves the degraded notice reaches
// the user even with --quiet, when the stderr warn is hidden and the rows are
// recent mail, not the requested query.
func TestPrintMessages_DegradedNoticeUnderQuiet(t *testing.T) {
	g := globalOpts{Quiet: true}
	var buf bytes.Buffer
	lr := &email.ListResult{
		Messages:       sampleMessages(),
		Total:          1,
		Degraded:       true,
		DegradedReason: "search unavailable; showing recent mail instead",
	}
	g.printMessages(&buf, lr)

	out := buf.String()
	assert.Contains(t, out, "search unavailable; showing recent mail instead",
		"the degraded notice must appear even under --quiet")
	assert.NotContains(t, out, "showing 1 of 1 messages",
		"--quiet still suppresses the normal status line and rows")
}

// TestPrintMessages_NormalListing shows the status line and rows without a
// degraded notice on an ordinary listing.
func TestPrintMessages_NormalListing(t *testing.T) {
	var g globalOpts
	var buf bytes.Buffer
	g.printMessages(&buf, &email.ListResult{Messages: sampleMessages(), Total: 1})

	out := buf.String()
	assert.Contains(t, out, "showing 1 of 1 messages")
	assert.Contains(t, out, "hello")
	assert.NotContains(t, out, "degraded")
	assert.NotContains(t, out, "recent mail instead")
}

// TestPrintMessages_DegradedShownWithRows proves that, when not quiet, the
// degraded notice leads and the status line and rows follow.
func TestPrintMessages_DegradedShownWithRows(t *testing.T) {
	var g globalOpts
	var buf bytes.Buffer
	lr := &email.ListResult{
		Messages:       sampleMessages(),
		Total:          1,
		Degraded:       true,
		DegradedReason: "search unavailable; showing recent mail instead",
	}
	g.printMessages(&buf, lr)

	out := buf.String()
	assert.Contains(t, out, "search unavailable; showing recent mail instead")
	assert.Contains(t, out, "showing 1 of 1 messages")
	assert.Contains(t, out, "hello")
}

// TestPrintMessages_JSONUnchanged proves --json still emits the message slice.
func TestPrintMessages_JSONUnchanged(t *testing.T) {
	g := globalOpts{JSON: true}
	var buf bytes.Buffer
	lr := &email.ListResult{
		Messages:       sampleMessages(),
		Total:          1,
		Degraded:       true,
		DegradedReason: "search unavailable; showing recent mail instead",
	}
	g.printMessages(&buf, lr)

	out := buf.String()
	assert.Contains(t, out, `"id": "7"`, "JSON carries the message slice")
	assert.NotContains(t, out, "showing 1 of 1 messages", "no human status line in JSON mode")
	assert.NotContains(t, out, "recent mail instead", "no human notice in JSON mode")
}

// TestResolveConfig_UsesIdentityScopedConfigOverExplicit proves resolveConfig
// (used by list/search/read/send/reply/move/mark/folders) prefers the
// identity-scoped config over the explicit --config default, mirroring
// doctor/status's own precedence.
func TestResolveConfig_UsesIdentityScopedConfigOverExplicit(t *testing.T) {
	setupDefaultIdentityHome(t, "agent@test.com")

	idConfigPath, err := paths.IdentityConfigPath("agent@test.com")
	require.NoError(t, err)
	writeConfigFixture(t, idConfigPath, "identity@test.com")

	cfg, id, err := resolveConfig(configFlagCmd(t), email.DefaultConfigPath())
	require.NoError(t, err)
	require.NotNil(t, id)
	assert.Equal(t, "agent@test.com", id.Email)
	assert.Equal(t, "identity@test.com", cfg.IMAPUser)
}

// TestResolveConfig_FailsClosedOnCorruptIdentityConfig proves the fail-closed
// behavior change this round introduces: a corrupt identity-scoped config is
// a hard error for every command that resolves config through resolveConfig
// (list, search, read, send, reply, move, mark, folders), never a silent
// fallback to explicitPath — the same corruption-is-fatal contract doctor and
// status already enforce via email.LoadIdentityConfig.
func TestResolveConfig_FailsClosedOnCorruptIdentityConfig(t *testing.T) {
	home := setupDefaultIdentityHome(t, "agent@test.com")

	idConfigPath, err := paths.IdentityConfigPath("agent@test.com")
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(idConfigPath), 0o700))
	require.NoError(t, os.WriteFile(idConfigPath, []byte(`{not json`), 0o600))

	// A fallback the corrupt identity config must NOT silently fall back to.
	fallbackPath := filepath.Join(home, "fallback-email.json")
	writeConfigFixture(t, fallbackPath, "fallback@test.com")

	cfg, id, err := resolveConfig(configFlagCmd(t), fallbackPath)
	assert.Error(t, err, "a corrupt identity config must fail closed, never report the fallback")
	assert.Nil(t, cfg)
	require.NotNil(t, id, "the resolved identity is still returned alongside the error")
	assert.Equal(t, "agent@test.com", id.Email)
}

// TestResolveConfig_ExplicitFlagSkipsIdentityLookupEntirely proves an
// explicit -c/--config wins the same way doctor/status's loadConfigForCmd
// does: identity-config lookup is skipped entirely, so even a corrupt
// identity config never surfaces — matching list/search/read/send/reply/
// move/mark/folders to doctor's own -c precedence.
func TestResolveConfig_ExplicitFlagSkipsIdentityLookupEntirely(t *testing.T) {
	home := setupDefaultIdentityHome(t, "agent@test.com")

	idConfigPath, err := paths.IdentityConfigPath("agent@test.com")
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(idConfigPath), 0o700))
	require.NoError(t, os.WriteFile(idConfigPath, []byte(`{not json`), 0o600))

	explicitPath := filepath.Join(home, "explicit-email.json")
	writeConfigFixture(t, explicitPath, "explicit@test.com")

	cmd := configFlagCmd(t)
	require.NoError(t, cmd.Flags().Set("config", explicitPath))

	cfg, id, err := resolveConfig(cmd, explicitPath)
	require.NoError(t, err, "an explicit -c must bypass identity-config lookup, so the corrupt identity config never surfaces")
	require.NotNil(t, cfg)
	assert.Equal(t, "explicit@test.com", cfg.IMAPUser)
	require.NotNil(t, id, "the resolved identity is still returned for repo tagging")
	assert.Equal(t, "agent@test.com", id.Email)
}

// writeConfigFixtureWithPort writes a minimal valid email.json config at path
// with an explicit imap_port, so a test can pin the dial target to a port it
// controls instead of accepting LoadConfig's default (1143, Proton Bridge's
// real port) — a value shared with, and dependent on, whatever happens to be
// listening on the machine running the test.
func writeConfigFixtureWithPort(t *testing.T, path, imapUser string, port int) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	body := fmt.Sprintf(`{"imap_host":"127.0.0.1","imap_port":%d,"imap_user":"%s"}`, port, imapUser)
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
}

// TestFoldersCmd_ExplicitConfigFlagSkipsIdentityLookup is a wiring-level
// regression guard: driving an actual mail command (folders) with an
// explicit -c against a corrupt identity config must reach the dial step,
// not fail on the identity config's corruption — proving resolveConfig's
// flag-awareness is wired through cmd, not just exercised in isolation.
func TestFoldersCmd_ExplicitConfigFlagSkipsIdentityLookup(t *testing.T) {
	home := setupDefaultIdentityHome(t, "agent@test.com")

	idConfigPath, err := paths.IdentityConfigPath("agent@test.com")
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(idConfigPath), 0o700))
	require.NoError(t, os.WriteFile(idConfigPath, []byte(`{not json`), 0o600))

	// Allocate an ephemeral port, then close the listener to guarantee nothing
	// is listening on it — a hermetic dial-refused target, unlike the default
	// IMAP port (1143) whose reachability depends on whatever happens to be
	// listening on the machine running the test.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := ln.Addr().(*net.TCPAddr).Port
	require.NoError(t, ln.Close())

	explicitPath := filepath.Join(home, "explicit-email.json")
	writeConfigFixtureWithPort(t, explicitPath, "explicit@test.com", port)

	t.Cleanup(snapshotConfigFlag(t, foldersCmd))
	require.NoError(t, foldersCmd.Flags().Set("config", explicitPath))

	err = foldersCmd.RunE(foldersCmd, nil)
	require.Error(t, err, "nothing is listening on the just-closed port")
	assert.Contains(t, err.Error(), "connection refused",
		"an explicit -c must reach the dial step (a refused connection to the closed port), not fail on the corrupt identity config or a generic substring also satisfied by unrelated dial-stage failures like a missing password")
}
