package log

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Event captures a relay activity record.
type Event struct {
	Timestamp string `json:"ts"`
	Kind      string `json:"kind"`
	MsgID     string `json:"msg_id,omitempty"`
	Target    string `json:"target,omitempty"`
	Error     string `json:"error,omitempty"`
}

// EventLog writes append-only JSONL logs.
type EventLog struct {
	path string
	mu   sync.Mutex
}

func NewEventLog(logDir string) *EventLog {
	return &EventLog{path: filepath.Join(logDir, "events.jsonl")}
}

func (l *EventLog) Log(event Event) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if event.Timestamp == "" {
		event.Timestamp = time.Now().UTC().Format(time.RFC3339)
	}

	if err := os.MkdirAll(filepath.Dir(l.path), 0o755); err != nil {
		return err
	}

	file, err := os.OpenFile(l.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()

	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if _, err := file.Write(append(payload, '\n')); err != nil {
		return err
	}

	return nil
}
