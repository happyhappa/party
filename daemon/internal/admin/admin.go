// Package admin implements the RFC-002 admin daemon for checkpoint coordination.
// Admin monitors relay activity and session logs to trigger automatic checkpoints.
package admin

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"

	"github.com/norm/relay-daemon/internal/autogen"
	logpkg "github.com/norm/relay-daemon/internal/log"
	"github.com/norm/relay-daemon/internal/sessionmap"
	tmuxpkg "github.com/norm/relay-daemon/internal/tmux"
	"github.com/norm/relay-daemon/pkg/envelope"
)

// PodConfig holds pod-specific configuration for session discovery.
type PodConfig struct {
	PodName   string            // Name of the pod
	Worktrees map[string]string // role -> worktree path
	Panes     map[string]string // role -> tmux pane ID
}

// Config holds admin daemon configuration.
type Config struct {
	// Trigger thresholds
	RelayIdleThreshold    time.Duration // Trigger after relay idle this long (default: 120s)
	SessionLogStableThreshold time.Duration // AND session log stable this long (default: 60s)

	// Checkpoint settings
	ACKTimeout           time.Duration // Wait for ACK before fallback (default: 60s)
	MinCheckpointInterval time.Duration // Minimum between checkpoints (default: 5min)
	CooldownAfterCheckpoint time.Duration // Ignore activity after checkpoint (default: 2min)

	// Session log paths (per role)
	SessionLogPaths map[string]string // role -> session log path

	// State persistence
	StateDir string
}

// DefaultConfig returns sensible defaults per RFC-002.
func DefaultConfig() *Config {
	return &Config{
		RelayIdleThreshold:       120 * time.Second,
		SessionLogStableThreshold: 60 * time.Second,
		ACKTimeout:               60 * time.Second,
		MinCheckpointInterval:    5 * time.Minute,
		CooldownAfterCheckpoint:  2 * time.Minute,
		SessionLogPaths:          make(map[string]string),
	}
}

// PendingCheckpoint tracks an inflight checkpoint request.
type PendingCheckpoint struct {
	ChkID     string
	Role      string
	RequestedAt time.Time
}

// Admin coordinates checkpoint requests across agents.
type Admin struct {
	cfg      *Config
	podCfg   *PodConfig
	logger   *logpkg.EventLog
	injector *tmuxpkg.Injector
	autogen  *autogen.Generator
	metrics  *Metrics

	mu sync.Mutex

	// Activity tracking
	lastRelayActivity time.Time
	lastLogGrowth     map[string]time.Time // per-role

	// Checkpoint state
	lastCheckpointTime map[string]time.Time         // per-role
	pendingRequests    map[string]*PendingCheckpoint // role -> pending request
	cooldownUntil      map[string]time.Time         // per-role cooldown

	// Session log watchers
	logWatchers map[string]*SessionLogWatcher

	// Session mapping (discovered per-worktree)
	sessionMap *sessionmap.SessionMap
}

// New creates a new Admin instance.
func New(cfg *Config, podCfg *PodConfig, logger *logpkg.EventLog, injector *tmuxpkg.Injector, gen *autogen.Generator) *Admin {
	if cfg == nil {
		cfg = DefaultConfig()
	}
	podName := ""
	if podCfg != nil {
		podName = podCfg.PodName
	}
	return &Admin{
		cfg:                cfg,
		podCfg:             podCfg,
		logger:             logger,
		injector:           injector,
		autogen:            gen,
		metrics:            NewMetrics(cfg.StateDir),
		lastLogGrowth:      make(map[string]time.Time),
		lastCheckpointTime: make(map[string]time.Time),
		pendingRequests:    make(map[string]*PendingCheckpoint),
		cooldownUntil:      make(map[string]time.Time),
		logWatchers:        make(map[string]*SessionLogWatcher),
		sessionMap:         sessionmap.NewSessionMap(podName, cfg.StateDir),
	}
}

// SetPodConfig updates the pod configuration and triggers session rediscovery.
func (a *Admin) SetPodConfig(podCfg *PodConfig) {
	a.mu.Lock()
	a.podCfg = podCfg
	a.mu.Unlock()

	// Trigger rediscovery
	a.discoverSessionLogs()
}

