package adminpane

import (
	"context"
	"testing"
	"time"

	"github.com/norm/relay-daemon/internal/config"
	"github.com/norm/relay-daemon/internal/tmux"
)

// noopInjector returns an Injector with no targets — Inject() returns an error
// for unknown targets but never panics.
func noopInjector() *tmux.Injector {
	return tmux.NewInjector(tmux.New(), map[string]string{})
}

func TestAllowedCommands(t *testing.T) {
	allowed := []string{"/checkpoint-cycle", "/health-check", "/ack", "/exit"}
	for _, cmd := range allowed {
		if !allowedCommands[cmd] {
			t.Errorf("%q should be allowed", cmd)
		}
	}

	rejected := []string{"/plan", "/attack", "rm -rf /", "echo hello"}
	for _, cmd := range rejected {
		if allowedCommands[cmd] {
			t.Errorf("%q should NOT be allowed", cmd)
		}
	}
}

func TestRecordACK(t *testing.T) {
	cfg := config.Default()
	timer := NewAdminTimer(noopInjector(), cfg, nil)

	before := timer.lastACKTime
	time.Sleep(10 * time.Millisecond)
	timer.RecordACK()

	timer.mu.Lock()
	after := timer.lastACKTime
	timer.mu.Unlock()

	if !after.After(before) {
		t.Error("RecordACK should update lastACKTime")
	}
}

func TestCheckpointCycleIncrement(t *testing.T) {
	cfg := config.Default()
	cfg.CheckpointInterval = 50 * time.Millisecond
	cfg.HealthCheckInterval = 10 * time.Second // don't fire during test

	timer := NewAdminTimer(noopInjector(), cfg, nil)

	if timer.CheckpointCycles() != 0 {
		t.Errorf("initial cycles = %d, want 0", timer.CheckpointCycles())
	}
}

func TestStartCancellation(t *testing.T) {
	cfg := config.Default()
	cfg.CheckpointInterval = 100 * time.Millisecond
	cfg.HealthCheckInterval = 100 * time.Millisecond

	timer := NewAdminTimer(noopInjector(), cfg, nil)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		timer.Start(ctx)
		close(done)
	}()

	// Cancel after a short delay
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
		// Success: Start returned after cancel
	case <-time.After(2 * time.Second):
		t.Fatal("Start did not return after context cancellation")
	}
}
