package adminpane

import (
	"context"
	"log"
	"os/exec"
	"sync"
	"time"

	"github.com/norm/relay-daemon/internal/config"
	logpkg "github.com/norm/relay-daemon/internal/log"
	"github.com/norm/relay-daemon/internal/tmux"
	"github.com/norm/relay-daemon/pkg/envelope"
)

// allowedCommands is the set of commands that may be injected into the admin pane.
var allowedCommands = map[string]bool{
	"/health-check":   true,
	"/register-panes": true,
	"/ack":            true,
	"/exit":           true,
}

// AdminTimer manages periodic injection of control-plane commands into the admin pane.
type AdminTimer struct {
	injector *tmux.Injector
	cfg      *config.Config
	logger   *logpkg.EventLog

	mu               sync.Mutex
	lastInjectTime   time.Time
	startTime        time.Time
	lastRecycleTime  time.Time
	paneMapRefreshed bool

	// idle detection
	idleDetector       *IdleDetector
	consecutiveIdleHC  int

	// recycler is set externally after construction
	recycler *Recycler
}

// NewAdminTimer creates a new AdminTimer.
func NewAdminTimer(injector *tmux.Injector, cfg *config.Config, logger *logpkg.EventLog) *AdminTimer {
	now := time.Now()
	return &AdminTimer{
		injector:        injector,
		cfg:             cfg,
		logger:          logger,
		lastInjectTime:     now,
		startTime:       now,
		lastRecycleTime: now,
	}
}

// SetIdleDetector attaches an idle detector for adaptive health-check frequency.
func (t *AdminTimer) SetIdleDetector(d *IdleDetector) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.idleDetector = d
}

// SetRecycler attaches a recycler for triggering admin recycles.
func (t *AdminTimer) SetRecycler(r *Recycler) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.recycler = r
}

// Start launches the health-check ticker goroutine.
// Blocks until ctx is cancelled.
func (t *AdminTimer) Start(ctx context.Context) {
	t.runHealthTicker(ctx)
}

// recordInjectTime updates lastInjectTime under lock after a successful injection.
func (t *AdminTimer) recordInjectTime() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lastInjectTime = time.Now()
}

// StartTime returns the timer's start time.
func (t *AdminTimer) StartTime() time.Time {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.startTime
}

func (t *AdminTimer) runHealthTicker(ctx context.Context) {
	baseInterval := t.cfg.HealthCheckInterval
	idleInterval := 3 * baseInterval // 15min when base is 5min
	ticker := time.NewTicker(baseInterval)
	defer ticker.Stop()
	usingIdle := false

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			t.refreshPaneMapIfStale()
			t.injectCommand("/health-check")
			t.checkDeadman()

			// Adjust health-check frequency based on idle state
			t.mu.Lock()
			idle := t.idleDetector
			t.mu.Unlock()

			if idle != nil && idle.AllAgentsIdle() {
				t.mu.Lock()
				t.consecutiveIdleHC++
				count := t.consecutiveIdleHC
				t.mu.Unlock()
				if count >= 3 && !usingIdle {
					ticker.Reset(idleInterval)
					usingIdle = true
					log.Printf("admin timer: switching to idle health-check interval (%s)", idleInterval)
					t.logEvent("health_interval_idle", idleInterval.String())
				}
			} else {
				t.mu.Lock()
				t.consecutiveIdleHC = 0
				t.mu.Unlock()
				if usingIdle {
					ticker.Reset(baseInterval)
					usingIdle = false
					log.Printf("admin timer: switching to active health-check interval (%s)", baseInterval)
					t.logEvent("health_interval_active", baseInterval.String())
				}
			}
		}
	}
}

// refreshPaneMapIfStale checks if the pane map is stale (registered_at before
// last recycle) and injects /register-panes to admin if needed. Only triggers
// once per staleness detection — guarded by paneMapRefreshed flag.
func (t *AdminTimer) refreshPaneMapIfStale() {
	t.mu.Lock()
	if t.paneMapRefreshed {
		t.mu.Unlock()
		return
	}
	lastRecycle := t.lastRecycleTime
	t.mu.Unlock()

	if !t.cfg.IsPaneMapStale(lastRecycle) {
		return
	}

	log.Printf("admin timer: pane map is stale, injecting /register-panes")
	t.logEvent("admin_pane_map_stale", "")
	t.injectCommand("/register-panes")

	// Brief wait for admin to process /register-panes and write new panes.json
	time.Sleep(3 * time.Second)

	// Reload pane map from disk
	if err := t.cfg.LoadPaneMap(); err != nil {
		log.Printf("admin timer: failed to reload pane map after /register-panes: %v", err)
		t.logEvent("admin_pane_map_reload_error", err.Error())
		return
	}

	// Propagate updated targets to the injector so queues use new pane IDs
	t.injector.UpdateTargets(t.cfg.PaneTargets)

	t.mu.Lock()
	t.paneMapRefreshed = true
	t.mu.Unlock()

	log.Printf("admin timer: pane map reloaded (version=%d, registered_at=%s)",
		t.cfg.PaneMapVersion, t.cfg.PaneMapRegisteredAt)
	t.logEvent("admin_pane_map_refreshed", "")
}

func (t *AdminTimer) injectCommand(cmd string) bool {
	if !allowedCommands[cmd] {
		log.Printf("admin timer: rejected non-allowlisted command %q", cmd)
		t.logEvent("admin_command_rejected", cmd)
		return false
	}

	env := envelope.NewEnvelope("relay", "admin", "command", cmd)
	if err := t.injector.Inject(env); err != nil {
		log.Printf("admin timer: inject %s failed: %v", cmd, err)
		t.logEvent("admin_inject_error", cmd+": "+err.Error())
		return false
	}
	t.recordInjectTime()
	t.logEvent("admin_inject", cmd)
	return true
}

func (t *AdminTimer) checkDeadman() {
	t.mu.Lock()
	elapsed := time.Since(t.lastInjectTime)
	threshold := t.cfg.DeadmanThreshold
	alertHook := t.cfg.AdminAlertHook
	t.mu.Unlock()

	if elapsed <= threshold {
		return
	}

	msg := "admin pane unresponsive: no activity since last inject " + elapsed.Truncate(time.Second).String() + " ago"
	log.Printf("CRITICAL: %s", msg)
	t.logEvent("admin_deadman", msg)

	if alertHook != "" {
		go func() {
			cmd := exec.Command(alertHook, msg)
			if out, err := cmd.CombinedOutput(); err != nil {
				log.Printf("admin alert hook failed: %v (output: %s)", err, string(out))
			}
		}()
	}
}

func (t *AdminTimer) logEvent(eventType, detail string) {
	if t.logger == nil {
		return
	}
	evt := logpkg.NewEvent(eventType, "relay", "admin")
	if detail != "" {
		evt = evt.WithError(detail)
	}
	_ = t.logger.Log(evt)
}
