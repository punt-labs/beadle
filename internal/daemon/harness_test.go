package daemon_test

import (
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/punt-labs/beadle/internal/daemon"
	"github.com/punt-labs/beadle/internal/daemontest"
	"github.com/punt-labs/beadle/internal/testenv"
	"github.com/punt-labs/beadle/internal/testserver"
)

// This file is an external test package (package daemon_test), not the
// white-box `package daemon` convention every other file in this directory
// uses. It has to be: daemontest imports daemon (to build a *daemon.Command
// and implement daemon.Spawner), so any package-daemon test file that
// imported daemontest would close an import cycle -- confirmed by trial
// build, and documented on daemontest's package comment. TestOnNewMail's
// own gate-1/2 coverage in handler_test.go is unaffected and stays exactly
// where it is.

// TestOnNewMail_EndToEnd drives all four security-relevant gates on the
// daemon's mail-triggered pipeline together, through the real OnNewMail
// entry point -- the way beadle-8gt was actually found, by running the real
// path once and watching it break (docs/daemon-test-harness.md). Only
// daemon.Spawner (the real Claude Code worker subprocess) is faked, via
// daemontest.FakeSpawner; everything upstream -- trust classification,
// per-contact permission, command-file signature verification, and the
// real ethos CLI's contract validation -- runs for real.
//
// Poller is bypassed entirely: OnNewMail is called directly, then
// handler.Stop() blocks on the handler's own sync.WaitGroup for
// deterministic completion of the async worker-spawn goroutine -- no
// sleep, no polling loop. Poller's first-poll suppression and 1-minute
// minimum interval make it unusable for a pre-commit-gate test and carry
// no security-relevant behavior of their own; the gates all live inside
// OnNewMail and downstream.
func TestOnNewMail_EndToEnd(t *testing.T) {
	// Fail fast, before any setup, if either external dependency is
	// missing -- never t.Skip, per this tier's hard constraint.
	daemontest.EthosOrFatal(t)
	gpgBin, err := exec.LookPath("gpg")
	if err != nil {
		t.Fatalf("gpg not found on PATH: install it (apt install gnupg / brew install gnupg): %v", err)
	}

	tests := []struct {
		name             string
		messageSigned    bool // gate 1: false = unsigned RFC822 (negative case)
		contactPerm      string
		wantSpawnerCalls int
	}{
		{
			name:             "all four gates pass: pgp-verified rwx sender, signed command file, valid contract",
			messageSigned:    true,
			contactPerm:      "rwx",
			wantSpawnerCalls: 1,
		},
		{
			name:             "gate 1 negative: unsigned message from an rwx sender creates nothing",
			messageSigned:    false,
			contactPerm:      "rwx",
			wantSpawnerCalls: 0,
		},
		{
			name:             "gate 2 negative: pgp-signed message from an rw- sender creates nothing",
			messageSigned:    true,
			contactPerm:      "rw-",
			wantSpawnerCalls: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := testenv.New(t, "test@test.com")
			fix := testserver.NewFixture(t)
			env.WriteConfig(fix.Config)
			dialer := testserver.TestDialer{Password: "testpass"}

			env.AddContact("Jim", "jim@punt-labs.com", tt.contactPerm)

			if tt.messageSigned {
				raw, _ := daemontest.BuildPGPSignedMessage(t, gpgBin, "jim@punt-labs.com", "Run the test command", "body")
				fix.AddRawMessage("INBOX", raw)
			} else {
				fix.AddMessage("INBOX", "jim@punt-labs.com", "Run the test command", "body")
			}

			// Gate 3: a real command file, signed by a real owner key,
			// loaded through the real signature-verifying LoadCommands.
			// VerifySignature's importOwnerKey step exports the owner key
			// from the CALLER's ambient keyring (no --homedir on that one
			// exec.Command), so GNUPGHOME must point at ownerHome for the
			// export to find it -- matching every setup in
			// signature_test.go's TestVerifySignature.
			ownerHome := testenv.ShortGPGHome(t)
			const ownerEmail = "owner-e2e@example.com"
			ownerFpr := daemontest.GenOwnerKey(t, gpgBin, ownerHome, ownerEmail, "1y")
			t.Setenv("GNUPGHOME", ownerHome)

			fixture := &daemon.Command{
				Name:         "test-command",
				Runner:       "claude",
				Mode:         "passthrough",
				Prompt:       "Do the test task",
				OutputSchema: "text",
				WriteSet:     []string{"output/test-command.txt"},
			}
			fixture.Budget.Rounds = 1
			signed := daemontest.SignCommand(t, gpgBin, ownerHome, ownerEmail, fixture)

			commandsDir := t.TempDir()
			data, err := yaml.Marshal(signed)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(commandsDir, "test-command.yaml"), data, 0o600))

			var logBuf logCapture
			logger := slog.New(slog.NewTextHandler(&logBuf, nil))
			commands, err := daemon.LoadCommands(commandsDir, gpgBin, ownerFpr, logger)
			require.NoError(t, err)
			require.Contains(t, commands, "test-command", "signed command file must load and verify: %s", logBuf.String())

			// Gate 4: the real ethos CLI validates the generated stage
			// contract. testenv.New already isolated $HOME/$ETHOS_REPO_ROOT
			// above -- calling testenv.IsolateEthos again here would
			// re-redirect $HOME to a second, unrelated scratch directory
			// and strand the beadle identity/config env just wrote into
			// the first one.
			spawner := &daemontest.FakeSpawner{Result: daemon.WorkerResult{Output: "done"}}
			planner := &daemon.StubPlanner{Result: []daemon.CommandCall{{Command: "test-command", Args: map[string]any{}}}}
			templates := &daemon.MissionTemplate{TmpDir: t.TempDir()}

			handler := daemon.NewMailHandler(t.Context(), env.Resolver, dialer, nil, spawner, templates,
				slog.New(slog.NewTextHandler(io.Discard, nil)), 0, planner, commands, nil)
			handler.OnNewMail(1)
			handler.Stop()

			assert.Len(t, spawner.Calls(), tt.wantSpawnerCalls, "spawner call count")
		})
	}
}

// logCapture is a minimal io.Writer so LoadCommands' reject/skip log lines
// are available for a failure message, without needing package daemon's
// own unexported testLoggerCapture helper (unreachable from this external
// test package).
type logCapture struct {
	buf []byte
}

func (c *logCapture) Write(p []byte) (int, error) {
	c.buf = append(c.buf, p...)
	return len(p), nil
}

func (c *logCapture) String() string {
	return string(c.buf)
}
