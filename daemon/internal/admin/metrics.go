// Package admin provides metrics tracking for the admin daemon.
package admin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// Metrics tracks admin daemon operational metrics.
type Metrics struct {
	mu sync.RWMutex

	// Checkpoint metrics
	CheckpointRequestsSent   atomic.Int64 `json:"checkpoint_requests_sent"`
	CheckpointACKsReceived   atomic.Int64 `json:"checkpoint_acks_received"`
	CheckpointTimeouts       atomic.Int64 `json:"checkpoint_timeouts"`
	AutogenCheckpointsCreated atomic.Int64 `json:"autogen_checkpoints_created"`

	// Timing metrics (stored as nanoseconds, exported as ms)
	lastACKLatencyNs       atomic.Int64
	totalACKLatencyNs      atomic.Int64
	ackLatencyCount        atomic.Int64

	// Session metrics
	SessionLogsDiscovered  atomic.Int64 `json:"session_logs_discovered"`
	SessionLogWatcherErrors atomic.Int64 `json:"session_log_watcher_errors"`

	// Relay activity metrics
	RelayActivityEvents    atomic.Int64 `json:"relay_activity_events"`
	LastRelayActivityTs    atomic.Int64 `json:"last_relay_activity_ts_ms"`

	// State
	startTime time.Time
	stateDir  string
}

// NewMetrics creates a new metrics tracker.
func NewMetrics(stateDir string) *Metrics {
	return &Metrics{
		startTime: time.Now(),
		stateDir:  stateDir,
	}
}

// RecordCheckpointRequest increments the checkpoint request counter.
func (m *Metrics) RecordCheckpointRequest() {
	m.CheckpointRequestsSent.Add(1)
}

// RecordCheckpointACK records an ACK receipt with latency.
func (m *Metrics) RecordCheckpointACK(requestedAt time.Time) {
	m.CheckpointACKsReceived.Add(1)

	latency := time.Since(requestedAt).Nanoseconds()
	m.lastACKLatencyNs.Store(latency)
	m.totalACKLatencyNs.Add(latency)
	m.ackLatencyCount.Add(1)
}

// RecordCheckpointTimeout increments the timeout counter.
func (m *Metrics) RecordCheckpointTimeout() {
	m.CheckpointTimeouts.Add(1)
}

// RecordAutogenCheckpoint increments the autogen counter.
func (m *Metrics) RecordAutogenCheckpoint() {
	m.AutogenCheckpointsCreated.Add(1)
}

// RecordSessionDiscovery increments the session discovery counter.
func (m *Metrics) RecordSessionDiscovery() {
	m.SessionLogsDiscovered.Add(1)
}

// RecordSessionWatcherError increments the watcher error counter.
func (m *Metrics) RecordSessionWatcherError() {
	m.SessionLogWatcherErrors.Add(1)
}

// RecordRelayActivity records relay activity.
func (m *Metrics) RecordRelayActivity() {
	m.RelayActivityEvents.Add(1)
	m.LastRelayActivityTs.Store(time.Now().UnixMilli())
}

// Snapshot returns current metrics as a JSON-serializable struct.
type MetricsSnapshot struct {
	// Timestamps
	SnapshotTimeMs int64 `json:"snapshot_time_ms"`
	UptimeSeconds  int64 `json:"uptime_seconds"`

	// Checkpoint metrics
	CheckpointRequestsSent    int64 `json:"checkpoint_requests_sent"`
	CheckpointACKsReceived    int64 `json:"checkpoint_acks_received"`
	CheckpointTimeouts        int64 `json:"checkpoint_timeouts"`
	AutogenCheckpointsCreated int64 `json:"autogen_checkpoints_created"`

	// ACK latency (milliseconds)
	LastACKLatencyMs float64 `json:"last_ack_latency_ms"`
	AvgACKLatencyMs  float64 `json:"avg_ack_latency_ms"`

	// Session metrics
	SessionLogsDiscovered   int64 `json:"session_logs_discovered"`
	SessionLogWatcherErrors int64 `json:"session_log_watcher_errors"`

	// Relay activity
	RelayActivityEvents  int64 `json:"relay_activity_events"`
	LastRelayActivityMs  int64 `json:"last_relay_activity_ts_ms"`
}

// Snapshot returns current metrics.
func (m *Metrics) Snapshot() MetricsSnapshot {
	ackCount := m.ackLatencyCount.Load()
	var avgLatency float64
	if ackCount > 0 {
		avgLatency = float64(m.totalACKLatencyNs.Load()) / float64(ackCount) / 1e6
	}

	return MetricsSnapshot{
		SnapshotTimeMs:            time.Now().UnixMilli(),
		UptimeSeconds:             int64(time.Since(m.startTime).Seconds()),
		CheckpointRequestsSent:    m.CheckpointRequestsSent.Load(),
		CheckpointACKsReceived:    m.CheckpointACKsReceived.Load(),
		CheckpointTimeouts:        m.CheckpointTimeouts.Load(),
		AutogenCheckpointsCreated: m.AutogenCheckpointsCreated.Load(),
		LastACKLatencyMs:          float64(m.lastACKLatencyNs.Load()) / 1e6,
		AvgACKLatencyMs:           avgLatency,
		SessionLogsDiscovered:     m.SessionLogsDiscovered.Load(),
		SessionLogWatcherErrors:   m.SessionLogWatcherErrors.Load(),
		RelayActivityEvents:       m.RelayActivityEvents.Load(),
		LastRelayActivityMs:       m.LastRelayActivityTs.Load(),
	}
}

// Save persists metrics to disk.
func (m *Metrics) Save() error {
	if m.stateDir == "" {
		return nil
	}

	path := filepath.Join(m.stateDir, "admin-metrics.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(m.Snapshot(), "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0o644)
}
