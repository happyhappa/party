package summarywatcher

import (
	"bufio"
	"io"
	"os"
)

// findMessageBoundary finds a clean JSONL message boundary near the target offset.
// It searches backwards from targetOffset to find a newline that ends a complete JSON object.
// Returns the offset just after the newline (start of next message).
func (w *Watcher) findMessageBoundary(path string, targetOffset int64) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	// Get file size
	info, err := f.Stat()
	if err != nil {
		return 0, err
	}
	fileSize := info.Size()

	// Clamp target to file size
	if targetOffset > fileSize {
		targetOffset = fileSize
	}

	// Search window: look back up to 64KB to find a boundary
	// Large JSONL messages (e.g., tool outputs) can exceed 4KB
	searchWindowSize := int64(65536)
	searchStart := targetOffset - searchWindowSize
	if searchStart < 0 {
		searchStart = 0
	}

	// Seek to search start
	if _, err := f.Seek(searchStart, io.SeekStart); err != nil {
		return 0, err
	}

	// Read the search window
	windowSize := targetOffset - searchStart
	buf := make([]byte, windowSize)
	n, err := io.ReadFull(f, buf)
	if err != nil && err != io.ErrUnexpectedEOF {
		return 0, err
	}
	buf = buf[:n]

	// Find the last newline in the buffer (end of a complete line)
	lastNewline := -1
	for i := len(buf) - 1; i >= 0; i-- {
		if buf[i] == '\n' {
			lastNewline = i
			break
		}
	}

	if lastNewline == -1 {
		// No newline found in 64KB window - this is a very large message
		// Fall back to target offset, but log warning
		// In practice, caller should handle incomplete JSON gracefully
		return targetOffset, nil
	}

	// Return offset just after the newline
	return searchStart + int64(lastNewline) + 1, nil
}

// countNewBytes returns the number of new bytes since last processed offset.
func (w *Watcher) countNewBytes(path string) (int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}

	w.mu.Lock()
	lastOffset := w.lastByteOffset
	w.mu.Unlock()

	newBytes := info.Size() - lastOffset
	if newBytes < 0 {
		// File was truncated, reset
		return info.Size(), nil
	}
	return newBytes, nil
}

// readChunk reads a chunk of bytes from the session log.
func (w *Watcher) readChunk(path string, startOffset, endOffset int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	if _, err := f.Seek(startOffset, io.SeekStart); err != nil {
		return nil, err
	}

	size := endOffset - startOffset
	buf := make([]byte, size)
	n, err := io.ReadFull(f, buf)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return nil, err
	}

	return buf[:n], nil
}

// countMessagesInRange counts complete JSONL messages in a byte range.
func (w *Watcher) countMessagesInRange(path string, startOffset, endOffset int64) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	if _, err := f.Seek(startOffset, io.SeekStart); err != nil {
		return 0, err
	}

	// Limit reader to our range
	limitReader := io.LimitReader(f, endOffset-startOffset)
	scanner := bufio.NewScanner(limitReader)

	count := 0
	for scanner.Scan() {
		line := scanner.Text()
		if line != "" && line[0] == '{' {
			count++
		}
	}

	return count, scanner.Err()
}
