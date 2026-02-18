package adminpane

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAllAgentsIdle_NoDirs(t *testing.T) {
	d := NewIdleDetector(map[string]string{}, 2*time.Hour)
	if d.AllAgentsIdle() {
		t.Fatal("expected not idle when no project dirs configured")
	}
}

func TestAllAgentsIdle_AllIdle(t *testing.T) {
	dir := t.TempDir()
	// Create a JSONL file with mtime in the past
	f := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(f, []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-10 * time.Minute)
	os.Chtimes(f, past, past)

	d := NewIdleDetector(map[string]string{"oc": dir}, 2*time.Hour)
	// Record injection after the file mtime
	d.RecordCheckpointInjection()

	if !d.AllAgentsIdle() {
		t.Fatal("expected all agents idle when JSONL mtime is before injection time")
	}
}

func TestAllAgentsIdle_OneActive(t *testing.T) {
	dirIdle := t.TempDir()
	dirActive := t.TempDir()

	// Idle agent: old file
	fIdle := filepath.Join(dirIdle, "session.jsonl")
	os.WriteFile(fIdle, []byte("{}"), 0644)
	past := time.Now().Add(-10 * time.Minute)
	os.Chtimes(fIdle, past, past)

	// Record injection
	d := NewIdleDetector(map[string]string{"oc": dirIdle, "cc": dirActive}, 2*time.Hour)
	d.RecordCheckpointInjection()

	// Active agent: file written after injection
	time.Sleep(10 * time.Millisecond)
	fActive := filepath.Join(dirActive, "session.jsonl")
	os.WriteFile(fActive, []byte("{}"), 0644)

	if d.AllAgentsIdle() {
		t.Fatal("expected not idle when one agent has recent JSONL")
	}
}

func TestAllAgentsIdle_NoJSONLFiles(t *testing.T) {
	dir := t.TempDir()
	d := NewIdleDetector(map[string]string{"oc": dir}, 2*time.Hour)
	if d.AllAgentsIdle() {
		t.Fatal("expected not idle when no JSONL files exist")
	}
}

func TestAllAgentsIdle_MultipleJSONL_PicksLatest(t *testing.T) {
	dir := t.TempDir()

	// Old file
	old := filepath.Join(dir, "old.jsonl")
	os.WriteFile(old, []byte("{}"), 0644)
	past := time.Now().Add(-1 * time.Hour)
	os.Chtimes(old, past, past)

	d := NewIdleDetector(map[string]string{"oc": dir}, 2*time.Hour)
	d.RecordCheckpointInjection()

	// New file written after injection
	time.Sleep(10 * time.Millisecond)
	recent := filepath.Join(dir, "recent.jsonl")
	os.WriteFile(recent, []byte("{}"), 0644)

	if d.AllAgentsIdle() {
		t.Fatal("expected not idle when latest JSONL is newer than injection")
	}
}

func TestShouldBackstop(t *testing.T) {
	d := NewIdleDetector(map[string]string{}, 100*time.Millisecond)
	if d.ShouldBackstop() {
		t.Fatal("should not backstop immediately after creation")
	}

	time.Sleep(150 * time.Millisecond)
	if !d.ShouldBackstop() {
		t.Fatal("should backstop after interval has passed")
	}

	d.RecordCheckpointInjection()
	if d.ShouldBackstop() {
		t.Fatal("should not backstop right after recording injection")
	}
}
