package envelope

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"
)

// Envelope defines the JSONL message schema for relay communication.
type Envelope struct {
	MsgID     string `json:"msg_id"`      // "msg-a1b2c3d4"
	Timestamp string `json:"ts"`          // ISO8601
	ProjectID string `json:"project_id"`  // "leaseupcre"
	From      string `json:"from"`        // "oc", "cc", "cx", "relay", "human"
	To        string `json:"to"`          // "oc", "cc", "cx"
	Kind      string `json:"kind"`        // "chat", "command", "event", "ack", "nag"
	Priority  int    `json:"priority"`    // 0=urgent, 1=normal, 2=low
	ThreadID  string `json:"thread_id"`   // "atk-x1y2z3"
	Payload   string `json:"payload"`     // The actual message
	Ephemeral bool   `json:"ephemeral"`   // Don't sync to S3 if true
}

// NewEnvelope creates a new envelope with a generated message ID and timestamp.
func NewEnvelope(from, to, kind, payload string) *Envelope {
	return &Envelope{
		MsgID:     GenerateMsgID(),
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		From:      from,
		To:        to,
		Kind:      kind,
		Payload:   payload,
		Priority:  1,
	}
}

// GenerateMsgID returns a msg- prefixed 8-hex identifier.
func GenerateMsgID() string {
	buf := make([]byte, 4)
	if _, err := rand.Read(buf); err != nil {
		// Fallback to time-based bytes if crypto/rand fails.
		n := time.Now().UnixNano()
		buf[0] = byte(n)
		buf[1] = byte(n >> 8)
		buf[2] = byte(n >> 16)
		buf[3] = byte(n >> 24)
	}

	return "msg-" + hex.EncodeToString(buf)
}

// Validate checks required fields for basic message integrity.
func (e *Envelope) Validate() error {
	if e == nil {
		return errors.New("envelope: nil")
	}
	if e.MsgID == "" {
		return errors.New("envelope: missing msg_id")
	}
	if e.From == "" {
		return errors.New("envelope: missing from")
	}
	if e.To == "" {
		return errors.New("envelope: missing to")
	}
	if e.Kind == "" {
		return errors.New("envelope: missing kind")
	}
	return nil
}
