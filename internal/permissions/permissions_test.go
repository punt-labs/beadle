package permissions

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func settingsFile(root string) string { return filepath.Join(root, ".claude", "settings.json") }

func writeSettings(t *testing.T, root, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".claude"), 0o750))
	require.NoError(t, os.WriteFile(settingsFile(root), []byte(content), 0o600))
}

func readSettings(t *testing.T, root string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(settingsFile(root))
	require.NoError(t, err)
	var out map[string]any
	require.NoError(t, json.Unmarshal(data, &out))
	return out
}

func allowList(t *testing.T, root string) []any {
	t.Helper()
	perms, ok := readSettings(t, root)["permissions"].(map[string]any)
	require.True(t, ok)
	allow, ok := perms["allow"].([]any)
	require.True(t, ok)
	return allow
}

func asAny(rules ...string) []any {
	out := make([]any, len(rules))
	for i, r := range rules {
		out[i] = r
	}
	return out
}

func TestGrantCreatesSettingsWithEveryRule(t *testing.T) {
	root := t.TempDir()
	added, err := Grant(root)
	require.NoError(t, err)
	assert.Equal(t, Rules, added)
	assert.Equal(t, asAny(Rules...), allowList(t, root))
}

func TestGrantKeepsOtherSettingsAndAddsOnlyMissing(t *testing.T) {
	root := t.TempDir()
	writeSettings(t, root, `{"env":{"A":"1"},"permissions":{"allow":["Read","`+Rules[0]+`"]}}`)
	added, err := Grant(root)
	require.NoError(t, err)
	assert.Equal(t, Rules[1:], added)
	assert.Equal(t, map[string]any{"A": "1"}, readSettings(t, root)["env"])
	assert.Equal(t, asAny(append([]string{"Read"}, Rules...)...), allowList(t, root))
}

func TestGrantIsIdempotent(t *testing.T) {
	root := t.TempDir()
	_, err := Grant(root)
	require.NoError(t, err)
	added, err := Grant(root)
	require.NoError(t, err)
	assert.Empty(t, added)
}

func TestGrantNeverAddsDevRules(t *testing.T) {
	for _, rule := range Rules {
		assert.NotContains(t, rule, "beadle-dev")
	}
}

func TestRulesAreLeastPrivilegeAndScoped(t *testing.T) {
	entries, err := filepath.Glob(filepath.Join("..", "..", "plugin", "commands", "*.md"))
	require.NoError(t, err)
	var want []string
	for _, path := range entries {
		name := strings.TrimSuffix(filepath.Base(path), ".md")
		if !strings.HasSuffix(name, "-dev") {
			want = append(want, "Skill(beadle:"+name+")")
		}
	}
	var skills []string
	for _, rule := range Rules {
		if strings.HasPrefix(rule, "Skill(") {
			skills = append(skills, rule)
		}
	}
	require.NotEmpty(t, want)
	assert.Equal(t, want, skills)
	assert.NotContains(t, Rules, "Bash(beadle-email:*)")
	assert.NotContains(t, Rules, "mcp__plugin_beadle_email__*")
	for _, verb := range []string{"serve", "install", "uninstall", "enable", "disable", "identity set", "contact add", "contact remove"} {
		for _, rule := range Rules {
			assert.False(t, strings.HasPrefix(rule, "Bash(beadle-email "+verb+":"), rule)
		}
	}
}

func TestGrantRefusesMalformedSettings(t *testing.T) {
	for _, content := range []string{`{not json`, `[]`, `{"permissions":[]}`, `{"permissions":{"allow":"x"}}`, "{\"a\":\"\xff\"}"} {
		root := t.TempDir()
		writeSettings(t, root, content)
		_, err := Grant(root)
		require.ErrorIs(t, err, ErrRefused, content)
		data, readErr := os.ReadFile(settingsFile(root))
		require.NoError(t, readErr)
		assert.Equal(t, content, string(data))
	}
}

