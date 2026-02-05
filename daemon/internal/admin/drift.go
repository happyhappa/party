// Package admin provides drift detection for context capture health.
package admin

import (
	"fmt"
	"os"
	"time"
)

// DriftConfig holds drift detection thresholds.
type DriftConfig struct {
	// Staleness thresholds
	CheckpointStaleThreshold time.Duration // Checkpoint considered stale after this (default: 60min)
	CheckpointWarnThreshold  time.Duration // Warn if checkpoint older than this (default: 30min)
	SummaryStaleThreshold    time.Duration // Summary considered stale after this (default: 120min)

	// Gap detection
	MaxChunkGap int // Alert if chunk indices have gaps larger than this (default: 1)
}

// DefaultDriftConfig returns sensible defaults.
func DefaultDriftConfig() *DriftConfig {
	return &DriftConfig{
		CheckpointStaleThreshold: 60 * time.Minute,
		CheckpointWarnThreshold:  30 * time.Minute,
		SummaryStaleThreshold:    120 * time.Minute,
		MaxChunkGap:              1,
	}
}

// DriftDetector checks for various drift conditions.
type DriftDetector struct {
	cfg *DriftConfig
}

// NewDriftDetector creates a new drift detector.
func NewDriftDetector(cfg *DriftConfig) *DriftDetector {
	if cfg == nil {
		cfg = DefaultDriftConfig()
	}
	return &DriftDetector{cfg: cfg}
}

// DriftReport contains all drift detection results.
type DriftReport struct {
	Timestamp time.Time `json:"timestamp"`

	// Staleness
	CheckpointStaleness []StalenessResult `json:"checkpoint_staleness,omitempty"`
	SummaryStaleness    []StalenessResult `json:"summary_staleness,omitempty"`

	// Missing summaries
	MissingSummaries []MissingSummaryResult `json:"missing_summaries,omitempty"`

	// Dangling wisps
	DanglingWisps []DanglingWispResult `json:"dangling_wisps,omitempty"`

	// Overall status
	HasIssues bool   `json:"has_issues"`
	Summary   string `json:"summary"`
}

// StalenessResult reports staleness for a single item.
type StalenessResult struct {
	Role      string        `json:"role"`
	ItemType  string        `json:"item_type"` // "checkpoint" or "summary"
	Age       time.Duration `json:"age"`
	AgeStr    string        `json:"age_str"`
	Threshold time.Duration `json:"threshold"`
	Status    string        `json:"status"` // "ok", "warn", "stale"
}

// MissingSummaryResult reports a gap in chunk summaries.
type MissingSummaryResult struct {
	Role           string `json:"role"`
	SessionLogPath string `json:"session_log_path"`
	ExpectedChunk  int    `json:"expected_chunk"`
	FoundChunk     int    `json:"found_chunk"`
	GapSize        int    `json:"gap_size"`
}

// DanglingWispResult reports a wisp pointer to non-existent file.
type DanglingWispResult struct {
	Role           string `json:"role"`
	ChkID          string `json:"chk_id"`
	SessionLogPath string `json:"session_log_path"`
	RangeStart     int64  `json:"range_start"`
	RangeEnd       int64  `json:"range_end"`
	Error          string `json:"error"`
}

// CheckStaleness checks if a checkpoint is stale.
func (d *DriftDetector) CheckStaleness(role string, itemType string, createdAt time.Time) StalenessResult {
	age := time.Since(createdAt)

	var threshold time.Duration
	var status string

	switch itemType {
	case "checkpoint":
		if age >= d.cfg.CheckpointStaleThreshold {
			status = "stale"
			threshold = d.cfg.CheckpointStaleThreshold
		} else if age >= d.cfg.CheckpointWarnThreshold {
			status = "warn"
			threshold = d.cfg.CheckpointWarnThreshold
		} else {
			status = "ok"
			threshold = d.cfg.CheckpointWarnThreshold
		}
	case "summary":
		if age >= d.cfg.SummaryStaleThreshold {
			status = "stale"
			threshold = d.cfg.SummaryStaleThreshold
		} else {
			status = "ok"
			threshold = d.cfg.SummaryStaleThreshold
		}
	default:
		status = "unknown"
	}

	return StalenessResult{
		Role:      role,
		ItemType:  itemType,
		Age:       age,
		AgeStr:    formatDuration(age),
		Threshold: threshold,
		Status:    status,
	}
}

