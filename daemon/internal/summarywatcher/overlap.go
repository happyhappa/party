package summarywatcher

import (
	"strings"
)

// extractChunkWithOverlap extracts chunk content with overlap from previous chunk.
// The overlap provides context continuity between chunks.
func (w *Watcher) extractChunkWithOverlap(path string, startOffset, endOffset int64) (string, error) {
	// Read the main chunk
	chunkBytes, err := w.readChunk(path, startOffset, endOffset)
	if err != nil {
		return "", err
	}

	content := string(chunkBytes)

	// Prepend overlap from previous chunk if available
	w.mu.Lock()
	overlap := w.lastOverlapText
	w.mu.Unlock()

	if overlap != "" {
		content = "--- Previous context ---\n" + overlap + "\n--- Current content ---\n" + content
	}

	// Extract overlap for next chunk (from end of current chunk)
	newOverlap := w.extractOverlapText(string(chunkBytes))

	w.mu.Lock()
	w.lastOverlapText = newOverlap
	w.mu.Unlock()

	return content, nil
}

// extractOverlapText extracts the overlap portion from chunk content.
// Takes approximately OverlapPercent from the end of the content.
func (w *Watcher) extractOverlapText(content string) string {
	if content == "" {
		return ""
	}

	overlapPercent := w.cfg.OverlapPercent
	if overlapPercent <= 0 {
		overlapPercent = 10
	}
	if overlapPercent > 50 {
		overlapPercent = 50
	}

	// Calculate target overlap size
	targetSize := len(content) * overlapPercent / 100
	if targetSize < 100 {
		targetSize = 100 // Minimum overlap
	}
	if targetSize > len(content) {
		return content
	}

	// Find a clean break point (newline) near the target
	startPos := len(content) - targetSize

	// Search forward for a newline to get a clean boundary
	cleanStart := strings.Index(content[startPos:], "\n")
	if cleanStart != -1 {
		startPos = startPos + cleanStart + 1
	}

	if startPos >= len(content) {
		return ""
	}

	return content[startPos:]
}

// calculateOverlapBytes calculates how many bytes of overlap to include.
func (w *Watcher) calculateOverlapBytes() int64 {
	targetChunkBytes := int64(w.cfg.ChunkSizeTokens * w.cfg.BytesPerToken)
	overlapBytes := targetChunkBytes * int64(w.cfg.OverlapPercent) / 100

	// Ensure minimum and maximum bounds
	if overlapBytes < 256 {
		overlapBytes = 256
	}
	maxOverlap := targetChunkBytes / 2
	if overlapBytes > maxOverlap {
		overlapBytes = maxOverlap
	}

	return overlapBytes
}