func TestGrantRefusesSymlinkedSettingsFile(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "elsewhere.json")
	require.NoError(t, os.WriteFile(target, []byte("{}"), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".claude"), 0o750))
	require.NoError(t, os.Symlink(target, settingsFile(root)))
	_, err := Grant(root)
	require.ErrorIs(t, err, ErrRefused)
	data, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "{}", string(data))
}

func TestGrantRefusesSymlinkedClaudeDir(t *testing.T) {
	base := t.TempDir()
	outside := filepath.Join(base, "outside")
	repo := filepath.Join(base, "repo")
	require.NoError(t, os.MkdirAll(outside, 0o750))
	require.NoError(t, os.MkdirAll(repo, 0o750))
	require.NoError(t, os.Symlink(outside, filepath.Join(repo, ".claude")))
	_, err := Grant(repo)
	require.ErrorIs(t, err, ErrRefused)
	entries, err := os.ReadDir(outside)
	require.NoError(t, err)
	assert.Empty(t, entries)
}

func TestGrantRefusesHomeDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	_, err := Grant(home)
	require.ErrorIs(t, err, ErrRefused)
	_, statErr := os.Stat(filepath.Join(home, ".claude"))
	assert.True(t, os.IsNotExist(statErr))
}

func TestRevokeRemovesExactlyBeadleRules(t *testing.T) {
	root := t.TempDir()
	_, err := Grant(root)
	require.NoError(t, err)
	data, err := json.Marshal(map[string]any{"permissions": map[string]any{"allow": append(asAny("Read"), append(asAny(Rules...), "Bash(x)")...)}})
	require.NoError(t, err)
	writeSettings(t, root, string(data))
	removed, _, err := Revoke(root)
	require.NoError(t, err)
	assert.Equal(t, Rules, removed)
	assert.Equal(t, asAny("Read", "Bash(x)"), allowList(t, root))
}

func TestRevokeWithoutSettingsCreatesNothing(t *testing.T) {
	root := t.TempDir()
	removed, _, err := Revoke(root)
	require.NoError(t, err)
	assert.Empty(t, removed)
	_, statErr := os.Stat(filepath.Join(root, ".claude"))
	assert.True(t, os.IsNotExist(statErr))
}

func TestGrantSurvivesClaudeSwappedForSymlinkDuringLockWait(t *testing.T) {
	base := t.TempDir()
	repo := filepath.Join(base, "repo")
	outside := filepath.Join(base, "outside")
	moved := filepath.Join(base, "moved")
	require.NoError(t, os.MkdirAll(outside, 0o750))
	writeSettings(t, repo, `{"env":{"A":"1"}}`)

	// Hold the lock so Grant passes its preflight and then blocks on the flock.
	c, err := os.OpenRoot(filepath.Join(repo, ".claude"))
	require.NoError(t, err)
	defer func() { _ = c.Close() }()
	release := make(chan struct{})
	held := make(chan struct{})
	go func() {
		_ = withLock(c, func() error { close(held); <-release; return nil })
	}()
	<-held

	done := make(chan error, 1)
	go func() { _, err := Grant(repo); done <- err }()
	// Blocking on the flock is not observable; give Grant time to reach it. The
	// swap below is safe whether or not it has.
	select {
	case err := <-done:
		t.Fatalf("Grant returned while the lock was held: %v", err)
	case <-time.After(200 * time.Millisecond):
	}

	// Swap .claude for a symlink to a directory holding its own settings.
	require.NoError(t, os.WriteFile(filepath.Join(outside, "settings.json"), []byte(`{"victim":true}`), 0o600))
	require.NoError(t, os.Rename(filepath.Join(repo, ".claude"), moved))
	require.NoError(t, os.Symlink(outside, filepath.Join(repo, ".claude")))
	close(release)
	require.NoError(t, <-done, "Grant holds the original directory and succeeds there")

	data, err := os.ReadFile(filepath.Join(outside, "settings.json"))
	require.NoError(t, err)
	assert.Equal(t, `{"victim":true}`, string(data), "the symlink target is never written")
	entries, err := os.ReadDir(outside)
	require.NoError(t, err)
	assert.Len(t, entries, 1, "nothing is created in the symlink target")

	data, err = os.ReadFile(filepath.Join(moved, "settings.json"))
	require.NoError(t, err)
	var got struct {
		Env         map[string]string `json:"env"`
		Permissions struct {
			Allow []string `json:"allow"`
		} `json:"permissions"`
	}
	require.NoError(t, json.Unmarshal(data, &got))
	assert.Equal(t, map[string]string{"A": "1"}, got.Env, "pre-existing content is kept")
	assert.Equal(t, Rules, got.Permissions.Allow, "the rules landed in the moved original")
}

