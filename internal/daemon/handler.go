// Package daemon implements the mail-triggered mission pipeline for beadle-daemon.
package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"

	"github.com/punt-labs/beadle/internal/channel"
	"github.com/punt-labs/beadle/internal/contacts"
	"github.com/punt-labs/beadle/internal/email"
	"github.com/punt-labs/beadle/internal/identity"
	"github.com/punt-labs/beadle/internal/paths"
	"github.com/punt-labs/beadle/internal/pgp"
)

// MissionCreator creates a mission from email metadata and returns the mission ID.
type MissionCreator interface {
	Create(meta EmailMeta) (string, error)
}

// MailHandler processes new mail notifications from the poller.
// For each unread message, it checks the sender's x-bit permission,
// creates an Executor pipeline, and runs it.
type MailHandler struct {
	resolver  *identity.Resolver
	dialer    email.Dialer
	missions  MissionCreator
	spawner   Spawner
	templates *MissionTemplate
	planner   Planner
	commands  map[string]*Command
	logger    *slog.Logger

	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	workerSem chan struct{} // limits concurrent workers
}

// NewMailHandler creates a MailHandler. If spawner or templates is nil,
// mission creation still works but no worker is spawned.
// maxWorkers sets the concurrency limit for worker goroutines (default 2).
// planner and commands configure the pipeline executor; if planner is nil,
// a StubPlanner is used that returns a single generic CommandCall.
// The returned context governs worker subprocess lifetimes — call Stop
// to cancel running workers and wait for them to exit.
func NewMailHandler(ctx context.Context, resolver *identity.Resolver, dialer email.Dialer, missions MissionCreator, spawner Spawner, templates *MissionTemplate, logger *slog.Logger, maxWorkers int, planner Planner, commands map[string]*Command) *MailHandler {
	if maxWorkers <= 0 {
		maxWorkers = 2
	}
	if planner == nil {
		planner = &StubPlanner{Err: fmt.Errorf("no planner configured")}
	}
	if commands == nil {
		commands = make(map[string]*Command)
	}
	ctx, cancel := context.WithCancel(ctx)
	return &MailHandler{
		resolver:  resolver,
		dialer:    dialer,
		missions:  missions,
		spawner:   spawner,
		templates: templates,
		planner:   planner,
		commands:  commands,
		logger:    logger,
		ctx:       ctx,
		cancel:    cancel,
		workerSem: make(chan struct{}, maxWorkers),
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

		// Verify transport trust before creating a mission.
		// Accept: Verified (PGP-signed) or Trusted (Proton E2E via Bridge).
		// Proton E2E headers are safe when IMAP source is Proton Bridge on
		// localhost — Bridge controls these headers for internal messages.
		// External SMTP injection of these headers is blocked by Bridge.
		// TODO(beadle-xxx): verify IMAP is localhost as additional guard.
		trust := h.verifyTrust(client, cfg, msg, contact)
		if trust != channel.Verified && trust != channel.Trusted {
			h.logger.Warn("skip message: insufficient transport trust",
				"from", addr, "trust", trust, "id", msg.ID)
			continue
		}

		meta := EmailMeta{
			MessageID: msg.ID,
			From:      msg.From,
			Subject:   msg.Subject,
		}

		if h.spawner != nil && h.templates != nil {
			select {
			case h.workerSem <- struct{}{}:
				h.wg.Add(1)
				go func() {
					defer h.wg.Done()
					defer func() { <-h.workerSem }()
					executor := &Executor{
						Planner:   h.planner,
						Commands:  h.commands,
						Missions:  h.missions,
						Spawner:   h.spawner,
						Templates: h.templates,
						Registry:  DefaultMCPRegistry(),
						Logger:    h.logger,
					}
					p, err := executor.Run(h.ctx, meta, "")
					if err != nil {
						h.logger.Error("pipeline failed",
							"pipeline", p.ID, "from", addr,
							"id", msg.ID, "error", err)
						return
					}
					h.logger.Info("pipeline completed",
						"pipeline", p.ID, "from", addr,
						"subject", truncateLog(msg.Subject, 200),
						"stages", len(p.Results))
				}()
			default:
				h.logger.Warn("worker capacity full, skipping", "from", addr, "id", msg.ID)
			}
		} else {
			missionID, err := h.missions.Create(meta)
			if err != nil {
				h.logger.Error("create mission", "from", addr, "id", msg.ID, "error", err)
				continue
			}
			h.logger.Info("mission created (no spawner)", "mission", missionID, "from", addr)
		}
	}
}

// verifyTrust determines the transport trust level for a message.
// Proton headers are SMTP-injectable; only PGP verification provides
// cryptographic proof of sender identity for x-bit execution.
// If the contact has a GPGKeyID set, the signing key must match.
func (h *MailHandler) verifyTrust(client *email.Client, cfg *email.Config, msg channel.MessageSummary, contact contacts.Contact) channel.TrustLevel {
	// Proton E2E: Bridge sets these headers for internal messages only.
	// Safe when IMAP source is Bridge on localhost (Bridge controls headers).
	// TODO: verify IMAP host is loopback as additional guard.
	if msg.TrustLevel == channel.Trusted {
		return channel.Trusted
	}
	if !msg.HasSig {
		return channel.Unverified
	}

	// PGP signature present — fetch raw and verify.
	uid, err := strconv.ParseUint(msg.ID, 10, 32)
	if err != nil {
		h.logger.Error("parse message uid", "id", msg.ID, "error", err)
		return channel.Unverified
	}
	raw, err := client.FetchRaw("INBOX", uint32(uid))
	if err != nil {
		h.logger.Error("fetch raw for pgp verify", "id", msg.ID, "error", err)
		return channel.Unverified
	}
	result, err := pgp.Verify(cfg.GPGBinary, raw)
	if err != nil {
		h.logger.Warn("pgp verify failed", "id", msg.ID, "error", err)
		return channel.Untrusted
	}
	if !result.Valid {
		return channel.Untrusted
	}

	// If the contact has a registered GPG key, the signing key must match.
	// Normalize: strip 0x prefix, case-insensitive suffix match.
	normalizeKeyID := func(id string) string {
		id = strings.ToUpper(strings.TrimSpace(id))
		id = strings.TrimPrefix(id, "0X")
		return id
	}
	if contact.GPGKeyID != "" && !strings.HasSuffix(normalizeKeyID(result.KeyID), normalizeKeyID(contact.GPGKeyID)) {
		h.logger.Warn("pgp key mismatch",
			"id", msg.ID,
			"expected", contact.GPGKeyID,
			"got", result.KeyID)
		return channel.Untrusted
	}

	return channel.Verified
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
