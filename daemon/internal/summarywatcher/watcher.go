// Package summarywatcher implements RFC-002 session log summary generation.
// It watches session logs and generates chunk summaries and state rollups.
package summarywatcher

import (
	"context"
	"os"
	"sync"
	"time"

	"github.com/norm/relay-daemon/internal/haiku"
)

// Config holds summary watcher configuration.
type Config struct {
	// Session log path to watch
	SessionLogPath string

	// Role identifier (cc, cx, oc)
	Role string

	// Chunk settings
	ChunkSizeTokens   int // Target chunk size (~4k tokens per RFC-002)
	BytesPerToken     int // Bytes per token estimate (default: 4)
	OverlapPercent    int // Overlap percentage (default: 10)
	MinChunkSizeBytes int // Minimum bytes before considering a chunk

	// Rollup settings
	ChunksPerRollup int // Generate rollup every N chunks (default: 5)

	// Timing
	PollInterval time.Duration // How often to check for new content

	// State persistence
	StateDir string

	// Haiku client for summarization
	HaikuClient *haiku.Client
}

// DefaultConfig returns sensible defaults per RFC-002.
func DefaultConfig() *Config {
	return &Config{
		ChunkSizeTokens:   4000, // RFC-002 specifies ~4k tokens per chunk
		BytesPerToken:     4,
		OverlapPercent:    10,
		MinChunkSizeBytes: 1024, // At least 1KB before processing
		ChunksPerRollup:   5,
		PollInterval:      30 * time.Second,
	}
}

// Watcher monitors a session log and generates summaries.
type Watcher struct {
	cfg *Config

	mu sync.Mutex

	// File tracking
	lastByteOffset int64 // Last processed byte offset
	lastChunkEnd   int64 // End of last chunk (for overlap)

	// Chunk tracking
	chunkCount      int    // Number of chunks generated
	lastOverlapText string // Overlap text from previous chunk

	// Rollup tracking
	chunksSinceRollup int      // Chunks since last rollup
	recentSummaries   []string // Recent chunk summaries for rollup

	// State
	running bool
	cancel  context.CancelFunc
}

// New creates a new summary watcher.
func New(cfg *Config) *Watcher {
	if cfg == nil {
		cfg = DefaultConfig()
	}
	return &Watcher{
		cfg:             cfg,
		recentSummaries: make([]string, 0, cfg.ChunksPerRollup),
	}
}

// Start begins watching the session log.
func (w *Watcher) Start(ctx context.Context) error {
	w.mu.Lock()
	if w.running {
		w.mu.Unlock()
		return nil
	}
	w.running = true

	ctx, cancel := context.WithCancel(ctx)
	w.cancel = cancel
	w.mu.Unlock()

	// Load persisted state
	if err := w.loadState(); err != nil {
		// Log warning but continue - will start from beginning
		_ = err
	}

	// Main watch loop
	ticker := time.NewTicker(w.cfg.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			w.mu.Lock()
			w.running = false
			w.mu.Unlock()
			return ctx.Err()
		case <-ticker.C:
			w.checkForNewContent(ctx)
		}
	}
}

// Stop stops the watcher.
func (w *Watcher) Stop() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cancel != nil {
		w.cancel()
	}
}

// checkForNewContent checks for new session log content and processes it.
func (w *Watcher) checkForNewContent(ctx context.Context) {
	w.mu.Lock()
	path := w.cfg.SessionLogPath
	lastOffset := w.lastByteOffset
	w.mu.Unlock()

	// Check file size
	info, err := os.Stat(path)
	if err != nil {
		return // File doesn't exist or inaccessible
	}

	currentSize := info.Size()
	if currentSize <= lastOffset {
		return // No new content
	}

	// Calculate new bytes available
	newBytes := currentSize - lastOffset
	targetChunkBytes := w.cfg.ChunkSizeTokens * w.cfg.BytesPerToken

	// Check if we have enough for a chunk
	if newBytes < int64(w.cfg.MinChunkSizeBytes) {
		return // Not enough new content yet
	}

	// Check if we have a full chunk worth
	if newBytes < int64(targetChunkBytes) {
		return // Wait for more content
	}

	// Process the new chunk
	w.processChunk(ctx, path, lastOffset, currentSize)
}

// processChunk extracts and summarizes a chunk of the session log.
func (w *Watcher) processChunk(ctx context.Context, path string, startOffset, endOffset int64) {
	// Find clean message boundary
	boundaryOffset, err := w.findMessageBoundary(path, endOffset)
	if err != nil {
		return
	}

	// Calculate overlap start (from previous chunk end)
	w.mu.Lock()
	overlapStart := w.lastChunkEnd
	w.mu.Unlock()

	// Extract chunk content with overlap
	content, err := w.extractChunkWithOverlap(path, startOffset, boundaryOffset)
	if err != nil {
		return
	}

	if content == "" {
		return
	}

	// Generate summary
	result, err := w.summarizeChunk(ctx, content)
	if err != nil {
		return
	}

	// Write chunk_summary bead with full metadata
	chunkNum := w.chunkCount + 1
	meta := ChunkMeta{
		ChunkNum:       chunkNum,
		ChunkIndex:     chunkNum - 1, // 0-based index
		StartOffset:    startOffset,
		EndOffset:      boundaryOffset,
		OverlapStart:   overlapStart,
		Source:         result.Source,
		SessionLogPath: path,
	}
	if err := w.writeChunkSummaryBead(result.Content, meta); err != nil {
		return
	}

	// Update state
	w.mu.Lock()
	w.lastByteOffset = boundaryOffset
	w.lastChunkEnd = boundaryOffset
	w.chunkCount = chunkNum
	w.chunksSinceRollup++
	w.recentSummaries = append(w.recentSummaries, result.Content)

	// Check if rollup needed
	needsRollup := w.chunksSinceRollup >= w.cfg.ChunksPerRollup
	summariesForRollup := make([]string, len(w.recentSummaries))
	copy(summariesForRollup, w.recentSummaries)
	w.mu.Unlock()

	// Save state after each chunk
	_ = w.saveState()

	// Generate rollup if needed
	if needsRollup {
		w.generateRollup(ctx, summariesForRollup, path)
	}
}

// generateRollup creates a state rollup from recent chunk summaries.
func (w *Watcher) generateRollup(ctx context.Context, summaries []string, sessionLogPath string) {
	result, err := w.summarizeForRollup(ctx, summaries)
	if err != nil {
		return
	}

	w.mu.Lock()
	chunkCount := w.chunkCount
	lastOffset := w.lastByteOffset
	w.mu.Unlock()

	rollupNum := (chunkCount / w.cfg.ChunksPerRollup)
	meta := RollupMeta{
		RollupNum:      rollupNum,
		ChunksIncluded: w.cfg.ChunksPerRollup,
		TotalChunks:    chunkCount,
		ByteOffset:     lastOffset,
		Source:         result.Source,
		SessionLogPath: sessionLogPath,
	}
	if err := w.writeStateRollupBead(result.Content, meta); err != nil {
		return
	}

	// Reset rollup tracking
	w.mu.Lock()
	w.chunksSinceRollup = 0
	w.recentSummaries = make([]string, 0, w.cfg.ChunksPerRollup)
	w.mu.Unlock()

	_ = w.saveState()
}

// GetState returns current watcher state for external inspection.
func (w *Watcher) GetState() (byteOffset int64, chunkCount int, chunksSinceRollup int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.lastByteOffset, w.chunkCount, w.chunksSinceRollup
}
