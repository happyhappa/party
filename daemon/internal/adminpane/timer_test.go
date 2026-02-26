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
	allowed := []string{"/health-check", "/ack", "/exit"}
	for _, cmd := range allowed {
		if !allowedCommands[cmd] {
			t.Errorf("%q should be allowed", cmd)
		}
	}

	rejected := []string{"/plan", "/attack", "/checkpoint-cycle", "rm -rf /", "echo hello"}
	for _, cmd := range rejected {
		if allowedCommands[cmd] {
			t.Errorf("%q should NOT be allowed", cmd)
		}
	}
}

func TestRecordInjectTime(t *testing.T) {
	cfg := config.Default()
	timer := NewAdminTimer(noopInjector(), cfg, nil)

	before := timer.lastInjectTime
	time.Sleep(10 * time.Millisecond)
	timer.recordInjectTime()

	timer.mu.Lock()
	after := timer.lastInjectTime
	timer.mu.Unlock()

	if !after.After(before) {
		t.Error("recordInjectTime should update lastInjectTime")
	}
}

func TestInjectCommandReturnsFalseOnFailure(t *testing.T) {
	cfg := config.Default()
	// noopInjector has no "admin" target, so Inject will fail
	timer := NewAdminTimer(noopInjector(), cfg, nil)

	if timer.injectCommand("/health-check") {
		t.Error("injectCommand should return false when inject fails (no admin target)")
	}
}

func TestInjectCommandReturnsFalseOnRejected(t *testing.T) {
	cfg := config.Default()
	timer := NewAdminTimer(noopInjector(), cfg, nil)

	if timer.injectCommand("/not-allowed") {
		t.Error("injectCommand should return false for non-allowlisted command")
	}
}

func TestStartCancellation(t *testing.T) {
	cfg := config.Default()
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
