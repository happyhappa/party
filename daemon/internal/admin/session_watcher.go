package admin

import (
	"context"
	"os"
	"sync"
	"time"
)

// SessionLogWatcher monitors a session log file for changes via polling.
type SessionLogWatcher struct {
	path     string
	interval time.Duration
	lastSize int64
	lastMod  time.Time

	mu sync.Mutex
}

// NewSessionLogWatcher creates a new watcher for a session log path.
func NewSessionLogWatcher(path string) (*SessionLogWatcher, error) {
	w := &SessionLogWatcher{
		path:     path,
		interval: 10 * time.Second, // Poll every 10s
	}

	// Initialize size tracking
	if info, err := os.Stat(path); err == nil {
		w.lastSize = info.Size()
		w.lastMod = info.ModTime()
	}

	return w, nil
}

// Start begins watching for changes.
// Calls onChange(true) when the file grows, onChange(false) for other changes.
func (w *SessionLogWatcher) Start(ctx context.Context, onChange func(growth bool)) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			growth, changed := w.checkChange()
			if changed {
				onChange(growth)
			}
		}
	}
}

// checkChange checks if the file has changed since last check.
// Returns (grew, changed).
func (w *SessionLogWatcher) checkChange() (bool, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()

	info, err := os.Stat(w.path)
	if err != nil {
		return false, false
	}

	size := info.Size()
	mod := info.ModTime()

	grew := size > w.lastSize
	changed := size != w.lastSize || !mod.Equal(w.lastMod)

	w.lastSize = size
	w.lastMod = mod

	return grew, changed
}

// Close cleans up the watcher (no-op for polling watcher).
func (w *SessionLogWatcher) Close() error {
	return nil
}