// Start begins the admin daemon loop.
func (a *Admin) Start(ctx context.Context) error {
	// Discover session logs from pod configuration
	a.discoverSessionLogs()

	// Initialize session log watchers for discovered paths
	a.initSessionLogWatchers(ctx)

	// Main admin loop
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	// Periodic rediscovery ticker (every 5 minutes)
	rediscoverTicker := time.NewTicker(5 * time.Minute)
	defer rediscoverTicker.Stop()

	// Metrics save ticker (every minute)
	metricsTicker := time.NewTicker(1 * time.Minute)
	defer metricsTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Save final metrics on shutdown
			_ = a.SaveMetrics()
			return ctx.Err()
		case <-ticker.C:
			a.checkTriggers()
			a.checkTimeouts()
		case <-rediscoverTicker.C:
			// Periodically rediscover session logs in case they changed
			a.discoverSessionLogs()
			a.initSessionLogWatchers(ctx)
		case <-metricsTicker.C:
			// Periodically save metrics
			_ = a.SaveMetrics()
		}
	}
}

// discoverSessionLogs discovers session logs based on pod configuration.
func (a *Admin) discoverSessionLogs() {
	a.mu.Lock()
	podCfg := a.podCfg
	a.mu.Unlock()

	if podCfg == nil || len(podCfg.Worktrees) == 0 {
		// No pod config - fall back to static SessionLogPaths if configured
		if len(a.cfg.SessionLogPaths) > 0 {
			a.logEvent("session_discovery_fallback", "admin", "", "", "using static SessionLogPaths")
		}
		return
	}

	// Use sessionmap to discover logs per worktree
	if err := a.sessionMap.DiscoverAndUpdate(podCfg.Worktrees, podCfg.Panes); err != nil {
		a.logEvent("session_discovery_error", "admin", "", "", err.Error())
		return
	}

	// Save discovered mappings
	if err := a.sessionMap.Save(); err != nil {
		a.logEvent("session_map_save_error", "admin", "", "", err.Error())
	}

	// Log discovered sessions
	for role, path := range a.sessionMap.GetAllSessionLogPaths() {
		a.logEvent("session_discovered", "admin", role, "", path)
		if a.metrics != nil {
			a.metrics.RecordSessionDiscovery()
		}
	}
}

// initSessionLogWatchers initializes watchers for all discovered session logs.
func (a *Admin) initSessionLogWatchers(ctx context.Context) {
	// Get session log paths - prefer discovered, fall back to static
	paths := a.getSessionLogPaths()

	for role, path := range paths {
		// Skip if already watching this path
		if existing, ok := a.logWatchers[role]; ok && existing != nil {
			continue
		}

		watcher, err := NewSessionLogWatcher(path)
		if err != nil {
			a.logEvent("session_log_watcher_error", "admin", role, "", err.Error())
			if a.metrics != nil {
				a.metrics.RecordSessionWatcherError()
			}
			continue
		}
		a.logWatchers[role] = watcher
		go watcher.Start(ctx, func(growth bool) {
			a.onSessionLogChange(role, growth)
		})
	}
}

// getSessionLogPaths returns session log paths, preferring discovered over static.
func (a *Admin) getSessionLogPaths() map[string]string {
	// Try discovered paths first
	if a.sessionMap != nil {
		paths := a.sessionMap.GetAllSessionLogPaths()
		if len(paths) > 0 {
			return paths
		}
	}
	// Fall back to static configuration
	return a.cfg.SessionLogPaths
}

// RecordRelayActivity updates the last relay activity timestamp.
// Called by the relay daemon when a message is routed.
func (a *Admin) RecordRelayActivity() {
	a.mu.Lock()
	a.lastRelayActivity = time.Now()
	a.mu.Unlock()

	if a.metrics != nil {
		a.metrics.RecordRelayActivity()
	}
}

