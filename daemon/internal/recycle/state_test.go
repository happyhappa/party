package recycle

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadStateDefaultReady(t *testing.T) {
	dir := t.TempDir()
	s, err := LoadState(dir, "cc")
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if s.State != StateReady {
		t.Errorf("state = %q, want %q", s.State, StateReady)
	}
}

func TestSaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	s := &RecycleState{
		State:          StateExiting,
		EnteredAt:      time.Now().UTC(),
		AgentPID:       12345,
		SessionID:      "test-session",
		TranscriptPath: "/tmp/test.jsonl",
		RecycleReason:  "context 70% >= threshold 65%",
	}

	if err := s.Save(dir, "oc"); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := LoadState(dir, "oc")
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if loaded.State != StateExiting {
		t.Errorf("state = %q, want %q", loaded.State, StateExiting)
	}
	if loaded.AgentPID != 12345 {
		t.Errorf("agent_pid = %d, want 12345", loaded.AgentPID)
	}
	if loaded.RecycleReason != "context 70% >= threshold 65%" {
		t.Errorf("recycle_reason = %q", loaded.RecycleReason)
	}
}

func TestTransitionValid(t *testing.T) {
	tests := []struct {
		from State
		to   State
	}{
		{StateReady, StateExiting},
		{StateExiting, StateConfirming},
		{StateConfirming, StateRelaunching},
		{StateRelaunching, StateHydrating},
		{StateHydrating, StateReady},
		{StateDegraded, StateRelaunching},
		{StateFailed, StateRelaunching},
	}

	for _, tt := range tests {
		t.Run(string(tt.from)+"→"+string(tt.to), func(t *testing.T) {
			s := &RecycleState{State: tt.from}
			if err := s.Transition(tt.to); err != nil {
				t.Fatalf("Transition(%s → %s): %v", tt.from, tt.to, err)
			}
			if s.State != tt.to {
				t.Errorf("state = %q, want %q", s.State, tt.to)
			}
		})
	}
}

func TestTransitionInvalid(t *testing.T) {
	tests := []struct {
		from State
		to   State
	}{
		{StateReady, StateReady},
		{StateReady, StateConfirming},
		{StateExiting, StateReady},
		{StateHydrating, StateExiting},
	}

	for _, tt := range tests {
		t.Run(string(tt.from)+"→"+string(tt.to), func(t *testing.T) {
			s := &RecycleState{State: tt.from}
			if err := s.Transition(tt.to); err == nil {
				t.Fatalf("expected error for invalid transition %s → %s", tt.from, tt.to)
			}
		})
	}
}

func TestTransitionReady_ResetsFields(t *testing.T) {
	s := &RecycleState{
		State:         StateHydrating,
		FailureCount:  2,
		RecycleReason: "threshold exceeded",
		Error:         "some error",
	}
	if err := s.Transition(StateReady); err != nil {
		t.Fatalf("Transition: %v", err)
	}
	if s.FailureCount != 0 {
		t.Errorf("failure_count = %d, want 0", s.FailureCount)
	}
	if s.RecycleReason != "" {
		t.Errorf("recycle_reason = %q, want empty", s.RecycleReason)
	}
	if s.Error != "" {
		t.Errorf("error = %q, want empty", s.Error)
	}
}

func TestTransitionDegraded(t *testing.T) {
	s := &RecycleState{State: StateExiting}
	if err := s.TransitionDegraded("timeout"); err != nil {
		t.Fatalf("TransitionDegraded: %v", err)
	}
	if s.State != StateDegraded {
		t.Errorf("state = %q, want %q", s.State, StateDegraded)
	}
	if s.FailureCount != 1 {
		t.Errorf("failure_count = %d, want 1", s.FailureCount)
	}
	if s.Error != "timeout" {
		t.Errorf("error = %q, want %q", s.Error, "timeout")
	}
}

func TestTransitionFailed(t *testing.T) {
	s := &RecycleState{State: StateDegraded}
	if err := s.TransitionFailed("retry budget exhausted"); err != nil {
		t.Fatalf("TransitionFailed: %v", err)
	}
	if s.State != StateFailed {
		t.Errorf("state = %q, want %q", s.State, StateFailed)
	}
}

func TestStatePaths(t *testing.T) {
	dir := "/tmp/state"
	if got := StatePath(dir, "cc"); got != "/tmp/state/recycle-cc.json" {
		t.Errorf("StatePath = %q", got)
	}
	if got := LockPath(dir, "cc"); got != "/tmp/state/recycle-cc.lock" {
		t.Errorf("LockPath = %q", got)
	}
}

func TestAcquireLockAndRelease(t *testing.T) {
	dir := t.TempDir()
	lock, err := AcquireLock(dir, "test")
	if err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}

	// Verify lock file exists
	lockPath := LockPath(dir, "test")
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("lock file not created: %v", err)
	}

	// Second lock should fail (non-blocking)
	_, err = AcquireLock(dir, "test")
	if err == nil {
		t.Fatal("expected error acquiring second lock")
	}

	// Release and re-acquire should succeed
	if err := lock.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	lock2, err := AcquireLock(dir, "test")
	if err != nil {
		t.Fatalf("re-acquire after release: %v", err)
	}
	lock2.Release()
}

func TestSaveAtomicity(t *testing.T) {
	dir := t.TempDir()
	s := &RecycleState{
		State:     StateReady,
		EnteredAt: time.Now().UTC(),
		AgentPID:  1,
	}
	if err := s.Save(dir, "cc"); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Verify no temp files left behind
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.Name() != "recycle-cc.json" {
			t.Errorf("unexpected file: %s", e.Name())
		}
	}

	// Verify file content is valid JSON
	path := filepath.Join(dir, "recycle-cc.json")
	loaded, err := LoadState(dir, "cc")
	if err != nil {
		t.Fatalf("LoadState after save: %v", err)
	}
	_ = path
	if loaded.AgentPID != 1 {
		t.Errorf("agent_pid = %d, want 1", loaded.AgentPID)
	}
}
