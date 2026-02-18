package adminpane

import (
	"os"
	"path/filepath"
	"sync"
	"time"
)

const checkpointWriteGracePeriod = 2 * time.Minute

// IdleDetector tracks whether all agents are idle by comparing JSONL session
// file modification times against the last checkpoint injection time.
type IdleDetector struct {
	mu                          sync.Mutex
	lastCheckpointInjectionTime time.Time
	projectDirs                 map[string]string // role → project dir
	backstopInterval            time.Duration
	hasInjected                 bool
}

// NewIdleDetector creates an IdleDetector with the given project directories
// and backstop interval.
func NewIdleDetector(projectDirs map[string]string, backstopInterval time.Duration) *IdleDetector {
	return &IdleDetector{
		lastCheckpointInjectionTime: time.Now(),
		projectDirs:                 projectDirs,
		backstopInterval:            backstopInterval,
	}
}

// RecordCheckpointInjection stores the current time as the last checkpoint injection.
func (d *IdleDetector) RecordCheckpointInjection() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.lastCheckpointInjectionTime = time.Now()
	d.hasInjected = true
}

// AllAgentsIdle returns true if all configured agents' most recent JSONL file
// has an mtime at or before the checkpoint injection time plus grace period.
// Returns false (not idle) if no project dirs are configured.
func (d *IdleDetector) AllAgentsIdle() bool {
	d.mu.Lock()
	dirs := d.projectDirs
	lastInjection := d.lastCheckpointInjectionTime
	injected := d.hasInjected
	d.mu.Unlock()

	if len(dirs) == 0 || !injected {
		return false
	}

	cutoff := lastInjection.Add(checkpointWriteGracePeriod)
	for _, dir := range dirs {
		latest, err := latestJSONLMtime(dir)
		if err != nil {
			// Can't determine state — assume active
			return false
		}
		if latest.After(cutoff) {
			return false
		}
	}
	return true
}

// ShouldBackstop returns true if it has been longer than the backstop interval
// since the last checkpoint injection. This ensures checkpoints still fire
// periodically even during long idle stretches.
func (d *IdleDetector) ShouldBackstop() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return time.Since(d.lastCheckpointInjectionTime) > d.backstopInterval
}

// latestJSONLMtime finds the most recently modified .jsonl file in dir and
// returns its modification time.
func latestJSONLMtime(dir string) (time.Time, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if err != nil {
		return time.Time{}, err
	}
	if len(matches) == 0 {
		return time.Time{}, os.ErrNotExist
	}

	var latest time.Time
	for _, path := range matches {
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		if info.ModTime().After(latest) {
			latest = info.ModTime()
		}
	}
	if latest.IsZero() {
		return time.Time{}, os.ErrNotExist
	}
	return latest, nil
}