// CheckChunkGaps checks for gaps in chunk indices.
func (d *DriftDetector) CheckChunkGaps(role string, sessionLogPath string, chunkIndices []int) []MissingSummaryResult {
	var results []MissingSummaryResult

	if len(chunkIndices) < 2 {
		return results
	}

	// Sort indices (assume caller provides sorted)
	for i := 1; i < len(chunkIndices); i++ {
		expected := chunkIndices[i-1] + 1
		found := chunkIndices[i]
		gap := found - expected

		if gap > d.cfg.MaxChunkGap {
			results = append(results, MissingSummaryResult{
				Role:           role,
				SessionLogPath: sessionLogPath,
				ExpectedChunk:  expected,
				FoundChunk:     found,
				GapSize:        gap,
			})
		}
	}

	return results
}

// CheckDanglingWisp checks if a wisp pointer references an existing file.
func (d *DriftDetector) CheckDanglingWisp(role, chkID, sessionLogPath string, rangeStart, rangeEnd int64) *DanglingWispResult {
	info, err := os.Stat(sessionLogPath)
	if err != nil {
		return &DanglingWispResult{
			Role:           role,
			ChkID:          chkID,
			SessionLogPath: sessionLogPath,
			RangeStart:     rangeStart,
			RangeEnd:       rangeEnd,
			Error:          fmt.Sprintf("file not found: %v", err),
		}
	}

	// Check if range is valid
	if rangeEnd > info.Size() {
		return &DanglingWispResult{
			Role:           role,
			ChkID:          chkID,
			SessionLogPath: sessionLogPath,
			RangeStart:     rangeStart,
			RangeEnd:       rangeEnd,
			Error:          fmt.Sprintf("range end %d exceeds file size %d", rangeEnd, info.Size()),
		}
	}

	return nil // No issues
}

// formatDuration formats a duration for human display.
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	hours := int(d.Hours())
	mins := int(d.Minutes()) % 60
	if mins == 0 {
		return fmt.Sprintf("%dh", hours)
	}
	return fmt.Sprintf("%dh%dm", hours, mins)
}

// GenerateReport creates a full drift report.
func (d *DriftDetector) GenerateReport(
	checkpoints map[string]time.Time, // role -> createdAt
	summaries map[string]time.Time, // role -> most recent summary time
	chunkIndices map[string][]int, // role -> sorted chunk indices
	wisps map[string][]WispInfo, // role -> wisp pointers
) *DriftReport {
	report := &DriftReport{
		Timestamp: time.Now(),
	}

	var issues []string

	// Check checkpoint staleness
	for role, createdAt := range checkpoints {
		result := d.CheckStaleness(role, "checkpoint", createdAt)
		if result.Status != "ok" {
			report.CheckpointStaleness = append(report.CheckpointStaleness, result)
			issues = append(issues, fmt.Sprintf("%s checkpoint %s (%s)", role, result.Status, result.AgeStr))
		}
	}

	// Check summary staleness
	for role, createdAt := range summaries {
		result := d.CheckStaleness(role, "summary", createdAt)
		if result.Status != "ok" {
			report.SummaryStaleness = append(report.SummaryStaleness, result)
			issues = append(issues, fmt.Sprintf("%s summary stale (%s)", role, result.AgeStr))
		}
	}

	// Check chunk gaps
	for role, indices := range chunkIndices {
		gaps := d.CheckChunkGaps(role, "", indices)
		if len(gaps) > 0 {
			report.MissingSummaries = append(report.MissingSummaries, gaps...)
			for _, g := range gaps {
				issues = append(issues, fmt.Sprintf("%s missing chunks %d-%d", role, g.ExpectedChunk, g.FoundChunk-1))
			}
		}
	}

	// Check dangling wisps
	for role, wispList := range wisps {
		for _, w := range wispList {
			if result := d.CheckDanglingWisp(role, w.ChkID, w.SessionLogPath, w.RangeStart, w.RangeEnd); result != nil {
				report.DanglingWisps = append(report.DanglingWisps, *result)
				issues = append(issues, fmt.Sprintf("%s dangling wisp: %s", role, result.Error))
			}
		}
	}

	report.HasIssues = len(issues) > 0
	if report.HasIssues {
		report.Summary = fmt.Sprintf("%d issues: %v", len(issues), issues)
	} else {
		report.Summary = "No drift detected"
	}

	return report
}

// WispInfo holds wisp pointer data for drift checking.
type WispInfo struct {
	ChkID          string
	SessionLogPath string
	RangeStart     int64
	RangeEnd       int64
}
