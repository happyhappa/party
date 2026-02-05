package admin

import (
	"testing"
	"time"

	"github.com/norm/relay-daemon/internal/tmux"
)

func newTestAdmin() *Admin {
	cfg := DefaultConfig()
	cfg.RelayIdleThreshold = 2 * time.Second
	cfg.SessionLogStableThreshold = 2 * time.Second
	cfg.ACKTimeout = 2 * time.Second
	cfg.MinCheckpointInterval = 0
	cfg.CooldownAfterCheckpoint = 1 * time.Second
	cfg.SessionLogPaths = map[string]string{
		"cc": "/tmp/session-cc.jsonl",
	}
	injector := tmux.NewInjector(nil, map[string]string{"cc": "%1"})
	return New(cfg, nil, nil, injector, nil) // nil podCfg, nil logger, nil autogen for tests
}

func TestAdminCheckTriggersCreatesRequest(t *testing.T) {
	admin := newTestAdmin()
	now := time.Now()

	admin.lastRelayActivity = now.Add(-3 * time.Second)
	admin.lastLogGrowth["cc"] = now.Add(-3 * time.Second)

	admin.checkTriggers()

	pending, ok := admin.pendingRequests["cc"]
	if !ok {
		t.Fatalf("expected pending request for cc")
	}
	if pending.ChkID == "" || pending.ChkID[:4] != "chk-" {
		t.Fatalf("expected chk- prefixed chk_id, got %q", pending.ChkID)
	}
}

func TestAdminCheckTriggersNoRelayIdle(t *testing.T) {
	admin := newTestAdmin()
	now := time.Now()

	admin.lastRelayActivity = now
	admin.lastLogGrowth["cc"] = now.Add(-3 * time.Second)

	admin.checkTriggers()

	if _, ok := admin.pendingRequests["cc"]; ok {
		t.Fatalf("expected no pending request when relay not idle")
	}
}

func TestAdminCheckTriggersNoLogStable(t *testing.T) {
	admin := newTestAdmin()
	now := time.Now()

	admin.lastRelayActivity = now.Add(-3 * time.Second)
	admin.lastLogGrowth["cc"] = now

	admin.checkTriggers()

	if _, ok := admin.pendingRequests["cc"]; ok {
		t.Fatalf("expected no pending request when log not stable")
	}
}

func TestAdminCheckTimeoutsClearsPending(t *testing.T) {
	admin := newTestAdmin()
	now := time.Now()

	admin.pendingRequests["cc"] = &PendingCheckpoint{
		ChkID:       "chk-timeout",
		Role:        "cc",
		RequestedAt: now.Add(-3 * time.Second),
	}

	admin.checkTimeouts()

	if _, ok := admin.pendingRequests["cc"]; ok {
		t.Fatalf("expected pending request cleared after timeout")
	}
}

func TestAdminHandleCheckpointACK(t *testing.T) {
	admin := newTestAdmin()
	now := time.Now()

	admin.pendingRequests["cc"] = &PendingCheckpoint{
		ChkID:       "chk-good",
		Role:        "cc",
		RequestedAt: now,
	}

	admin.HandleCheckpointACK("cc", "chk-bad", "success")
	if _, ok := admin.pendingRequests["cc"]; !ok {
		t.Fatalf("expected pending request to remain on chk_id mismatch")
	}

	admin.HandleCheckpointACK("cc", "chk-good", "success")
	if _, ok := admin.pendingRequests["cc"]; ok {
		t.Fatalf("expected pending request cleared on valid ack")
	}
	if admin.cooldownUntil["cc"].IsZero() {
		t.Fatalf("expected cooldown set on valid ack")
	}
	if admin.lastCheckpointTime["cc"].IsZero() {
		t.Fatalf("expected last checkpoint time set on valid ack")
	}
}

func TestAdminCooldownEnforced(t *testing.T) {
	admin := newTestAdmin()
	now := time.Now()

	admin.lastRelayActivity = now.Add(-3 * time.Second)
	admin.lastLogGrowth["cc"] = now.Add(-3 * time.Second)
	admin.cooldownUntil["cc"] = now.Add(10 * time.Second)

	admin.checkTriggers()

	if _, ok := admin.pendingRequests["cc"]; ok {
		t.Fatalf("expected no pending request during cooldown")
	}
}

func TestAdminMinCheckpointIntervalEnforced(t *testing.T) {
	admin := newTestAdmin()
	now := time.Now()

	admin.cfg.MinCheckpointInterval = 10 * time.Second
	admin.lastRelayActivity = now.Add(-3 * time.Second)
	admin.lastLogGrowth["cc"] = now.Add(-3 * time.Second)
	admin.lastCheckpointTime["cc"] = now.Add(-2 * time.Second)

	admin.checkTriggers()

	if _, ok := admin.pendingRequests["cc"]; ok {
		t.Fatalf("expected no pending request within min checkpoint interval")
	}
}

func TestAdminSaveLoadState(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig()
	cfg.StateDir = dir
	cfg.SessionLogPaths = map[string]string{"cc": "/tmp/session-cc.jsonl"}
	admin := New(cfg, nil, nil, tmux.NewInjector(nil, map[string]string{"cc": "%1"}), nil)

	now := time.Now()
	admin.lastRelayActivity = now
	admin.lastLogGrowth["cc"] = now.Add(-1 * time.Minute)
	admin.lastCheckpointTime["cc"] = now.Add(-2 * time.Minute)
	admin.cooldownUntil["cc"] = now.Add(30 * time.Second)

	if err := admin.SaveState(); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	admin2 := New(cfg, nil, nil, tmux.NewInjector(nil, map[string]string{"cc": "%1"}), nil)
	if err := admin2.LoadState(); err != nil {
		t.Fatalf("LoadState: %v", err)
	}

	if admin2.lastRelayActivity.IsZero() {
		t.Fatalf("expected lastRelayActivity restored")
	}
	if admin2.lastLogGrowth["cc"].IsZero() {
		t.Fatalf("expected lastLogGrowth restored")
	}
	if admin2.lastCheckpointTime["cc"].IsZero() {
		t.Fatalf("expected lastCheckpointTime restored")
	}
	if admin2.cooldownUntil["cc"].IsZero() {
		t.Fatalf("expected cooldownUntil restored")
	}
}
