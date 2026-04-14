// Package daemon implements the mail-triggered mission pipeline for beadle-daemon.
package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"

	"github.com/punt-labs/beadle/internal/contacts"
	"github.com/punt-labs/beadle/internal/email"
	"github.com/punt-labs/beadle/internal/identity"
	"github.com/punt-labs/beadle/internal/paths"
)

// MissionCreator creates a mission from email metadata and returns the mission ID.
type MissionCreator interface {
	Create(meta EmailMeta) (string, error)
}

// MailHandler processes new mail notifications from the poller.
// For each unread message, it checks the sender's x-bit permission,
// creates an ethos mission, and spawns a Claude Code worker to execute it.
type MailHandler struct {
	resolver  *identity.Resolver
	dialer    email.Dialer
	missions  MissionCreator
	spawner   *WorkerSpawner
	templates *MissionTemplate
	logger    *slog.Logger

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewMailHandler creates a MailHandler. If spawner or templates is nil,
// mission creation still works but no worker is spawned.
// The returned context governs worker subprocess lifetimes — call Stop
// to cancel running workers and wait for them to exit.
func NewMailHandler(ctx context.Context, resolver *identity.Resolver, dialer email.Dialer, missions MissionCreator, spawner *WorkerSpawner, templates *MissionTemplate, logger *slog.Logger) *MailHandler {
	ctx, cancel := context.WithCancel(ctx)
	return &MailHandler{
		resolver:  resolver,
		dialer:    dialer,
		missions:  missions,
		spawner:   spawner,
		templates: templates,
		logger:    logger,
		ctx:       ctx,
		cancel:    cancel,
	}
}

// Stop cancels all running workers and waits for them to exit.
func (h *MailHandler) Stop() {
	h.cancel()
	h.wg.Wait()
}

// OnNewMail is the poller callback. It lists recent unread messages,
// checks each sender's x-bit, and creates missions for authorized senders.
func (h *MailHandler) OnNewMail(newCount uint32) {
	id, err := h.resolver.Resolve()
	if err != nil {
		h.logger.Error("resolve identity", "error", err)
		return
	}

	cfg, err := h.loadConfig(id.Email)
	if err != nil {
		h.logger.Error("load email config", "error", err)
		return
	}

	client, err := h.dialer.Dial(cfg, h.logger)
	if err != nil {
		h.logger.Error("dial imap", "error", err)
		return
	}
	defer func() {
		if err := client.Close(); err != nil {
			h.logger.Debug("close imap", "error", err)
		}
	}()

	// Cap fetch count to avoid flooding the mission creator on first run
	// when the inbox has many pre-existing unread messages.
	fetchCount := int(newCount)
	if fetchCount > 20 {
		fetchCount = 20
		h.logger.Warn("capping message fetch", "newCount", newCount, "cap", fetchCount)
	}
	result, err := client.ListMessages("INBOX", fetchCount, true)
	if err != nil {
		h.logger.Error("list messages", "error", err)
		return
	}

	store, err := h.loadContacts(id.Email)
	if err != nil {
		h.logger.Error("load contacts", "error", err)
		return
	}

	for _, msg := range result.Messages {
		addr := email.ExtractEmailAddress(msg.From)
		if addr == "" {
			h.logger.Warn("skip message: no sender address", "id", msg.ID)
			continue
		}

		contact, found := store.FindByAddress(addr)
		if !found {
			h.logger.Info("skip message: unknown sender", "from", addr, "id", msg.ID)
			continue
		}

		perm := contacts.CheckPermission(contact, id.Email)
		if !perm.Execute {
			h.logger.Info("skip message: sender lacks x permission",
				"from", addr, "contact", contact.Name, "perm", perm.String(), "id", msg.ID)
			continue
		}

		meta := EmailMeta{
			MessageID: msg.ID,
			From:      msg.From,
			Subject:   msg.Subject,
		}

		missionID, err := h.missions.Create(meta)
		if err != nil {
			h.logger.Error("create mission", "from", addr, "id", msg.ID, "error", err)
			continue
		}
		h.logger.Info("mission created", "mission", missionID, "from", addr, "subject", msg.Subject)

		if h.spawner != nil && h.templates != nil {
			h.wg.Add(1)
			go func() {
				defer h.wg.Done()
				h.spawnWorker(h.ctx, missionID)
			}()
		}
	}
}

func (h *MailHandler) spawnWorker(ctx context.Context, missionID string) {
	mcpPath, err := h.templates.BuildMCPConfig()
	if err != nil {
		h.logger.Error("build mcp config", "mission", missionID, "error", err)
		return
	}

	promptPath, err := h.templates.BuildSystemPrompt(missionID)
	if err != nil {
		os.Remove(mcpPath)
		h.logger.Error("build system prompt", "mission", missionID, "error", err)
		return
	}

	result, err := h.spawner.Run(ctx, missionID, mcpPath, promptPath)
	// Clean up temp files after subprocess exits.
	os.Remove(mcpPath)
	os.Remove(promptPath)

	if err != nil {
		h.logger.Error("spawn worker", "mission", missionID, "error", err)
		return
	}
	h.logger.Info("worker completed",
		"mission", missionID,
		"session", result.SessionID,
		"isError", result.IsError,
		"exitCode", result.ExitCode)
}

func (h *MailHandler) loadConfig(identityEmail string) (*email.Config, error) {
	cfgPath, err := paths.IdentityConfigPath(identityEmail)
	if err != nil {
		return nil, fmt.Errorf("identity config path: %w", err)
	}
	cfg, err := email.LoadConfig(cfgPath)
	if err != nil {
		return nil, fmt.Errorf("load config %s: %w", cfgPath, err)
	}
	return cfg, nil
}

func (h *MailHandler) loadContacts(identityEmail string) (*contacts.Store, error) {
	contactsPath, err := paths.IdentityContactsPath(identityEmail)
	if err != nil {
		return nil, fmt.Errorf("contacts path: %w", err)
	}
	store := contacts.NewStore(contactsPath)
	if err := store.Load(); err != nil {
		return nil, fmt.Errorf("load contacts: %w", err)
	}
	return store, nil
}
