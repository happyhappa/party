package admin

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDriftDetectorStaleness(t *testing.T) {
	cfg := DefaultDriftConfig()
	cfg.CheckpointWarnThreshold = 30 * time.Minute
	cfg.CheckpointStaleThreshold = 60 * time.Minute
	d := NewDriftDetector(cfg)

	tests := []struct {
		name       string
		age        time.Duration
		wantStatus string
	}{
		{"fresh", 10 * time.Minute, "ok"},
		{"warn", 45 * time.Minute, "warn"},
		{"stale", 90 * time.Minute, "stale"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			createdAt := time.Now().Add(-tt.age)
			result := d.CheckStaleness("cc", "checkpoint", createdAt)
			if result.Status != tt.wantStatus {
				t.Errorf("got status %q, want %q", result.Status, tt.wantStatus)
			}
		})
	}
}

func TestDriftDetectorChunkGaps(t *testing.T) {
	d := NewDriftDetector(nil)

	tests := []struct {
		name     string
		indices  []int
		wantGaps int
	}{
		{"no_gaps", []int{0, 1, 2, 3}, 0},
		{"one_gap", []int{0, 1, 4, 5}, 1},
		{"multiple_gaps", []int{0, 3, 7}, 2},
		{"single", []int{0}, 0},
		{"empty", []int{}, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gaps := d.CheckChunkGaps("cc", "/path/to/log.jsonl", tt.indices)
			if len(gaps) != tt.wantGaps {
				t.Errorf("got %d gaps, want %d", len(gaps), tt.wantGaps)
			}
		})
	}
}

func TestDriftDetectorDanglingWisp(t *testing.T) {
	d := NewDriftDetector(nil)

	// Create temp file
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "session.jsonl")
	if err := os.WriteFile(tmpFile, []byte("test content here"), 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name       string
		path       string
		rangeEnd   int64
		wantDangle bool
	}{
		{"valid", tmpFile, 10, false},
		{"missing_file", "/nonexistent/path.jsonl", 100, true},
		{"range_exceeds", tmpFile, 999999, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := d.CheckDanglingWisp("cc", "chk-123", tt.path, 0, tt.rangeEnd)
			gotDangle := result != nil
			if gotDangle != tt.wantDangle {
				t.Errorf("got dangling=%v, want %v", gotDangle, tt.wantDangle)
			}
		})
	}
}

func TestDriftDetectorGenerateReport(t *testing.T) {
	d := NewDriftDetector(nil)

	// Fresh checkpoint - should be OK
	checkpoints := map[string]time.Time{
		"cc": time.Now().Add(-5 * time.Minute),
	}

	// No other issues
	report := d.GenerateReport(checkpoints, nil, nil, nil)

	if report.HasIssues {
		t.Errorf("expected no issues, got: %s", report.Summary)
	}
}

func TestDriftDetectorGenerateReportWithIssues(t *testing.T) {
	cfg := DefaultDriftConfig()
	cfg.CheckpointStaleThreshold = 30 * time.Minute
	cfg.CheckpointWarnThreshold = 15 * time.Minute
	d := NewDriftDetector(cfg)

	// Stale checkpoint
	checkpoints := map[string]time.Time{
		"cc": time.Now().Add(-60 * time.Minute),
	}

	// Gap in chunks
	chunkIndices := map[string][]int{
		"cc": {0, 1, 5, 6}, // missing 2, 3, 4
	}

	report := d.GenerateReport(checkpoints, nil, chunkIndices, nil)

	if !report.HasIssues {
		t.Error("expected issues to be detected")
	}
	if len(report.CheckpointStaleness) != 1 {
		t.Errorf("expected 1 stale checkpoint, got %d", len(report.CheckpointStaleness))
	}
	if len(report.MissingSummaries) != 1 {
		t.Errorf("expected 1 missing summary gap, got %d", len(report.MissingSummaries))
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "30s"},
		{5 * time.Minute, "5m"},
		{2 * time.Hour, "2h"},
		{90 * time.Minute, "1h30m"},
	}

	for _, tt := range tests {
		got := formatDuration(tt.d)
		if got != tt.want {
			t.Errorf("formatDuration(%v) = %q, want %q", tt.d, got, tt.want)
		}
	}
}
