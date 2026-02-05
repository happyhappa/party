package admin

import (
	"testing"

	"github.com/norm/relay-daemon/pkg/envelope"
)

func TestMessageRouterRoutesToAdmin(t *testing.T) {
	admin := newTestAdmin()
	forwardCalled := false
	router := NewMessageRouter(admin, func(env *envelope.Envelope) error {
		forwardCalled = true
		return nil
	})

	// Message to admin should be handled locally
	env := envelope.NewEnvelope("cc", "admin", CheckpointContentKind, `{"chk_id":"chk-123","role":"cc","content":"test"}`)
	handled, err := router.Route(env)
	if err != nil {
		t.Fatalf("Route error: %v", err)
	}
	if !handled {
		t.Error("expected message to admin to be handled locally")
	}
	if forwardCalled {
		t.Error("expected forward to NOT be called for admin message")
	}
}

func TestMessageRouterForwardsNonAdmin(t *testing.T) {
	admin := newTestAdmin()
	forwardCalled := false
	router := NewMessageRouter(admin, func(env *envelope.Envelope) error {
		forwardCalled = true
		return nil
	})

	// Message to cc should be forwarded
	env := envelope.NewEnvelope("oc", "cc", "chat", "hello")
	handled, err := router.Route(env)
	if err != nil {
		t.Fatalf("Route error: %v", err)
	}
	if handled {
		t.Error("expected message to cc to NOT be handled locally")
	}
	if !forwardCalled {
		t.Error("expected forward to be called for non-admin message")
	}
}

func TestParseCheckpointACK(t *testing.T) {
	tests := []struct {
		payload    string
		wantChkID  string
		wantStatus string
	}{
		{
			payload:    "chk_id=chk-abc123 status=success",
			wantChkID:  "chk-abc123",
			wantStatus: "success",
		},
		{
			payload:    "chk_id=chk-xyz789",
			wantChkID:  "chk-xyz789",
			wantStatus: "",
		},
		{
			payload:    "status=failed chk_id=chk-fail",
			wantChkID:  "chk-fail",
			wantStatus: "failed",
		},
		{
			payload:    "",
			wantChkID:  "",
			wantStatus: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.payload, func(t *testing.T) {
			chkID, status := parseCheckpointACK(tt.payload)
			if chkID != tt.wantChkID {
				t.Errorf("chkID = %q, want %q", chkID, tt.wantChkID)
			}
			if status != tt.wantStatus {
				t.Errorf("status = %q, want %q", status, tt.wantStatus)
			}
		})
	}
}

func TestRouterNilEnvelope(t *testing.T) {
	router := NewMessageRouter(nil, nil)
	handled, err := router.Route(nil)
	if err != nil {
		t.Fatalf("Route error: %v", err)
	}
	if handled {
		t.Error("expected nil envelope to not be handled")
	}
}
