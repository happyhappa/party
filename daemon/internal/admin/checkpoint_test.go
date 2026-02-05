package admin

import (
	"encoding/json"
	"testing"
)

func TestParseCheckpointContent(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		wantErr bool
		wantCC  *CheckpointContent
	}{
		{
			name: "valid content",
			payload: `{
				"chk_id": "chk-abc123",
				"role": "cc",
				"content": "## Current Goal\nImplement feature X",
				"title": "CC checkpoint",
				"labels": {"task": "RFC-002"}
			}`,
			wantErr: false,
			wantCC: &CheckpointContent{
				ChkID:   "chk-abc123",
				Role:    "cc",
				Content: "## Current Goal\nImplement feature X",
				Title:   "CC checkpoint",
				Labels:  map[string]string{"task": "RFC-002"},
			},
		},
		{
			name:    "missing chk_id",
			payload: `{"role": "cc", "content": "test"}`,
			wantErr: true,
		},
		{
			name:    "missing role",
			payload: `{"chk_id": "chk-123", "content": "test"}`,
			wantErr: true,
		},
		{
			name:    "missing content",
			payload: `{"chk_id": "chk-123", "role": "cc"}`,
			wantErr: true,
		},
		{
			name:    "invalid json",
			payload: `{invalid`,
			wantErr: true,
		},
		{
			name:    "empty payload",
			payload: `{}`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cc, err := ParseCheckpointContent(tt.payload)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseCheckpointContent() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr {
				return
			}

			if cc.ChkID != tt.wantCC.ChkID {
				t.Errorf("ChkID = %q, want %q", cc.ChkID, tt.wantCC.ChkID)
			}
			if cc.Role != tt.wantCC.Role {
				t.Errorf("Role = %q, want %q", cc.Role, tt.wantCC.Role)
			}
			if cc.Content != tt.wantCC.Content {
				t.Errorf("Content = %q, want %q", cc.Content, tt.wantCC.Content)
			}
			if cc.Title != tt.wantCC.Title {
				t.Errorf("Title = %q, want %q", cc.Title, tt.wantCC.Title)
			}
		})
	}
}

func TestFormatCheckpointContent(t *testing.T) {
	payload, err := FormatCheckpointContent(
		"chk-test123",
		"oc",
		"## Goals\n- Complete task",
		"OC checkpoint",
		map[string]string{"phase": "3.4"},
	)
	if err != nil {
		t.Fatalf("FormatCheckpointContent error: %v", err)
	}

	// Parse it back to verify
	var cc CheckpointContent
	if err := json.Unmarshal([]byte(payload), &cc); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	if cc.ChkID != "chk-test123" {
		t.Errorf("ChkID = %q, want %q", cc.ChkID, "chk-test123")
	}
	if cc.Role != "oc" {
		t.Errorf("Role = %q, want %q", cc.Role, "oc")
	}
	if cc.Labels["phase"] != "3.4" {
		t.Errorf("Labels[phase] = %q, want %q", cc.Labels["phase"], "3.4")
	}
}

func TestCheckpointContentKind(t *testing.T) {
	if CheckpointContentKind != "checkpoint_content" {
		t.Errorf("CheckpointContentKind = %q, want %q", CheckpointContentKind, "checkpoint_content")
	}
}

func TestHandleCheckpointContentRejectsUnsolicited(t *testing.T) {
	admin := newTestAdmin()

	// No pending request exists - should reject
	payload := `{"chk_id":"chk-unsolicited","role":"cc","content":"test content"}`
	admin.HandleCheckpointContent("cc", payload)

	// Verify no state changes occurred (cooldown should not be set)
	if !admin.cooldownUntil["cc"].IsZero() {
		t.Error("cooldown should not be set for unsolicited checkpoint")
	}
	if !admin.lastCheckpointTime["cc"].IsZero() {
		t.Error("lastCheckpointTime should not be set for unsolicited checkpoint")
	}
}

func TestHandleCheckpointContentRejectsStaleChkID(t *testing.T) {
	admin := newTestAdmin()

	// Set up a pending request with different chk_id
	admin.pendingRequests["cc"] = &PendingCheckpoint{
		ChkID: "chk-expected",
		Role:  "cc",
	}

	// Send content with wrong chk_id - should reject
	payload := `{"chk_id":"chk-wrong","role":"cc","content":"test content"}`
	admin.HandleCheckpointContent("cc", payload)

	// Verify pending request was NOT cleared
	if _, ok := admin.pendingRequests["cc"]; !ok {
		t.Error("pending request should not be cleared for wrong chk_id")
	}
}

func TestHandleCheckpointContentRejectsRoleMismatch(t *testing.T) {
	admin := newTestAdmin()

	// Set up a pending request for cc
	admin.pendingRequests["cc"] = &PendingCheckpoint{
		ChkID: "chk-test",
		Role:  "cc",
	}

	// Send content claiming to be from cc but payload says role=oc
	payload := `{"chk_id":"chk-test","role":"oc","content":"test content"}`
	admin.HandleCheckpointContent("cc", payload)

	// Verify pending request was NOT cleared (role mismatch)
	if _, ok := admin.pendingRequests["cc"]; !ok {
		t.Error("pending request should not be cleared for role mismatch")
	}
}
