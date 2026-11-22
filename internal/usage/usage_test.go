package usage

import (
	"context"
	"database/sql"
	"encoding/json"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/punt-labs/punt-observe-go/telemetry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func freshClient(t *testing.T) (*Client, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PUNT_TELEMETRY", "")
	t.Setenv("BEADLE_TELEMETRY", "")
	c := New(nil)
	t.Cleanup(c.Shutdown)
	return c, filepath.Join(home, ".punt-labs", "beadle", "telemetry.db")
}

func TestSchemasAndTypedHelpers(t *testing.T) {
	c, path := freshClient(t)
	started := time.Now().Add(-time.Millisecond)
	want := 0
	for _, member := range schemas[0].Fields["command"].Members {
		for _, outcome := range []Outcome{OK, Error} {
			c.CLI(Command(member), outcome, started)
			want++
		}
	}
	for _, member := range schemas[1].Fields["tool"].Members {
		for _, outcome := range []Outcome{OK, Error, Denied} {
			ctx, finish := c.StartMCP(t.Context(), Tool(member))
			if outcome == Denied {
				PermissionDenied(ctx)
				finish(OK)
			} else {
				finish(outcome)
			}
			want++
		}
	}
	for _, outcome := range []Decision{Created, BelowTrust, NoPermission, NoCommand, Failed} {
		c.Mission(outcome, started)
		want++
	}
	c.CLI(Command("private subject"), OK, started)
	c.CLI("", OK, started)
	c.CLI(CommandVersion, Outcome("recipient@example.com"), started)
	c.CLI(CommandVersion, Denied, started)
	c.Mission(Decision("private body"), started)
	_, invalidTool := c.StartMCP(t.Context(), Tool("private-tool"))
	invalidTool(OK)
	for _, observation := range []telemetry.Observation{
		{Name: "beadle.cli.command", Version: 2},
		{Name: "beadle.cli.command", Version: 1},
		{Name: "beadle.cli.command", Version: 1, Attributes: []telemetry.Value{telemetry.Int("command", 1)}},
		{Name: "beadle.cli.command", Version: 1, Attributes: []telemetry.Value{telemetry.EnumValue("command", "version"), telemetry.EnumValue("outcome", "ok"), telemetry.Elapsed("elapsed_ms", time.Millisecond), telemetry.Items("body", 1)}},
	} {
		c.client.Record(observation)
	}
	c.Shutdown()
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	require.NoError(t, err)
	defer func() { require.NoError(t, db.Close()) }()
	rows, err := db.QueryContext(t.Context(), "SELECT name, schema_version, start_ns, end_ns, attributes FROM signals")
	require.NoError(t, err)
	defer func() { require.NoError(t, rows.Close()) }()
	got := 0
	for rows.Next() {
		var name, encoded string
		var version int
		var start, end int64
		require.NoError(t, rows.Scan(&name, &version, &start, &end, &encoded))
		assert.Equal(t, 1, version)
		assert.GreaterOrEqual(t, end, start)
		var attrs map[string]json.RawMessage
		require.NoError(t, json.Unmarshal([]byte(encoded), &attrs))
		var schema telemetry.Schema
		for _, candidate := range schemas {
			if candidate.Name == name {
				schema = candidate
			}
		}
		require.NotEmpty(t, schema.Name)
		assert.Len(t, attrs, len(schema.Fields))
		for key, field := range schema.Fields {
			require.Contains(t, attrs, key)
			switch field.Kind {
			case telemetry.Enum:
				var value telemetry.EnumMember
				require.NoError(t, json.Unmarshal(attrs[key], &value))
				assert.Contains(t, field.Members, value)
			case telemetry.Duration:
				var elapsed int64
				require.NoError(t, json.Unmarshal(attrs[key], &elapsed))
				assert.GreaterOrEqual(t, elapsed, int64(0))
			default:
				t.Fatalf("unexpected field kind %v", field.Kind)
			}
		}
		for _, content := range []string{"private", "recipient@example.com", "body"} {
			assert.NotContains(t, encoded, content)
		}
		got++
	}
	require.NoError(t, rows.Err())
	assert.Equal(t, want, got)
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestIndependentVetoes(t *testing.T) {
	for _, control := range []string{"PUNT_TELEMETRY", "BEADLE_TELEMETRY", "email.json", "daemon.json", "invalid config"} {
		t.Run(control, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("PUNT_TELEMETRY", "")
			t.Setenv("BEADLE_TELEMETRY", "")
			if strings.HasSuffix(control, ".json") || control == "invalid config" {
				dir := filepath.Join(home, ".punt-labs", "beadle")
				require.NoError(t, os.MkdirAll(dir, 0o700))
				data := []byte(`{"telemetry_disabled":true}`)
				name := control
				if control == "invalid config" {
					name = "email.json"
					data = []byte("{")
				}
				require.NoError(t, os.WriteFile(filepath.Join(dir, name), data, 0o600))
			} else {
				t.Setenv(control, "off")
			}
			c := New(nil)
			c.CLI(CommandVersion, OK, time.Now())
			c.Shutdown()
			_, err := os.Stat(filepath.Join(home, ".punt-labs", "beadle", "telemetry.db"))
			assert.True(t, os.IsNotExist(err))
		})
	}
}

func TestUnavailableStoreDoesNotChangeOperation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PUNT_TELEMETRY", "")
	t.Setenv("BEADLE_TELEMETRY", "")
	require.NoError(t, os.WriteFile(filepath.Join(home, ".punt-labs"), nil, 0o600))
	c := New(nil)
	started := time.Now()
	c.CLI(CommandVersion, OK, started)
	c.Shutdown()
	assert.Less(t, time.Since(started), 2*time.Second)
	var disabled *Client
	disabled.CLI(CommandVersion, OK, started)
	disabled.Mission(Failed, started)
	_, finish := disabled.StartMCP(context.Background(), ToolWhoami)
	finish(OK)
	disabled.Shutdown()
}

func TestImportBoundary(t *testing.T) {
	root := filepath.Clean("../..")
	require.NoError(t, filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if path != root && (strings.HasPrefix(entry.Name(), ".") || entry.Name() == "vendor" || entry.Name() == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		source, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		for _, spec := range source.Imports {
			imported, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return err
			}
			assert.False(t, strings.HasPrefix(imported, "go.opentelemetry.io/"), "%s imports raw OTel", relative)
			if imported == "github.com/punt-labs/punt-observe-go/telemetry" {
				assert.True(t, strings.HasPrefix(filepath.ToSlash(relative), "internal/usage/"), "%s imports telemetry outside usage", relative)
			}
		}
		return nil
	}))
}
