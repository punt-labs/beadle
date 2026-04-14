package daemon

import (
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/punt-labs/beadle/internal/testenv"
	"github.com/punt-labs/beadle/internal/testserver"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// mockMissionCreator records Create calls for test assertions.
type mockMissionCreator struct {
	calls []EmailMeta
}

func (m *mockMissionCreator) Create(meta EmailMeta) (string, error) {
	m.calls = append(m.calls, meta)
	return "m-test-" + meta.MessageID, nil
}

func TestOnNewMail(t *testing.T) {
	tests := []struct {
		name         string
		messages     []testMsg
		contacts     []testContact
		wantMissions int
		wantSubjects []string
	}{
		{
			name: "rwx sender creates mission",
			messages: []testMsg{
				{from: "jim@punt-labs.com", subject: "Schedule meeting"},
			},
			contacts: []testContact{
				{name: "Jim", addr: "jim@punt-labs.com", perm: "rwx"},
			},
			wantMissions: 1,
			wantSubjects: []string{"Schedule meeting"},
		},
		{
			name: "rw- sender skipped",
			messages: []testMsg{
				{from: "bob@example.com", subject: "Hello"},
			},
			contacts: []testContact{
				{name: "Bob", addr: "bob@example.com", perm: "rw-"},
			},
			wantMissions: 0,
		},
		{
			name: "unknown sender skipped",
			messages: []testMsg{
				{from: "stranger@example.com", subject: "Spam"},
			},
			contacts: []testContact{
				{name: "Jim", addr: "jim@punt-labs.com", perm: "rwx"},
			},
			wantMissions: 0,
		},
		{
			name: "mixed: one rwx, one rw-, one unknown",
			messages: []testMsg{
				{from: "jim@punt-labs.com", subject: "Do this"},
				{from: "bob@example.com", subject: "Read this"},
				{from: "stranger@example.com", subject: "Buy now"},
			},
			contacts: []testContact{
				{name: "Jim", addr: "jim@punt-labs.com", perm: "rwx"},
				{name: "Bob", addr: "bob@example.com", perm: "rw-"},
			},
			wantMissions: 1,
			wantSubjects: []string{"Do this"},
		},
		{
			name: "r-x sender creates mission",
			messages: []testMsg{
				{from: "ops@example.com", subject: "Deploy"},
			},
			contacts: []testContact{
				{name: "Ops", addr: "ops@example.com", perm: "r-x"},
			},
			wantMissions: 1,
			wantSubjects: []string{"Deploy"},
		},
		{
			name:         "no messages",
			messages:     nil,
			contacts:     nil,
			wantMissions: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := testenv.New(t, "test@test.com")
			fix := testserver.NewFixture(t)
			env.WriteConfig(fix.Config)
			dialer := testserver.TestDialer{Password: "testpass"}

			for _, c := range tt.contacts {
				env.AddContact(c.name, c.addr, c.perm)
			}

			for _, m := range tt.messages {
				fix.AddMessage("INBOX", m.from, m.subject, "body")
			}

			mock := &mockMissionCreator{}
			handler := NewMailHandler(env.Resolver, dialer, mock, discardLogger())

			handler.OnNewMail(uint32(len(tt.messages)))

			assert.Equal(t, tt.wantMissions, len(mock.calls), "mission count")

			for i, want := range tt.wantSubjects {
				require.Greater(t, len(mock.calls), i, "not enough missions created")
				assert.Equal(t, want, mock.calls[i].Subject)
			}
		})
	}
}

type testMsg struct {
	from    string
	subject string
}

type testContact struct {
	name string
	addr string
	perm string
}
