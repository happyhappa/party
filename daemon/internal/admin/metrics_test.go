package admin

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMetricsRecording(t *testing.T) {
	m := NewMetrics("")

	// Record some metrics
	m.RecordCheckpointRequest()
	m.RecordCheckpointRequest()
	m.RecordCheckpointACK(time.Now().Add(-100 * time.Millisecond))
	m.RecordCheckpointTimeout()
	m.RecordAutogenCheckpoint()
	m.RecordSessionDiscovery()
	m.RecordSessionWatcherError()
	m.RecordRelayActivity()

	snap := m.Snapshot()

	if snap.CheckpointRequestsSent != 2 {
		t.Errorf("expected 2 requests, got %d", snap.CheckpointRequestsSent)
	}
	if snap.CheckpointACKsReceived != 1 {
		t.Errorf("expected 1 ACK, got %d", snap.CheckpointACKsReceived)
	}
	if snap.CheckpointTimeouts != 1 {
		t.Errorf("expected 1 timeout, got %d", snap.CheckpointTimeouts)
	}
	if snap.AutogenCheckpointsCreated != 1 {
		t.Errorf("expected 1 autogen, got %d", snap.AutogenCheckpointsCreated)
	}
	if snap.SessionLogsDiscovered != 1 {
		t.Errorf("expected 1 discovery, got %d", snap.SessionLogsDiscovered)
	}
	if snap.SessionLogWatcherErrors != 1 {
		t.Errorf("expected 1 watcher error, got %d", snap.SessionLogWatcherErrors)
	}
	if snap.RelayActivityEvents != 1 {
		t.Errorf("expected 1 relay activity, got %d", snap.RelayActivityEvents)
	}
}

func TestMetricsLatency(t *testing.T) {
	m := NewMetrics("")

	// Record ACK with known latency
	requestedAt := time.Now().Add(-50 * time.Millisecond)
	m.RecordCheckpointACK(requestedAt)

	snap := m.Snapshot()

	// Latency should be approximately 50ms (allow some slack)
	if snap.LastACKLatencyMs < 40 || snap.LastACKLatencyMs > 100 {
		t.Errorf("expected latency ~50ms, got %f", snap.LastACKLatencyMs)
	}
	if snap.AvgACKLatencyMs < 40 || snap.AvgACKLatencyMs > 100 {
		t.Errorf("expected avg latency ~50ms, got %f", snap.AvgACKLatencyMs)
	}
}

func TestMetricsPersistence(t *testing.T) {
	tmpDir := t.TempDir()
	m := NewMetrics(tmpDir)

	m.RecordCheckpointRequest()
	m.RecordCheckpointACK(time.Now().Add(-100 * time.Millisecond))

	if err := m.Save(); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Check file exists
	path := filepath.Join(tmpDir, "admin-metrics.json")
	if _, err := os.Stat(path); err != nil {
		t.Errorf("metrics file not created: %v", err)
	}
}

func TestMetricsUptime(t *testing.T) {
	m := NewMetrics("")

	// Wait a bit
	time.Sleep(10 * time.Millisecond)

	snap := m.Snapshot()

	if snap.UptimeSeconds < 0 {
		t.Errorf("uptime should be non-negative, got %d", snap.UptimeSeconds)
	}
	if snap.SnapshotTimeMs <= 0 {
		t.Errorf("snapshot time should be positive, got %d", snap.SnapshotTimeMs)
	}
}
