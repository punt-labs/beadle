package email_test

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/punt-labs/punt-observe-go/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/punt-labs/beadle/internal/email"
	"github.com/punt-labs/beadle/internal/testserver"
)

func TestDialAuthenticationDiagnostics(t *testing.T) {
	for _, success := range []bool{true, false} {
		name := "failure"
		if success {
			name = "success"
		}
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			fixture := testserver.NewFixture(t)
			if !success {
				fixture.Config.TestPassword = "wrong-password"
			}
			handler, err := logging.New("beadle", "cli", logging.WithLevel(slog.LevelDebug))
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, handler.Close()) })
			client, err := email.Dial(fixture.Config, slog.New(handler))
			if success {
				require.NoError(t, err)
				require.NoError(t, client.Close())
			} else {
				require.Error(t, err)
			}
			data, err := os.ReadFile(filepath.Join(home, ".punt-labs", "beadle", "logs", "beadle.log"))
			require.NoError(t, err)
			text := string(data)
			assert.Equal(t, 1, strings.Count(text, "\n"))
			if success {
				assert.Contains(t, text, "[DEBUG] beadle.email: IMAP connection authenticated")
			} else {
				assert.Contains(t, text, "[WARNING] beadle.email: IMAP authentication failed")
				assert.Contains(t, text, "invalid credentials")
			}
			assert.NotContains(t, text, fixture.Config.IMAPUser)
			assert.NotContains(t, text, fixture.Config.TestPassword)
		})
	}
}
