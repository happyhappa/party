package log

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func TestNewEventDefaults(t *testing.T) {
	start := time.Now().UnixMilli()
	evt := NewEvent(EventTypeMessageRouted, "oc", "cc")

	if evt.Version != EventVersion {
		t.Fatalf("expected version %d, got %d", EventVersion, evt.Version)
	}
	if evt.TimestampMs < start {
		t.Fatalf("expected TimestampMs >= %d, got %d", start, evt.TimestampMs)
	}
	if evt.EventID == "" || !strings.HasPrefix(evt.EventID, "evt-") {
		t.Fatalf("expected evt- prefixed event id, got %q", evt.EventID)
	}
	if evt.Type != EventTypeMessageRouted {
		t.Fatalf("expected type %q, got %q", EventTypeMessageRouted, evt.Type)
	}
	if evt.From != "oc" || evt.To != "cc" {
		t.Fatalf("expected from/to oc/cc, got %q/%q", evt.From, evt.To)
	}
}

func TestEventLogSchemaFields(t *testing.T) {
	dir := t.TempDir()
	logger := NewEventLog(dir)

	evt := Event{
		Type:   EventTypeCheckpointAck,
		From:   "cc",
		To:     "admin",
		ChkID:  "chk-abc123",
		Status: "success",
		MsgID:  "msg-1234",
		Error:  "",
	}

	if err := logger.Log(evt); err != nil {
		t.Fatalf("log event: %v", err)
	}

	payload, err := os.ReadFile(dir + "/events.jsonl")
	if err != nil {
		t.Fatalf("read events.jsonl: %v", err)
	}
	line := strings.TrimSpace(string(payload))
	if line == "" {
		t.Fatalf("expected one jsonl line")
	}

	var got map[string]any
	if err := json.Unmarshal([]byte(line), &got); err != nil {
		t.Fatalf("unmarshal event: %v", err)
	}

	// Required RFC-002 fields
	for _, key := range []string{"v", "ts_ms", "event_id", "type", "from", "to"} {
		if _, ok := got[key]; !ok {
			t.Fatalf("missing required field %q in %v", key, got)
		}
	}
	// Optional fields included when set
	for _, key := range []string{"chk_id", "status", "msg_id"} {
		if _, ok := got[key]; !ok {
			t.Fatalf("missing expected field %q in %v", key, got)
		}
	}
	if _, ok := got["ts"]; ok {
		t.Fatalf("unexpected legacy field ts present in %v", got)
	}

	if v, ok := got["v"].(float64); !ok || int(v) != EventVersion {
		t.Fatalf("expected v=%d, got %v", EventVersion, got["v"])
	}
	if _, ok := got["ts_ms"].(float64); !ok {
		t.Fatalf("expected ts_ms numeric, got %T", got["ts_ms"])
	}
	if id, ok := got["event_id"].(string); !ok || !strings.HasPrefix(id, "evt-") {
		t.Fatalf("expected evt- prefixed event_id, got %v", got["event_id"])
	}
}
