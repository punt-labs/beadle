package daemon

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/punt-labs/punt-observe-go/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/punt-labs/beadle/internal/testenv"
	"github.com/punt-labs/beadle/internal/testserver"
	"github.com/punt-labs/beadle/internal/usage"
)

type failingUsageMissionCreator struct{}

func (failingUsageMissionCreator) Create(EmailMeta) (string, error) {
	return "", fmt.Errorf("private mission output")
}

func TestNewMailUsageDecisions(t *testing.T) {
	for _, outcome := range []usage.Decision{usage.Created, usage.BelowTrust, usage.NoPermission, usage.NoCommand, usage.Failed} {
		t.Run(string(outcome), func(t *testing.T) {
			env := testenv.New(t, "agent@example.com")
			t.Setenv("PUNT_TELEMETRY", "")
			t.Setenv("BEADLE_TELEMETRY", "")
			fixture := testserver.NewFixture(t)
			env.WriteConfig(fixture.Config)
			env.AddContact("Private Display Name", "sender@example.com", "rwx")
			switch outcome {
			case usage.BelowTrust:
				fixture.AddMessage("INBOX", "sender@example.com", "Private Subject", "Private Body")
			case usage.NoPermission:
				fixture.AddMessage("INBOX", "unknown@example.com", "Private Subject", "Private Body")
			default:
				fixture.AddRawMessage("INBOX", buildTrustedRFC822("sender@example.com", "Private Subject", "Private Body"))
			}
			var creator MissionCreator = &mockMissionCreator{}
			if outcome == usage.Failed {
				creator = failingUsageMissionCreator{}
			}
			var spawner Spawner
			var templates *MissionTemplate
			var planner Planner
			if outcome == usage.NoCommand {
				spawner = &WorkerSpawner{}
				templates = &MissionTemplate{}
				planner = &StubPlanner{Err: ErrNoCommand}
			}
			client := usage.New(nil)
			t.Cleanup(client.Shutdown)
			logHandler, err := logging.New("beadle", "daemon")
			require.NoError(t, err)
			handler := NewMailHandler(t.Context(), env.Resolver, testserver.TestDialer{Password: "testpass"}, creator, spawner, templates,
				slog.New(logHandler), 0, planner, nil, nil, WithUsage(client))
			handler.OnNewMail(1)
			handler.Stop()
			client.Shutdown()
			require.NoError(t, logHandler.Close())
			home, err := os.UserHomeDir()
			require.NoError(t, err)
			diagnostic, err := os.ReadFile(filepath.Join(home, ".punt-labs", "beadle", "logs", "beadle.log"))
			require.NoError(t, err)
			assert.NotContains(t, string(diagnostic), "[unsupported value]")
			if outcome == usage.BelowTrust {
				assert.Contains(t, string(diagnostic), "trust=unverified")
			}
			db, err := sql.Open("sqlite", "file:"+filepath.Join(home, ".punt-labs", "beadle", "telemetry.db")+"?mode=ro")
			require.NoError(t, err)
			defer func() { require.NoError(t, db.Close()) }()
			var count int
			require.NoError(t, db.QueryRowContext(t.Context(), "SELECT count(*) FROM signals").Scan(&count))
			assert.Equal(t, 1, count)
			var data string
			require.NoError(t, db.QueryRowContext(t.Context(), "SELECT attributes FROM signals WHERE name='beadle.daemon.mission'").Scan(&data))
			var attrs struct{ Outcome usage.Decision }
			require.NoError(t, json.Unmarshal([]byte(data), &attrs))
			assert.Equal(t, outcome, attrs.Outcome)
			for _, content := range []string{"example.com", "Private", "private", "m-test"} {
				assert.NotContains(t, data, content)
			}
		})
	}
}
