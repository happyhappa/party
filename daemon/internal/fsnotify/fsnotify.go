package fsnotify

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Op describes a set of file operations.
type Op uint32

const (
	Create Op = 1 << iota
	Write
	Remove
	Rename
	Chmod
)

// Event represents a single file system notification.
type Event struct {
	Name string
	Op   Op
}

// Watcher polls watched directories and emits fsnotify-style events.
type Watcher struct {
	Events chan Event
	Errors chan error

	mu      sync.Mutex
	paths   map[string]struct{}
	state   map[string]time.Time
	ticker  *time.Ticker
	done    chan struct{}
	stopped bool
}

// NewWatcher creates a polling watcher.
func NewWatcher() (*Watcher, error) {
	w := &Watcher{
		Events: make(chan Event, 128),
		Errors: make(chan error, 1),
		paths:  make(map[string]struct{}),
		state:  make(map[string]time.Time),
		ticker: time.NewTicker(500 * time.Millisecond),
		done:   make(chan struct{}),
	}

	go w.loop()
	return w, nil
}

// Add begins watching a directory path.
func (w *Watcher) Add(name string) error {
	info, err := os.Stat(name)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("fsnotify: only directory watching supported")
	}

	w.mu.Lock()
	w.paths[name] = struct{}{}
	w.mu.Unlock()
	return nil
}

// Remove stops watching a path.
func (w *Watcher) Remove(name string) error {
	w.mu.Lock()
	delete(w.paths, name)
	w.mu.Unlock()
	return nil
}

// Close stops the watcher.
func (w *Watcher) Close() error {
	w.mu.Lock()
	if w.stopped {
		w.mu.Unlock()
		return nil
	}
	w.stopped = true
	w.mu.Unlock()

	close(w.done)
	w.ticker.Stop()
	close(w.Events)
	close(w.Errors)
	return nil
}

func (w *Watcher) loop() {
	for {
		select {
		case <-w.done:
			return
		case <-w.ticker.C:
			w.scan()
		}
	}
}

func (w *Watcher) scan() {
	w.mu.Lock()
	paths := make([]string, 0, len(w.paths))
	for path := range w.paths {
		paths = append(paths, path)
	}
	w.mu.Unlock()

	seen := make(map[string]struct{})

	for _, dir := range paths {
		entries, err := os.ReadDir(dir)
		if err != nil {
			w.sendError(err)
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			info, err := entry.Info()
			if err != nil {
				w.sendError(err)
				continue
			}
			seen[path] = struct{}{}
			mod := info.ModTime()

			w.mu.Lock()
			prev, ok := w.state[path]
			if !ok {
				w.state[path] = mod
				w.mu.Unlock()
				w.sendEvent(Event{Name: path, Op: Create})
				continue
			}
			if mod.After(prev) {
				w.state[path] = mod
				w.mu.Unlock()
				w.sendEvent(Event{Name: path, Op: Write})
				continue
			}
			w.mu.Unlock()
		}
	}

	w.mu.Lock()
	for path := range w.state {
		if _, ok := seen[path]; !ok {
			delete(w.state, path)
			w.mu.Unlock()
			w.sendEvent(Event{Name: path, Op: Remove})
			w.mu.Lock()
		}
	}
	w.mu.Unlock()
}

func (w *Watcher) sendEvent(event Event) {
	w.Events <- event
}

func (w *Watcher) sendError(err error) {
	select {
	case w.Errors <- err:
	default:
	}
}
