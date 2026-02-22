package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultDurations(t *testing.T) {
	cfg := Default()
	if cfg.StuckThreshold != 5*time.Minute {
		t.Errorf("StuckThreshold = %v, want 5m", cfg.StuckThreshold)
	}
	if cfg.NagInterval != 5*time.Minute {
		t.Errorf("NagInterval = %v, want 5m", cfg.NagInterval)
	}
	if cfg.MaxNagDuration != 30*time.Minute {
		t.Errorf("MaxNagDuration = %v, want 30m", cfg.MaxNagDuration)
	}
}

func TestLoadDurationEnvOverrides(t *testing.T) {
	t.Setenv("RELAY_STUCK_THRESHOLD", "7m")
	t.Setenv("RELAY_NAG_INTERVAL", "2m")
	t.Setenv("RELAY_MAX_NAG_DURATION", "20m")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.StuckThreshold != 7*time.Minute {
		t.Errorf("StuckThreshold = %v, want 7m", cfg.StuckThreshold)
	}
	if cfg.NagInterval != 2*time.Minute {
		t.Errorf("NagInterval = %v, want 2m", cfg.NagInterval)
	}
	if cfg.MaxNagDuration != 20*time.Minute {
		t.Errorf("MaxNagDuration = %v, want 20m", cfg.MaxNagDuration)
	}
}

func TestLoadPaneMapV2Format(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "panes.json")
	data := `{"panes":{"oc":"%0","cc":"%1","admin":"%2","cx":"%3"},"version":3,"registered_at":"2026-02-12T21:10:43Z"}`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := Default()
	cfg.PaneMapPath = path
	if err := cfg.LoadPaneMap(); err != nil {
		t.Fatalf("LoadPaneMap: %v", err)
	}

	if len(cfg.PaneTargets) != 4 {
		t.Fatalf("PaneTargets has %d entries, want 4", len(cfg.PaneTargets))
	}
	if cfg.PaneTargets["oc"] != "%0" {
		t.Errorf("PaneTargets[oc] = %q, want %%0", cfg.PaneTargets["oc"])
	}
	if cfg.PaneTargets["admin"] != "%2" {
		t.Errorf("PaneTargets[admin] = %q, want %%2", cfg.PaneTargets["admin"])
	}
	if cfg.PaneMapVersion != 3 {
		t.Errorf("PaneMapVersion = %d, want 3", cfg.PaneMapVersion)
	}
	if cfg.PaneMapRegisteredAt != "2026-02-12T21:10:43Z" {
		t.Errorf("PaneMapRegisteredAt = %q, want 2026-02-12T21:10:43Z", cfg.PaneMapRegisteredAt)
	}
}

func TestLoadPaneMapFlatFormat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "panes.json")
	data := `{"oc":"%0","cc":"%1","cx":"%2"}`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := Default()
	cfg.PaneMapPath = path
	if err := cfg.LoadPaneMap(); err != nil {
		t.Fatalf("LoadPaneMap: %v", err)
	}

	if len(cfg.PaneTargets) != 3 {
		t.Fatalf("PaneTargets has %d entries, want 3", len(cfg.PaneTargets))
	}
	if cfg.PaneTargets["oc"] != "%0" {
		t.Errorf("PaneTargets[oc] = %q, want %%0", cfg.PaneTargets["oc"])
	}
	if cfg.PaneMapVersion != 0 {
		t.Errorf("PaneMapVersion = %d, want 0 for flat format", cfg.PaneMapVersion)
	}
	if cfg.PaneMapRegisteredAt != "" {
		t.Errorf("PaneMapRegisteredAt = %q, want empty for flat format", cfg.PaneMapRegisteredAt)
	}
}

func TestIsPaneMapStale(t *testing.T) {
	cfg := Default()

	// Empty registered_at → stale
	cfg.PaneMapRegisteredAt = ""
	if !cfg.IsPaneMapStale(time.Now()) {
		t.Error("empty registered_at should be stale")
	}

	// registered_at before lastRecycleTime → stale
	cfg.PaneMapRegisteredAt = "2026-02-12T20:00:00Z"
	recycle, _ := time.Parse(time.RFC3339, "2026-02-12T21:00:00Z")
	if !cfg.IsPaneMapStale(recycle) {
		t.Error("registered_at before lastRecycleTime should be stale")
	}

	// registered_at after lastRecycleTime → not stale
	cfg.PaneMapRegisteredAt = "2026-02-12T22:00:00Z"
	if cfg.IsPaneMapStale(recycle) {
		t.Error("registered_at after lastRecycleTime should not be stale")
	}

	// Invalid registered_at → stale
	cfg.PaneMapRegisteredAt = "not-a-timestamp"
	if !cfg.IsPaneMapStale(recycle) {
		t.Error("invalid registered_at should be stale")
	}
}