// HandleCheckpointACK processes an ACK from an agent.
func (a *Admin) HandleCheckpointACK(role, chkID, status string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	pending, ok := a.pendingRequests[role]
	if !ok {
		// No pending request - ignore stale ACK
		a.logEvent("checkpoint_ack_ignored", role, "admin", chkID, "no pending request")
		return
	}

	if pending.ChkID != chkID {
		// Wrong chk_id - ignore stale ACK
		a.logEvent("checkpoint_ack_ignored", role, "admin", chkID, "chk_id mismatch")
		return
	}

	// Valid ACK - record latency metric
	requestedAt := pending.RequestedAt
	delete(a.pendingRequests, role)
	a.lastCheckpointTime[role] = time.Now()
	a.cooldownUntil[role] = time.Now().Add(a.cfg.CooldownAfterCheckpoint)

	if a.metrics != nil {
		a.metrics.RecordCheckpointACK(requestedAt)
	}

	a.logEventWithChkID(logpkg.EventTypeCheckpointAck, role, "admin", chkID, status, "")
}

// checkTriggers evaluates whether to send checkpoint requests.
func (a *Admin) checkTriggers() {
	a.mu.Lock()
	defer a.mu.Unlock()

	now := time.Now()

	// Get roles from session log paths (discovered or static)
	paths := a.getSessionLogPathsLocked()

	for role := range paths {
		// Skip if already has pending request
		if _, pending := a.pendingRequests[role]; pending {
			continue
		}

		// Skip if in cooldown
		if cooldown, ok := a.cooldownUntil[role]; ok && now.Before(cooldown) {
			continue
		}

		// Skip if too soon since last checkpoint
		if lastChk, ok := a.lastCheckpointTime[role]; ok {
			if now.Sub(lastChk) < a.cfg.MinCheckpointInterval {
				continue
			}
		}

		// Check dual-signal trigger
		relayIdle := a.lastRelayActivity.IsZero() || now.Sub(a.lastRelayActivity) >= a.cfg.RelayIdleThreshold

		logStable := false
		if lastGrowth, ok := a.lastLogGrowth[role]; ok {
			logStable = now.Sub(lastGrowth) >= a.cfg.SessionLogStableThreshold
		} else {
			// No growth recorded yet - consider stable
			logStable = true
		}

		if relayIdle && logStable {
			a.sendCheckpointRequest(role)
		}
	}
}

// getSessionLogPathsLocked returns session log paths (must hold mu lock).
func (a *Admin) getSessionLogPathsLocked() map[string]string {
	if a.sessionMap != nil {
		paths := a.sessionMap.GetAllSessionLogPaths()
		if len(paths) > 0 {
			return paths
		}
	}
	return a.cfg.SessionLogPaths
}

// checkTimeouts handles checkpoint request timeouts.
func (a *Admin) checkTimeouts() {
	a.mu.Lock()
	defer a.mu.Unlock()

	now := time.Now()
	paths := a.getSessionLogPathsLocked()

	for role, pending := range a.pendingRequests {
		if now.Sub(pending.RequestedAt) >= a.cfg.ACKTimeout {
			// Timeout - trigger autogen fallback
			a.logEventWithChkID(logpkg.EventTypeTimeout, "admin", role, pending.ChkID, "timeout", "")
			delete(a.pendingRequests, role)

			if a.metrics != nil {
				a.metrics.RecordCheckpointTimeout()
			}

			// Get session log path for this role
			sessionLogPath := paths[role]

			// Trigger autogen checkpoint in background (don't hold lock)
			go a.triggerAutogen(role, pending.ChkID, sessionLogPath)
		}
	}
}

