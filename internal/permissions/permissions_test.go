package permissions

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

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
	removed, err := Revoke(root)
	require.NoError(t, err)
	assert.Equal(t, Rules, removed)
	assert.Equal(t, asAny("Read", "Bash(x)"), allowList(t, root))
}

func TestRevokeWithoutSettingsCreatesNothing(t *testing.T) {
	root := t.TempDir()
	removed, err := Revoke(root)
	require.NoError(t, err)
	assert.Empty(t, removed)
	_, statErr := os.Stat(filepath.Join(root, ".claude"))
	assert.True(t, os.IsNotExist(statErr))
}
