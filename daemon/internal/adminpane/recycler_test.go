package adminpane

import (
	"testing"
	"time"

	"github.com/norm/relay-daemon/internal/config"
)

func TestNeedsRecycle_CycleThreshold(t *testing.T) {
	cfg := config.Default()
	cfg.AdminRecycleCycles = 6
	cfg.AdminMaxUptime = 24 * time.Hour // won't trigger

	r := NewRecycler(nil, cfg, nil, "%2", "/tmp/admin")

	if r.NeedsRecycle(5, time.Now()) {
		t.Error("5 cycles should not trigger recycle (threshold=6)")
	}
	if !r.NeedsRecycle(6, time.Now()) {
		t.Error("6 cycles should trigger recycle (threshold=6)")
	}
	if !r.NeedsRecycle(10, time.Now()) {
		t.Error("10 cycles should trigger recycle (threshold=6)")
	}
}

func TestNeedsRecycle_UptimeThreshold(t *testing.T) {
	cfg := config.Default()
	cfg.AdminRecycleCycles = 100 // won't trigger
	cfg.AdminMaxUptime = 1 * time.Hour

	r := NewRecycler(nil, cfg, nil, "%2", "/tmp/admin")

	if r.NeedsRecycle(0, time.Now()) {
		t.Error("fresh start should not trigger recycle")
	}
	if !r.NeedsRecycle(0, time.Now().Add(-2*time.Hour)) {
		t.Error("2h old start should trigger recycle (threshold=1h)")
	}
}

func TestNeedsRecycle_EitherTriggers(t *testing.T) {
	cfg := config.Default()
	cfg.AdminRecycleCycles = 3
	cfg.AdminMaxUptime = 30 * time.Minute

	r := NewRecycler(nil, cfg, nil, "%2", "/tmp/admin")

	// Cycles hit, uptime doesn't
	if !r.NeedsRecycle(3, time.Now()) {
		t.Error("cycle threshold should trigger even with fresh uptime")
	}

	// Uptime hits, cycles don't
	if !r.NeedsRecycle(0, time.Now().Add(-1*time.Hour)) {
		t.Error("uptime threshold should trigger even with zero cycles")
	}
}

func TestPromptPrefixes(t *testing.T) {
	// Verify the prompt prefixes list is reasonable
	if len(promptPrefixes) == 0 {
		t.Fatal("promptPrefixes should not be empty")
	}
	found := false
	for _, p := range promptPrefixes {
		if p == "$" {
			found = true
			break
		}
	}
	if !found {
		t.Error("promptPrefixes should include $")
	}
}

func TestLastNonEmptyLine(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", ""},
		{"hello", "hello"},
		{"hello\n\n\n", "hello"},
		{"line1\nline2\n", "line2"},
		{"\n\n\n", ""},
		{"$ ", "$"},
	}

	for _, tt := range tests {
		got := lastNonEmptyLine(tt.input)
		if got != tt.want {
			t.Errorf("lastNonEmptyLine(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
