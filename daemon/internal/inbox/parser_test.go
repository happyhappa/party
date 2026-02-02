package inbox

import (
	"errors"
	"testing"
)

func TestParseMessageWithDefaultsBasic(t *testing.T) {
	msg := "TO: oc\n---\nhello world"
	env, err := ParseMessageWithDefaults([]byte(msg), Defaults{From: "cx"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if env == nil {
		t.Fatal("expected envelope")
	}
	if env.To != "oc" {
		t.Fatalf("expected To oc, got %q", env.To)
	}
	if env.From != "cx" {
		t.Fatalf("expected From cx, got %q", env.From)
	}
	if env.Payload != "hello world" {
		t.Fatalf("expected payload, got %q", env.Payload)
	}
	if env.MsgID == "" {
		t.Fatal("expected msg id")
	}
	if env.Timestamp == "" {
		t.Fatal("expected timestamp")
	}
	if env.Priority != 1 {
		t.Fatalf("expected priority 1, got %d", env.Priority)
	}
}

func TestParseMessageWithHeaders(t *testing.T) {
	msg := "TO: cc\nFROM: oc\nPRIORITY: 2\nTHREAD: t-1\nKIND: notice\nEPHEMERAL: true\n---\nbody"
	env, err := ParseMessage([]byte(msg))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if env == nil {
		t.Fatal("expected envelope")
	}
	if env.To != "cc" {
		t.Fatalf("expected To cc, got %q", env.To)
	}
	if env.From != "oc" {
		t.Fatalf("expected From oc, got %q", env.From)
	}
	if env.Priority != 2 {
		t.Fatalf("expected priority 2, got %d", env.Priority)
	}
	if env.ThreadID != "t-1" {
		t.Fatalf("expected thread t-1, got %q", env.ThreadID)
	}
	if env.Kind != "notice" {
		t.Fatalf("expected kind notice, got %q", env.Kind)
	}
	if env.Ephemeral != true {
		t.Fatalf("expected ephemeral true, got %v", env.Ephemeral)
	}
	if env.Payload != "body" {
		t.Fatalf("expected payload body, got %q", env.Payload)
	}
}

func TestParseMessagePriorityZero(t *testing.T) {
	msg := "TO: oc\nPRIORITY: 0\n---\nbody"
	env, err := ParseMessage([]byte(msg))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if env == nil {
		t.Fatal("expected envelope")
	}
	if env.Priority != 0 {
		t.Fatalf("expected priority 0, got %d", env.Priority)
	}
}

func TestParseMessageMissingTo(t *testing.T) {
	msg := "FROM: oc\n---\nbody"
	_, err := ParseMessage([]byte(msg))
	if !errors.Is(err, errMissingTo) {
		t.Fatalf("expected missing TO error, got %v", err)
	}
}

func TestParseMessageMissingSeparator(t *testing.T) {
	msg := "TO: oc\nbody"
	env, err := ParseMessage([]byte(msg))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if env == nil {
		t.Fatal("expected envelope")
	}
	if env.To != "oc" {
		t.Fatalf("expected To oc, got %q", env.To)
	}
	if env.Payload != "body" {
		t.Fatalf("expected payload body, got %q", env.Payload)
	}
}

func TestParseMessageLeadingBlankLines(t *testing.T) {
	msg := "\n\nTO: oc\n---\nhello"
	env, err := ParseMessage([]byte(msg))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if env == nil {
		t.Fatal("expected envelope")
	}
	if env.To != "oc" {
		t.Fatalf("expected To oc, got %q", env.To)
	}
	if env.Payload != "hello" {
		t.Fatalf("expected payload hello, got %q", env.Payload)
	}
}
