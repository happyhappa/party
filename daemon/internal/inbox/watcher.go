package inbox

import (
	"bytes"
	"context"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"
	"github.com/norm/relay-daemon/pkg/envelope"
)

// Watcher monitors inbox files and emits new envelopes.
type Watcher struct {
	inboxDir string
	watcher  *fsnotify.Watcher
	events   chan *envelope.Envelope
	mu       sync.Mutex
	offsets  map[string]int64
	rest     map[string][]byte
	valid    map[string]struct{}
}

func NewWatcher(inboxDir string) (*Watcher, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	return &Watcher{
		inboxDir: inboxDir,
		watcher:  watcher,
		events:   make(chan *envelope.Envelope, 1024),
		offsets:  make(map[string]int64),
		rest:     make(map[string][]byte),
		valid: map[string]struct{}{
			"oc":  {},
			"cc":  {},
			"cx":  {},
			"vog": {},
		},
	}, nil
}

func (w *Watcher) Events() <-chan *envelope.Envelope {
	return w.events
}

func (w *Watcher) Start(ctx context.Context) error {
	if err := w.watcher.Add(w.inboxDir); err != nil {
		return err
	}

	if err := w.readExisting(); err != nil {
		return err
	}

	for {
		select {
	case <-ctx.Done():
		close(w.events)
		return nil
	case err := <-w.watcher.Errors:
		if err != nil {
			return err
		}
		case event := <-w.watcher.Events:
			if event.Op&(fsnotify.Write|fsnotify.Create) != 0 {
				if err := w.readNew(event.Name); err != nil {
					return err
				}
			}
	if event.Op&(fsnotify.Remove|fsnotify.Rename) != 0 {
		w.mu.Lock()
		delete(w.offsets, event.Name)
		delete(w.rest, event.Name)
		w.mu.Unlock()
	}
		}
	}
}

func (w *Watcher) Close() error {
	return w.watcher.Close()
}

// SaveOffsets writes offset state to disk for replay avoidance.
func (w *Watcher) SaveOffsets(path string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return saveOffsets(path, w.offsets)
}

// SetOffsets replaces the current offsets map.
func (w *Watcher) SetOffsets(offsets map[string]int64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if offsets == nil {
		w.offsets = make(map[string]int64)
		return
	}
	w.offsets = offsets
}

func (w *Watcher) readExisting() error {
	pattern := filepath.Join(w.inboxDir, "*.jsonl")
	files, err := filepath.Glob(pattern)
	if err != nil {
		return err
	}
	for _, path := range files {
		if err := w.readNew(path); err != nil {
			return err
		}
	}
	return nil
}

func (w *Watcher) readNew(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			log.Printf("outbox file missing %s (skipping)", path)
			return nil
		}
		return err
	}
	if info.IsDir() {
		return nil
	}

	w.mu.Lock()
	offset := w.offsets[path]
	prefix := w.rest[path]
	w.mu.Unlock()

	if offset > info.Size() {
		w.mu.Lock()
		w.offsets[path] = 0
		w.rest[path] = nil
		w.mu.Unlock()
		offset = 0
		prefix = nil
	}

	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			log.Printf("outbox file missing %s (skipping)", path)
			return nil
		}
		return err
	}
	defer file.Close()

	if _, err := file.Seek(offset, 0); err != nil {
		return err
	}

	chunk, err := io.ReadAll(file)
	if err != nil {
		return err
	}
	if len(chunk) == 0 {
		return nil
	}

	agent := agentFromPath(path)
	if !w.isValidAgent(agent) {
		log.Printf("outbox unknown agent %q from %s (skipping)", agent, path)
		return nil
	}
	defaults := Defaults{From: agent}

	payload := append(prefix, chunk...)
	lines := bytes.Split(payload, []byte("\n"))
	lastIdx := len(lines) - 1
	var remainder []byte
	if len(payload) > 0 && payload[len(payload)-1] != '\n' {
		remainder = lines[lastIdx]
		lines = lines[:lastIdx]
	}

	for _, line := range lines {
		env, err := ParseLineWithDefaults(line, defaults)
		if err != nil {
			log.Printf("outbox parse error %s: %v (skipping)", path, err)
			continue
		}
		if env != nil {
			select {
			case w.events <- env:
			default:
				log.Printf("outbox event dropped (channel full): %s -> %s", env.From, env.To)
			}
		}
	}

	w.mu.Lock()
	w.offsets[path] = offset + int64(len(chunk))
	w.rest[path] = remainder
	w.mu.Unlock()

	return nil
}

func agentFromPath(path string) string {
	base := filepath.Base(path)
	name := strings.TrimSuffix(base, filepath.Ext(base))
	return strings.ToLower(name)
}

func (w *Watcher) isValidAgent(agent string) bool {
	_, ok := w.valid[agent]
	return ok
}
