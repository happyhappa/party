package log

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// EventVersion is the current relay event schema version.
const EventVersion = 1

// Event captures a relay activity record (RFC-002 schema).
type Event struct {
	// RFC-002 fields
	Version   int    `json:"v"`                   // Schema version, always 1
	TimestampMs int64  `json:"ts_ms"`              // Unix milliseconds
	EventID   string `json:"event_id"`            // "evt-abc123"
	Type      string `json:"type"`                // "checkpoint_request", "checkpoint_ack", "message_routed", "timeout", etc.
	From      string `json:"from,omitempty"`      // "admin", "oc", "cc", "cx"
	To        string `json:"to,omitempty"`        // "admin", "oc", "cc", "cx", "all"
	ChkID     string `json:"chk_id,omitempty"`    // Checkpoint correlation ID
	Status    string `json:"status,omitempty"`    // "success", "fail", "timeout"

	// Extended fields (backwards compat / debugging)
	MsgID     string  `json:"msg_id,omitempty"`    // Message ID that triggered this event (for tracing)
	Error     string  `json:"error,omitempty"`     // Error message if applicable
	LatencyMs float64 `json:"latency_ms,omitempty"` // Operation latency in milliseconds
	Count     int     `json:"count,omitempty"`     // Count for batch operations
}

// WithMsgID sets the message ID for tracing which message triggered the event.
func (e Event) WithMsgID(msgID string) Event {
	e.MsgID = msgID
	return e
}

// WithError sets the error field.
func (e Event) WithError(err string) Event {
	e.Error = err
	return e
}

// WithChkID sets the checkpoint correlation ID.
func (e Event) WithChkID(chkID string) Event {
	e.ChkID = chkID
	return e
}

// WithStatus sets the status field.
func (e Event) WithStatus(status string) Event {
	e.Status = status
	return e
}

// WithLatency sets the latency field in milliseconds.
func (e Event) WithLatency(latencyMs float64) Event {
	e.LatencyMs = latencyMs
	return e
}

// WithCount sets the count field for batch operations.
func (e Event) WithCount(count int) Event {
	e.Count = count
	return e
}

// EventType constants for RFC-002 compliance.
const (
	EventTypeCheckpointRequest = "checkpoint_request"
	EventTypeCheckpointAck     = "checkpoint_ack"
	EventTypeMessageRouted     = "message_routed"
	EventTypeTimeout           = "timeout"
	EventTypeReceived          = "received"
	EventTypeEnqueue           = "enqueue"
	EventTypeDequeue           = "dequeue"
	EventTypeInject            = "inject"
	EventTypeBlocked           = "blocked"
	EventTypePaneTailError     = "pane_tail_error"
)

// GenerateEventID returns an evt- prefixed 8-hex identifier.
func GenerateEventID() string {
	buf := make([]byte, 4)
	if _, err := rand.Read(buf); err != nil {
		n := time.Now().UnixNano()
		buf[0] = byte(n)
		buf[1] = byte(n >> 8)
		buf[2] = byte(n >> 16)
		buf[3] = byte(n >> 24)
	}
	return "evt-" + hex.EncodeToString(buf)
}

// NewEvent creates a new event with RFC-002 defaults.
func NewEvent(eventType, from, to string) Event {
	return Event{
		Version:     EventVersion,
		TimestampMs: time.Now().UnixMilli(),
		EventID:     GenerateEventID(),
		Type:        eventType,
		From:        from,
		To:          to,
	}
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

	// Apply RFC-002 defaults
	if event.Version == 0 {
		event.Version = EventVersion
	}
	if event.TimestampMs == 0 {
		event.TimestampMs = time.Now().UnixMilli()
	}
	if event.EventID == "" {
		event.EventID = GenerateEventID()
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