func TestGrantRefusesSymlinkedLockFile(t *testing.T) {
	base := t.TempDir()
	repo := filepath.Join(base, "repo")
	outsideLock := filepath.Join(base, "outside.lock")
	writeSettings(t, repo, `{}`)
	require.NoError(t, os.Symlink(outsideLock, filepath.Join(repo, ".claude", lockName)))

	_, err := Grant(repo)
	require.Error(t, err)
	_, statErr := os.Stat(outsideLock)
	assert.True(t, os.IsNotExist(statErr), "O_CREATE must not follow the lock symlink out of the repo")
	data, err := os.ReadFile(settingsFile(repo))
	require.NoError(t, err)
	assert.Equal(t, `{}`, string(data))
}

func TestEditsPreserveUnrelatedBytes(t *testing.T) {
	const big = "9007199254740993"
	content := "{\n  \"zeta\": " + big + ",\n  \"alpha\": 1.0e2,\n  \"permissions\": {\"deny\": [\"Bash(rm:*)\"], \"allow\": [\"Bash(make && test)\", \"<&>\"]},\n  \"a\": \"x\\u0026y\"\n}\n"
	root := t.TempDir()
	writeSettings(t, root, content)

	_, err := Grant(root)
	require.NoError(t, err)
	data, err := os.ReadFile(settingsFile(root))
	require.NoError(t, err)
	got := string(data)
	assert.Contains(t, got, big, "large integer survives byte-for-byte")
	assert.Contains(t, got, "1.0e2")
	assert.Contains(t, got, `"Bash(make && test)"`, "HTML characters are not re-escaped")
	assert.Contains(t, got, `"<&>"`)
	assert.Contains(t, got, `x\u0026y`, "an unrelated string's escaping is kept as written")
	assert.Less(t, strings.Index(got, `"zeta"`), strings.Index(got, `"alpha"`), "key order kept")
	assert.Less(t, strings.Index(got, `"alpha"`), strings.Index(got, `"permissions"`))
	assert.Less(t, strings.Index(got, `"deny"`), strings.Index(got, `"allow"`))

	_, _, err = Revoke(root)
	require.NoError(t, err)
	data, err = os.ReadFile(settingsFile(root))
	require.NoError(t, err)
	got = string(data)
	assert.Contains(t, got, big)
	assert.Contains(t, got, `"Bash(make && test)"`)
	assert.NotContains(t, got, Rules[0])
}

func TestRevokeKeepsRulesPresentBeforeGrant(t *testing.T) {
	root := t.TempDir()
	writeSettings(t, root, `{"permissions":{"allow":["Skill(beadle:mail)"]}}`)
	added, err := Grant(root)
	require.NoError(t, err)
	assert.NotContains(t, added, "Skill(beadle:mail)")

	removed, _, err := Revoke(root)
	require.NoError(t, err)
	assert.NotContains(t, removed, "Skill(beadle:mail)")
	assert.Equal(t, asAny("Skill(beadle:mail)"), allowList(t, root), "the pre-existing rule survives disable")
	_, statErr := os.Stat(filepath.Join(root, ".claude", grantedName))
	assert.True(t, os.IsNotExist(statErr), "record removed once nothing remains granted")
}

