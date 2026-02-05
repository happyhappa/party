package summarywatcher

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// WatcherState holds persisted watcher state for restart recovery.
type WatcherState struct {
	// File tracking
	LastByteOffset int64 `json:"last_byte_offset"`
	LastChunkEnd   int64 `json:"last_chunk_end"`

	// Chunk tracking
	ChunkCount      int    `json:"chunk_count"`
	LastOverlapText string `json:"last_overlap_text"`

	// Rollup tracking
	ChunksSinceRollup int      `json:"chunks_since_rollup"`
	RecentSummaries   []string `json:"recent_summaries"`
}

// saveState persists watcher state to disk.
func (w *Watcher) saveState() error {
	if w.cfg.StateDir == "" {
		return nil
	}

	w.mu.Lock()
	state := WatcherState{
		LastByteOffset:    w.lastByteOffset,
		LastChunkEnd:      w.lastChunkEnd,
		ChunkCount:        w.chunkCount,
		LastOverlapText:   w.lastOverlapText,
		ChunksSinceRollup: w.chunksSinceRollup,
		RecentSummaries:   w.recentSummaries,
	}
	w.mu.Unlock()

	path := w.statePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}

	// Atomic write
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

// loadState restores watcher state from disk.
func (w *Watcher) loadState() error {
	if w.cfg.StateDir == "" {
		return nil
	}

	path := w.statePath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // No state file, start fresh
		}
		return err
	}

	var state WatcherState
	if err := json.Unmarshal(data, &state); err != nil {
		return err
	}

	w.mu.Lock()
	w.lastByteOffset = state.LastByteOffset
	w.lastChunkEnd = state.LastChunkEnd
	w.chunkCount = state.ChunkCount
	w.lastOverlapText = state.LastOverlapText
	w.chunksSinceRollup = state.ChunksSinceRollup
	if state.RecentSummaries != nil {
		w.recentSummaries = state.RecentSummaries
	}
	w.mu.Unlock()

	return nil
}

// statePath returns the path to the state file.
func (w *Watcher) statePath() string {
	filename := "summarywatcher-" + w.cfg.Role + "-state.json"
	return filepath.Join(w.cfg.StateDir, filename)
}

// ResetState clears all state (for testing or manual reset).
func (w *Watcher) ResetState() {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.lastByteOffset = 0
	w.lastChunkEnd = 0
	w.chunkCount = 0
	w.lastOverlapText = ""
	w.chunksSinceRollup = 0
	w.recentSummaries = make([]string, 0, w.cfg.ChunksPerRollup)

	// Remove state file
	if w.cfg.StateDir != "" {
		_ = os.Remove(w.statePath())
	}
}
