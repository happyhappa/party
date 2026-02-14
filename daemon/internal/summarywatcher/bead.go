package summarywatcher

import (
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// ChunkMeta holds metadata about a processed chunk for bead creation.
type ChunkMeta struct {
	ChunkNum      int
	ChunkIndex    int   // 0-based index within session
	StartOffset   int64 // Byte offset where chunk starts
	EndOffset     int64 // Byte offset where chunk ends
	OverlapStart  int64 // Byte offset where overlap begins (0 if no overlap)
	Source        string // "haiku" or "heuristic"
	SessionLogPath string
}

// writeChunkSummaryBead writes a chunk_summary bead via bd CLI.
func (w *Watcher) writeChunkSummaryBead(summary string, meta ChunkMeta) error {
	title := fmt.Sprintf("%s chunk summary #%d", w.cfg.Role, meta.ChunkNum)
	now := time.Now()

	args := []string{
		"create",
		"--type", "chunk_summary",
		"--title", title,
		"--label", "role:" + w.cfg.Role,
		"--label", fmt.Sprintf("chunk_num:%d", meta.ChunkNum),
		"--label", fmt.Sprintf("chunk_index:%d", meta.ChunkIndex),
		"--label", fmt.Sprintf("start_offset:%d", meta.StartOffset),
		"--label", fmt.Sprintf("end_offset:%d", meta.EndOffset),
		"--label", fmt.Sprintf("overlap_start:%d", meta.OverlapStart),
		"--label", fmt.Sprintf("chunk_range:%d-%d", meta.StartOffset, meta.EndOffset),
		"--label", "source:" + meta.Source,
		"--label", "session_log_path:" + meta.SessionLogPath,
		"--label", fmt.Sprintf("created_at:%s", now.Format(time.RFC3339)),
		"--label", fmt.Sprintf("timestamp:%d", now.Unix()),
		"--body", summary,
	}

	cmd := exec.Command("bd", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("bd create chunk_summary: %w: %s", err, string(output))
	}

	return nil
}

// RollupMeta holds metadata about a rollup for bead creation.
type RollupMeta struct {
	RollupNum      int
	ChunksIncluded int
	TotalChunks    int
	ByteOffset     int64
	Source         string // "haiku" or "heuristic"
	SessionLogPath string
}

// writeStateRollupBead writes a state_rollup bead via bd CLI.
func (w *Watcher) writeStateRollupBead(rollup string, meta RollupMeta) error {
	title := fmt.Sprintf("%s state rollup #%d", w.cfg.Role, meta.RollupNum)
	now := time.Now()

	args := []string{
		"create",
		"--type", "state_rollup",
		"--title", title,
		"--label", "role:" + w.cfg.Role,
		"--label", fmt.Sprintf("rollup_num:%d", meta.RollupNum),
		"--label", fmt.Sprintf("chunks_included:%d", meta.ChunksIncluded),
		"--label", fmt.Sprintf("total_chunks:%d", meta.TotalChunks),
		"--label", fmt.Sprintf("byte_offset:%d", meta.ByteOffset),
		"--label", "source:" + meta.Source,
		"--label", "session_log_path:" + meta.SessionLogPath,
		"--label", fmt.Sprintf("created_at:%s", now.Format(time.RFC3339)),
		"--label", fmt.Sprintf("timestamp:%d", now.Unix()),
		"--body", rollup,
	}

	cmd := exec.Command("bd", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("bd create state_rollup: %w: %s", err, string(output))
	}

	return nil
}

// getLatestChunkSummaries retrieves recent chunk summaries for rollup.
func (w *Watcher) getLatestChunkSummaries(count int) ([]string, error) {
	// Query beads for recent chunk summaries
	args := []string{
		"query",
		"--type", "chunk_summary",
		"--label", "role:" + w.cfg.Role,
		"--limit", fmt.Sprintf("%d", count),
		"--format", "body",
	}

	cmd := exec.Command("bd", args...)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("bd query: %w", err)
	}

	// Parse output - each bead body separated by delimiter
	bodies := strings.Split(string(output), "\n---\n")
	var summaries []string
	for _, body := range bodies {
		body = strings.TrimSpace(body)
		if body != "" {
			summaries = append(summaries, body)
		}
	}

	return summaries, nil
}
