// Package labels provides RFC-002 compliant label key constants and utilities.
package labels

import (
	"fmt"
	"regexp"
	"strings"
)

// Canonical label keys per RFC-002.
const (
	// Identity labels
	KeyRole     = "role"      // Agent role: oc, cc, cx
	KeyRepo     = "repo"      // Repository name
	KeyChkID    = "chk_id"    // Checkpoint correlation ID
	KeyWriter   = "writer"    // Who wrote the bead: agent, admin

	// Source and confidence labels
	KeySource     = "source"     // How created: manual, pre_compact, session_end, autogen, haiku, heuristic
	KeyConfidence = "confidence" // Confidence level: high, medium, low

	// Chunk summary labels
	KeyChunkNum      = "chunk_num"        // 1-based chunk number
	KeyChunkIndex    = "chunk_index"      // 0-based chunk index
	KeyStartOffset   = "start_offset"     // Byte offset start
	KeyEndOffset     = "end_offset"       // Byte offset end
	KeyOverlapStart  = "overlap_start"    // Overlap region start
	KeyChunkRange    = "chunk_range"      // "start-end" byte range
	KeySessionLogPath = "session_log_path" // Path to session log

	// State rollup labels
	KeyRollupNum      = "rollup_num"      // Rollup sequence number
	KeyChunksIncluded = "chunks_included" // Number of chunks in rollup
	KeyTotalChunks    = "total_chunks"    // Total chunks processed

	// Plan/milestone/tasklet labels
	KeyPlanID       = "plan_id"       // Plan identifier
	KeyMilestoneID  = "milestone_id"  // Milestone identifier
	KeyMilestoneNum = "milestone_num" // Milestone sequence number
	KeyTaskletID    = "tasklet_id"    // Tasklet identifier
	KeyStatus       = "status"        // Status: draft, active, completed, abandoned, pending, in_progress, done, blocked
	KeyThread       = "thread"        // Thread grouping
	KeyAssignee     = "assignee"      // Assigned agent role
	KeyTrigger      = "trigger"       // Trigger event: milestone_complete

	// Timestamp labels
	KeyCreatedAt = "created_at" // RFC3339 timestamp
	KeyTimestamp = "timestamp"  // Unix timestamp
	KeyByteOffset = "byte_offset" // Current byte offset
)

// Valid values for specific labels.
var (
	ValidRoles       = []string{"oc", "cc", "cx", "admin"}
	ValidSources     = []string{"manual", "pre_compact", "session_end", "autogen", "haiku", "heuristic", "agent"}
	ValidConfidences = []string{"high", "medium", "low"}
	ValidWriters     = []string{"agent", "admin"}

	// Plan status values
	ValidPlanStatuses      = []string{"draft", "active", "completed", "abandoned"}
	ValidMilestoneStatuses = []string{"pending", "in_progress", "done", "skipped"}
	ValidTaskletStatuses   = []string{"pending", "in_progress", "done", "blocked"}
)

// labelKeyRegex validates label key format: lowercase alphanumeric with underscores.
var labelKeyRegex = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// ValidateKey checks if a label key conforms to RFC-002 format.
func ValidateKey(key string) error {
	if key == "" {
		return fmt.Errorf("label key cannot be empty")
	}
	if !labelKeyRegex.MatchString(key) {
		return fmt.Errorf("label key %q must be lowercase alphanumeric with underscores, starting with a letter", key)
	}
	return nil
}