// triggerAutogen generates an autogen checkpoint when ACK times out or log is inaccessible.
func (a *Admin) triggerAutogen(role, chkID, sessionLogPath string) {
	if a.autogen == nil {
		a.logEvent("autogen_skipped", "admin", role, chkID, "autogen generator not configured")
		return
	}

	if sessionLogPath == "" {
		a.logEvent("autogen_skipped", "admin", role, chkID, "no session log path configured")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result, err := a.autogen.Generate(ctx, role, chkID, sessionLogPath)
	if err != nil {
		a.logEvent("autogen_error", "admin", role, chkID, err.Error())
		return
	}

	// Write the autogen bead
	beadID, err := result.WriteBead()
	if err != nil {
		a.logEvent("autogen_bead_error", "admin", role, chkID, err.Error())
		return
	}

	// Log success with source and confidence info
	a.logEventWithChkID("autogen_complete", "admin", role, chkID, result.Source+":"+result.Confidence, "")

	if a.metrics != nil {
		a.metrics.RecordAutogenCheckpoint()
	}

	// Update checkpoint time (autogen counts as a checkpoint)
	a.mu.Lock()
	a.lastCheckpointTime[role] = time.Now()
	a.cooldownUntil[role] = time.Now().Add(a.cfg.CooldownAfterCheckpoint)
	a.mu.Unlock()

	_ = beadID // Used for logging in future if needed
}

// TriggerAutogenForInaccessibleLog triggers autogen when session log is inaccessible.
// This can be called externally when log access fails.
func (a *Admin) TriggerAutogenForInaccessibleLog(role string) {
	a.mu.Lock()
	// Check cooldown
	if cooldown, ok := a.cooldownUntil[role]; ok && time.Now().Before(cooldown) {
		a.mu.Unlock()
		return
	}
	paths := a.getSessionLogPathsLocked()
	sessionLogPath := paths[role]
	a.mu.Unlock()

	chkID := generateChkID()
	a.logEvent("autogen_log_inaccessible", "admin", role, chkID, "")
	a.triggerAutogen(role, chkID, sessionLogPath)
}

// GetSessionMap returns the current session map (for external access).
func (a *Admin) GetSessionMap() *sessionmap.SessionMap {
	return a.sessionMap
}

// RefreshSessionLogs triggers immediate rediscovery of session logs.
func (a *Admin) RefreshSessionLogs() {
	a.discoverSessionLogs()
}

// sendCheckpointRequest sends a checkpoint request to a role.
func (a *Admin) sendCheckpointRequest(role string) {
	chkID := generateChkID()

	// Create checkpoint request message
	message := "[CHECKPOINT_REQUEST] chk_id=" + chkID
	env := envelope.NewEnvelope("admin", role, "checkpoint_request", message)
	env.Priority = 0 // Urgent
	env.Ephemeral = true

	if err := a.injector.Inject(env); err != nil {
		a.logEvent("checkpoint_request_error", "admin", role, chkID, err.Error())
		return
	}

	// Track pending request
	a.pendingRequests[role] = &PendingCheckpoint{
		ChkID:       chkID,
		Role:        role,
		RequestedAt: time.Now(),
	}

	if a.metrics != nil {
		a.metrics.RecordCheckpointRequest()
	}

	a.logEventWithChkID(logpkg.EventTypeCheckpointRequest, "admin", role, chkID, "", "")
}

// onSessionLogChange is called when a session log changes.
func (a *Admin) onSessionLogChange(role string, growth bool) {
	if growth {
		a.mu.Lock()
		a.lastLogGrowth[role] = time.Now()
		a.mu.Unlock()
	}
}

// logEvent logs an admin event.
func (a *Admin) logEvent(eventType, from, to, chkID, errText string) {
	if a.logger == nil {
		return
	}
	evt := logpkg.NewEvent(eventType, from, to)
	if chkID != "" {
		evt = evt.WithChkID(chkID)
	}
	if errText != "" {
		evt = evt.WithError(errText)
	}
	_ = a.logger.Log(evt)
}

// logEventWithChkID logs an event with checkpoint correlation.
func (a *Admin) logEventWithChkID(eventType, from, to, chkID, status, errText string) {
	if a.logger == nil {
		return
	}
	evt := logpkg.NewEvent(eventType, from, to).WithChkID(chkID)
	if status != "" {
		evt = evt.WithStatus(status)
	}
	if errText != "" {
		evt = evt.WithError(errText)
	}
	_ = a.logger.Log(evt)
}

// GetMetrics returns current metrics snapshot.
func (a *Admin) GetMetrics() *MetricsSnapshot {
	if a.metrics == nil {
		return nil
	}
	snap := a.metrics.Snapshot()
	return &snap
}

// SaveMetrics persists current metrics to disk.
func (a *Admin) SaveMetrics() error {
	if a.metrics == nil {
		return nil
	}
	return a.metrics.Save()
}

// generateChkID creates a checkpoint correlation ID.
func generateChkID() string {
	buf := make([]byte, 4)
	if _, err := rand.Read(buf); err != nil {
		n := time.Now().UnixNano()
		buf[0] = byte(n)
		buf[1] = byte(n >> 8)
		buf[2] = byte(n >> 16)
		buf[3] = byte(n >> 24)
	}
	return "chk-" + hex.EncodeToString(buf)
}
