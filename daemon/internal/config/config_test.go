package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultAdminFields(t *testing.T) {
	cfg := Default()
	if cfg.CheckpointInterval != 10*time.Minute {
		t.Errorf("CheckpointInterval = %v, want 10m", cfg.CheckpointInterval)
	}
	if cfg.HealthCheckInterval != 5*time.Minute {
		t.Errorf("HealthCheckInterval = %v, want 5m", cfg.HealthCheckInterval)
	}
	if cfg.AdminRecycleCycles != 6 {
		t.Errorf("AdminRecycleCycles = %d, want 6", cfg.AdminRecycleCycles)
	}
	if cfg.AdminMaxUptime != 2*time.Hour {
		t.Errorf("AdminMaxUptime = %v, want 2h", cfg.AdminMaxUptime)
	}
	if cfg.AdminAlertHook != "" {
		t.Errorf("AdminAlertHook = %q, want empty", cfg.AdminAlertHook)
	}
}

func TestLoadAdminEnvOverrides(t *testing.T) {
	t.Setenv("RELAY_CHECKPOINT_INTERVAL", "15m")
	t.Setenv("RELAY_HEALTH_CHECK_INTERVAL", "3m")
	t.Setenv("RELAY_ADMIN_RECYCLE_AFTER_CYCLES", "10")
	t.Setenv("RELAY_ADMIN_MAX_UPTIME", "1h")
	t.Setenv("RELAY_ADMIN_ALERT_HOOK", "/usr/local/bin/alert.sh")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.CheckpointInterval != 15*time.Minute {
		t.Errorf("CheckpointInterval = %v, want 15m", cfg.CheckpointInterval)
	}
	if cfg.HealthCheckInterval != 3*time.Minute {
		t.Errorf("HealthCheckInterval = %v, want 3m", cfg.HealthCheckInterval)
	}
	if cfg.AdminRecycleCycles != 10 {
		t.Errorf("AdminRecycleCycles = %d, want 10", cfg.AdminRecycleCycles)
	}
	if cfg.AdminMaxUptime != 1*time.Hour {
		t.Errorf("AdminMaxUptime = %v, want 1h", cfg.AdminMaxUptime)
	}
	if cfg.AdminAlertHook != "/usr/local/bin/alert.sh" {
		t.Errorf("AdminAlertHook = %q, want /usr/local/bin/alert.sh", cfg.AdminAlertHook)
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