// ValidateValue checks if a label value is valid for the given key.
func ValidateValue(key, value string) error {
	if value == "" {
		return fmt.Errorf("label value for %q cannot be empty", key)
	}

	// Key-specific validation
	switch key {
	case KeyRole:
		if !contains(ValidRoles, value) {
			return fmt.Errorf("invalid role %q, must be one of: %v", value, ValidRoles)
		}
	case KeySource:
		if !contains(ValidSources, value) {
			return fmt.Errorf("invalid source %q, must be one of: %v", value, ValidSources)
		}
	case KeyConfidence:
		if !contains(ValidConfidences, value) {
			return fmt.Errorf("invalid confidence %q, must be one of: %v", value, ValidConfidences)
		}
	case KeyWriter:
		if !contains(ValidWriters, value) {
			return fmt.Errorf("invalid writer %q, must be one of: %v", value, ValidWriters)
		}
	case KeyStatus:
		// Status can be from any of the status lists
		allStatuses := append(append(ValidPlanStatuses, ValidMilestoneStatuses...), ValidTaskletStatuses...)
		if !contains(allStatuses, value) {
			return fmt.Errorf("invalid status %q", value)
		}
	case KeyAssignee:
		if !contains(ValidRoles[:3], value) { // oc, cc, cx only
			return fmt.Errorf("invalid assignee %q, must be one of: %v", value, ValidRoles[:3])
		}
	}

	return nil
}

// Validate checks both key and value.
func Validate(key, value string) error {
	if err := ValidateKey(key); err != nil {
		return err
	}
	return ValidateValue(key, value)
}

// Format returns a "key:value" label string.
func Format(key, value string) string {
	return key + ":" + value
}

// Parse splits a "key:value" label string.
func Parse(label string) (key, value string, err error) {
	idx := strings.Index(label, ":")
	if idx == -1 {
		return "", "", fmt.Errorf("invalid label format %q, expected key:value", label)
	}
	return label[:idx], label[idx+1:], nil
}

// ParseAndValidate parses and validates a label string.
func ParseAndValidate(label string) (key, value string, err error) {
	key, value, err = Parse(label)
	if err != nil {
		return "", "", err
	}
	if err := Validate(key, value); err != nil {
		return key, value, err
	}
	return key, value, nil
}

// NormalizeKey converts common variations to canonical form.
func NormalizeKey(key string) string {
	// Handle common variations
	switch strings.ToLower(key) {
	case "chkid", "chk-id", "checkpoint_id":
		return KeyChkID
	case "sessionlogpath", "session-log-path", "log_path":
		return KeySessionLogPath
	case "planid", "plan-id":
		return KeyPlanID
	case "milestoneid", "milestone-id":
		return KeyMilestoneID
	case "taskletid", "tasklet-id":
		return KeyTaskletID
	case "milestonenum", "milestone-num":
		return KeyMilestoneNum
	case "chunknum", "chunk-num":
		return KeyChunkNum
	case "chunkindex", "chunk-index":
		return KeyChunkIndex
	case "startoffset", "start-offset":
		return KeyStartOffset
	case "endoffset", "end-offset":
		return KeyEndOffset
	case "overlapstart", "overlap-start":
		return KeyOverlapStart
	case "chunkrange", "chunk-range":
		return KeyChunkRange
	case "rollupnum", "rollup-num":
		return KeyRollupNum
	case "chunksincluded", "chunks-included":
		return KeyChunksIncluded
	case "totalchunks", "total-chunks":
		return KeyTotalChunks
	case "createdat", "created-at":
		return KeyCreatedAt
	case "byteoffset", "byte-offset":
		return KeyByteOffset
	default:
		return strings.ToLower(strings.ReplaceAll(key, "-", "_"))
	}
}

// contains checks if a slice contains a string.
func contains(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

// LabelSet is a helper for building label arguments.
type LabelSet struct {
	labels []string
}

// NewLabelSet creates a new label set.
func NewLabelSet() *LabelSet {
	return &LabelSet{labels: make([]string, 0)}
}

// Add adds a label to the set.
func (ls *LabelSet) Add(key, value string) *LabelSet {
	ls.labels = append(ls.labels, Format(key, value))
	return ls
}

// AddIf adds a label only if the value is non-empty.
func (ls *LabelSet) AddIf(key, value string) *LabelSet {
	if value != "" {
		ls.labels = append(ls.labels, Format(key, value))
	}
	return ls
}

// Args returns the labels as --label arguments for bd CLI.
func (ls *LabelSet) Args() []string {
	args := make([]string, 0, len(ls.labels)*2)
	for _, l := range ls.labels {
		args = append(args, "--label", l)
	}
	return args
}

// Strings returns the raw label strings.
func (ls *LabelSet) Strings() []string {
	return ls.labels
}