func TestRevokeWithoutRecordRemovesNothing(t *testing.T) {
	root := t.TempDir()
	writeSettings(t, root, `{"permissions":{"allow":["Skill(beadle:mail)"]}}`)
	removed, unrecorded, err := Revoke(root)
	require.NoError(t, err)
	assert.Empty(t, removed)
	assert.Equal(t, []string{"Skill(beadle:mail)"}, unrecorded, "a beadle rule no record explains is reported, not removed")
	assert.Equal(t, asAny("Skill(beadle:mail)"), allowList(t, root))
}

func TestRevokeWithRecordStaysQuietAboutOtherRules(t *testing.T) {
	root := t.TempDir()
	writeSettings(t, root, `{"permissions":{"allow":["Read","Skill(beadle:mail)"]}}`)
	_, err := Grant(root)
	require.NoError(t, err)
	_, unrecorded, err := Revoke(root)
	require.NoError(t, err)
	assert.Empty(t, unrecorded)
}

func TestGrantRefusesWhenHomeUnknown(t *testing.T) {
	t.Setenv("HOME", "")
	_, err := Grant(t.TempDir())
	require.ErrorIs(t, err, ErrRefused)
}

func TestRefusalToRevokeTellsOperatorToEditByHand(t *testing.T) {
	root := t.TempDir()
	writeSettings(t, root, `{not json`)
	_, _, err := Revoke(root)
	require.ErrorIs(t, err, ErrRefused)
	assert.Contains(t, err.Error(), "by hand")
}

func TestRemoveRollsBackOnlyTheGivenRules(t *testing.T) {
	root := t.TempDir()
	added, err := Grant(root)
	require.NoError(t, err)
	removed, err := Remove(root, added[:2])
	require.NoError(t, err)
	assert.Equal(t, added[:2], removed)
	assert.Equal(t, asAny(added[2:]...), allowList(t, root))
	again, _, err := Revoke(root)
	require.NoError(t, err)
	assert.Equal(t, added[2:], again, "the record kept the rest")
}

func TestPreflightWritesNothing(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, Preflight(root))
	_, statErr := os.Stat(filepath.Join(root, ".claude"))
	assert.True(t, os.IsNotExist(statErr))

	bad := t.TempDir()
	writeSettings(t, bad, `{not json`)
	require.ErrorIs(t, Preflight(bad), ErrRefused)
}

func TestGrantWritesRecordAndFreshRevokeUsesIt(t *testing.T) {
	root := t.TempDir()
	writeSettings(t, root, `{"permissions":{"allow":["Skill(beadle:mail)","Read"]}}`)
	added, err := Grant(root)
	require.NoError(t, err)

	record, err := os.ReadFile(filepath.Join(root, ".claude", grantedName))
	require.NoError(t, err, "the record sits next to settings.json")
	var recorded []string
	require.NoError(t, json.Unmarshal(record, &recorded))
	assert.Equal(t, added, recorded)

	// A collaborator's clone carries only the committed files; a fresh Revoke
	// must read the record and remove exactly the granted set.
	clone := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(clone, ".claude"), 0o750))
	for _, name := range []string{settingsName, grantedName} {
		data, readErr := os.ReadFile(filepath.Join(root, ".claude", name))
		require.NoError(t, readErr)
		require.NoError(t, os.WriteFile(filepath.Join(clone, ".claude", name), data, 0o600))
	}
	removed, _, err := Revoke(clone)
	require.NoError(t, err)
	assert.Equal(t, added, removed, "exactly the granted set, no more and no fewer")
	assert.Equal(t, asAny("Skill(beadle:mail)", "Read"), allowList(t, clone))
}
