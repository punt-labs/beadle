package email

import (
	"log/slog"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/punt-labs/beadle/internal/identity"
	"github.com/punt-labs/beadle/internal/paths"
)

// PollStatus is the current state of the background poller.
type PollStatus struct {
	Interval  string    `json:"interval"`
	Active    bool      `json:"active"`
	LastCheck time.Time `json:"last_check,omitempty"`
	Unseen    uint32    `json:"unseen"`
}

// Poller checks INBOX for new messages and sends tools/list_changed
// notifications when the unread count increases.
type Poller struct {
	server   *server.MCPServer
	resolver *identity.Resolver
	logger   *slog.Logger
	dialer   Dialer

	mu        sync.Mutex
	interval  time.Duration
	raw       string // original interval string for Status()
	lastSeen  uint32
	lastCheck time.Time
	stopCh    chan struct{}
}

// NewPoller creates a poller that is initially stopped.
func NewPoller(s *server.MCPServer, resolver *identity.Resolver, logger *slog.Logger, dialer Dialer) *Poller {
	return &Poller{
		server:   s,
		resolver: resolver,
		logger:   logger,
		dialer:   dialer,
	}
}

// Start reads the identity config and begins polling if poll_interval is set.
func (p *Poller) Start() {
	cfg, err := p.loadConfig()
	if err != nil {
		p.logger.Warn("poller: load config", "error", err)
		return
	}
	d, ok := cfg.PollDuration()
	if !ok {
		return
	}
	p.mu.Lock()
	p.interval = d
	p.raw = cfg.PollInterval
	p.mu.Unlock()
	p.startLoop()
}

// Stop signals the polling goroutine to exit.
func (p *Poller) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopCh != nil {
		close(p.stopCh)
		p.stopCh = nil
	}
}

// SetInterval validates and applies a new polling interval.
// Restarts the goroutine if already running.
func (p *Poller) SetInterval(interval string) error {
	if !ValidPollInterval(interval) {
		return &InvalidIntervalError{Value: interval}
	}

	p.Stop()

	if interval == "" || interval == "n" {
		p.mu.Lock()
		p.interval = 0
		p.raw = interval
		p.mu.Unlock()
		return nil
	}

	d := validPollIntervals[interval]
	p.mu.Lock()
	p.interval = d
	p.raw = interval
	p.mu.Unlock()
	p.startLoop()
	return nil
}

// Status returns the current poller state.
func (p *Poller) Status() PollStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	return PollStatus{
		Interval:  p.raw,
		Active:    p.stopCh != nil,
		LastCheck: p.lastCheck,
		Unseen:    p.lastSeen,
	}
}

// startLoop spawns the background goroutine. Caller must not hold p.mu.
func (p *Poller) startLoop() {
	p.mu.Lock()
	if p.stopCh != nil {
		close(p.stopCh)
	}
	ch := make(chan struct{})
	p.stopCh = ch
	interval := p.interval
	p.mu.Unlock()

	go p.loop(ch, interval)
}

func (p *Poller) loop(stop chan struct{}, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			p.poll()
		}
	}
}

func (p *Poller) poll() {
	cfg, err := p.loadConfig()
	if err != nil {
		p.logger.Warn("poller: load config", "error", err)
		return
	}

	client, err := p.dialer.Dial(cfg, p.logger)
	if err != nil {
		p.logger.Warn("poller: dial", "error", err)
		return
	}
	defer client.Close()

	unseen, err := client.Status("INBOX")
	if err != nil {
		p.logger.Warn("poller: status", "error", err)
		return
	}

	p.mu.Lock()
	prev := p.lastSeen
	p.lastSeen = unseen
	p.lastCheck = time.Now()
	p.mu.Unlock()

	if unseen > prev {
		p.logger.Info("poller: new mail", "unseen", unseen, "previous", prev)
		p.server.SendNotificationToAllClients(mcp.MethodNotificationToolsListChanged, nil)
	}
}

func (p *Poller) loadConfig() (*Config, error) {
	id, err := p.resolver.Resolve()
	if err != nil {
		return nil, err
	}
	idCfgPath, err := paths.IdentityConfigPath(id.Email)
	if err != nil {
		return nil, err
	}
	cfg, err := LoadConfig(idCfgPath)
	if err != nil {
		cfg, err = LoadConfig(DefaultConfigPath())
	}
	return cfg, err
}

// InvalidIntervalError is returned when SetInterval receives a value
// outside the allowed set.
type InvalidIntervalError struct {
	Value string
}

func (e *InvalidIntervalError) Error() string {
	return "invalid poll interval " + e.Value + ": must be 5m, 10m, 15m, 30m, 1h, 2h, or n"
}
