package contextcapture

import "time"

// WispPointer references a range in a session log.
type WispPointer struct {
	SessionLogPath string `json:"session_log_path" yaml:"session_log_path"`
	RangeStart     int64  `json:"range_start" yaml:"range_start"`
	RangeEnd       int64  `json:"range_end" yaml:"range_end"`
}

// Checkpoint captures an agent's durable state snapshot.
type Checkpoint struct {
	Goal         string        `json:"goal" yaml:"goal"`
	Decisions    []string      `json:"decisions" yaml:"decisions"`
	Blockers     []string      `json:"blockers" yaml:"blockers"`
	NextSteps    []string      `json:"next_steps" yaml:"next_steps"`
	WispPointers []WispPointer `json:"wisp_pointers" yaml:"wisp_pointers"`
	ChkID        string        `json:"chk_id" yaml:"chk_id"`
	CreatedAt    time.Time     `json:"created_at" yaml:"created_at"`

	// Plan context (Phase 4 - RFC-002 Section 4.4)
	PlanID      string `json:"plan_id,omitempty" yaml:"plan_id,omitempty"`           // Active plan ID
	MilestoneID string `json:"milestone_id,omitempty" yaml:"milestone_id,omitempty"` // Current milestone
	TaskletID   string `json:"tasklet_id,omitempty" yaml:"tasklet_id,omitempty"`     // Current tasklet being worked
}

// ChunkSummary stores a summary for a session log chunk.
type ChunkSummary struct {
	SessionLogPath string `json:"session_log_path" yaml:"session_log_path"`
	RangeStart     int64  `json:"range_start" yaml:"range_start"`
	RangeEnd       int64  `json:"range_end" yaml:"range_end"`
	SummaryText    string `json:"summary_text" yaml:"summary_text"`
	ChunkIndex     int    `json:"chunk_index" yaml:"chunk_index"`
}

// ChunkRange identifies a chunk index range.
type ChunkRange struct {
	Start int `json:"start" yaml:"start"`
	End   int `json:"end" yaml:"end"`
}

// StateRollup stores a rollup summary over multiple chunks.
type StateRollup struct {
	ChunkRange  ChunkRange `json:"chunk_range" yaml:"chunk_range"`
	SummaryText string     `json:"summary_text" yaml:"summary_text"`
	CreatedAt   time.Time  `json:"created_at" yaml:"created_at"`
}
