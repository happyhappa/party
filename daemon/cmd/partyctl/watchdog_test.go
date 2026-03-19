package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/norm/relay-daemon/internal/contract"
	"github.com/norm/relay-daemon/internal/recycle"
)

func watchdogTestContract(t *testing.T) (*contract.Contract, string) {
	t.Helper()
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "state")
	os.MkdirAll(stateDir, 0o755)
	os.MkdirAll(filepath.Join(dir, "log"), 0o755)
	os.MkdirAll(filepath.Join(dir, "outbox"), 0o755)

	c := &contract.Contract{
		Version: 1,
		Project: contract.ProjectSpec{Name: "test", RootDir: dir, MainDir: dir},
		Paths:   contract.PathSpec{StateDir: stateDir, ShareDir: dir, LogDir: filepath.Join(dir, "log"), InboxDir: filepath.Join(dir, "outbox")},
		Session: contract.SessionSpec{Name: "test", WindowName: "main"},
		Roles: []contract.RoleSpec{
			{Name: "oc", Tool: "claude_code", WorktreeDir: dir},
			{Name: "cc", Tool: "claude_code", WorktreeDir: dir},
		},
		Layout: contract.LayoutSpec{SchemaVersion: 1},
		Tools: map[string]contract.AgentToolSpec{
			"claude_code": {
				Name: "claude_code",
				Telemetry: contract.TelemetrySpec{
					HasSidecar:  true,
					SidecarPath: filepath.Join(stateDir, "telemetry-${role}.json"),
					ContextKey:  "context_pct",
				},
				Recycle: contract.RecycleSpec{
					ThresholdUsedPct: 65,
					ExitCommand:      "/exit",
				},
			},
		},
	}

	contractPath := filepath.Join(stateDir, "party-contract.json")
	if err := contract.WriteContract(c, contractPath); err != nil {
		t.Fatal(err)
	}
	return c, contractPath
}

func TestWatchdogOnce_BelowThreshold(t *testing.T) {
	c, contractPath := watchdogTestContract(t)
	stateDir := filepath.Dir(contractPath)

	// Write sidecar telemetry below threshold
	writeSidecar(t, stateDir, "oc", 30)
	writeSidecar(t, stateDir, "cc", 40)

	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"watchdog", "--contract-path", contractPath, "--once"})

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	output := out.String()
	// Should have startup, health checks for both roles, and shutdown
	var events []watchdogEvent
	for _, line := range bytes.Split([]byte(output), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var ev watchdogEvent
		if json.Unmarshal(line, &ev) == nil {
			events = append(events, ev)
		}
	}

	if len(events) < 3 {
		t.Fatalf("expected at least 3 events (startup + 2 health + shutdown), got %d: %s", len(events), output)
	}

	// Should not trigger any recycles
	for _, ev := range events {
		if ev.Action == "recycle" && ev.Result == "triggered" {
			t.Errorf("unexpected recycle trigger for %s at below-threshold levels", ev.Role)
		}
	}

	_ = c // used for setup
}

func TestWatchdogOnce_AboveThreshold_DryRun(t *testing.T) {
	_, contractPath := watchdogTestContract(t)
	stateDir := filepath.Dir(contractPath)

	// Write cc at 70% (above 65% threshold)
	writeSidecar(t, stateDir, "oc", 30)
	writeSidecar(t, stateDir, "cc", 70)

	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"watchdog", "--contract-path", contractPath, "--once", "--dry-run"})

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	output := out.String()
	var events []watchdogEvent
	for _, line := range bytes.Split([]byte(output), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var ev watchdogEvent
		if json.Unmarshal(line, &ev) == nil {
			events = append(events, ev)
		}
	}

	foundDryRun := false
	for _, ev := range events {
		if ev.Action == "recycle" && ev.Result == "dry_run" && ev.Role == "cc" {
			foundDryRun = true
		}
		// Should NOT actually trigger
		if ev.Action == "recycle" && ev.Result == "triggered" {
			t.Errorf("dry-run should not trigger actual recycle")
		}
	}
	if !foundDryRun {
		t.Errorf("expected dry_run recycle event for cc, got: %s", output)
	}
}

func TestWatchdogOnce_AboveThreshold_Triggers(t *testing.T) {
	_, contractPath := watchdogTestContract(t)
	stateDir := filepath.Dir(contractPath)

	// Write cc at 70% (above 65% threshold)
	writeSidecar(t, stateDir, "oc", 30)
	writeSidecar(t, stateDir, "cc", 70)

	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"watchdog", "--contract-path", contractPath, "--once"})

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// Verify state file was transitioned to exiting
	state, err := recycle.LoadState(stateDir, "cc")
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if state.State != recycle.StateExiting {
		t.Errorf("cc state = %q, want exiting", state.State)
	}
	if state.RecycleReason == "" {
		t.Error("expected non-empty recycle reason")
	}
}

func TestWatchdogOnce_SkipsMidRecycle(t *testing.T) {
	_, contractPath := watchdogTestContract(t)
	stateDir := filepath.Dir(contractPath)

	writeSidecar(t, stateDir, "oc", 30)
	writeSidecar(t, stateDir, "cc", 70)

	// Pre-set cc to exiting state (mid-recycle)
	state := &recycle.RecycleState{
		State:     recycle.StateExiting,
		EnteredAt: time.Now().UTC(),
	}
	if err := state.Save(stateDir, "cc"); err != nil {
		t.Fatal(err)
	}

	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"watchdog", "--contract-path", contractPath, "--once"})

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	output := out.String()
	var events []watchdogEvent
	for _, line := range bytes.Split([]byte(output), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var ev watchdogEvent
		if json.Unmarshal(line, &ev) == nil {
			events = append(events, ev)
		}
	}

	for _, ev := range events {
		if ev.Role == "cc" && ev.Action == "health" && ev.Result == "skip" {
			return // correct: skipped mid-recycle role
		}
	}
	t.Errorf("expected cc to be skipped (mid-recycle), got: %s", output)
}

func TestWatchdogSingleton(t *testing.T) {
	_, contractPath := watchdogTestContract(t)
	stateDir := filepath.Dir(contractPath)

	// Take the watchdog lock manually to simulate another instance
	lockPath := filepath.Join(stateDir, "watchdog.lock")
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer lockFile.Close()

	// Hold the flock
	acquireTestLock(t, lockFile)

	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"watchdog", "--contract-path", contractPath, "--once"})

	err = root.Execute()
	if err == nil {
		t.Fatal("expected error when another watchdog holds the lock")
	}
	if !bytes.Contains([]byte(err.Error()), []byte("another watchdog")) {
		t.Errorf("expected 'another watchdog' error, got: %v", err)
	}

	// Release
	lockFile.Close()
}

func acquireTestLock(t *testing.T, f *os.File) {
	t.Helper()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("acquire test lock: %v", err)
	}
}

func TestWatchdogMissingSidecar(t *testing.T) {
	_, contractPath := watchdogTestContract(t)
	// Don't write any sidecar files

	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"watchdog", "--contract-path", contractPath, "--once"})

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	output := out.String()
	var events []watchdogEvent
	for _, line := range bytes.Split([]byte(output), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var ev watchdogEvent
		if json.Unmarshal(line, &ev) == nil {
			events = append(events, ev)
		}
	}

	// Should have telemetry errors but not crash
	telemetryErrors := 0
	for _, ev := range events {
		if ev.Action == "health" && ev.Result == "telemetry_error" {
			telemetryErrors++
		}
	}
	if telemetryErrors != 2 {
		t.Errorf("expected 2 telemetry errors (oc + cc), got %d: %s", telemetryErrors, output)
	}
}
