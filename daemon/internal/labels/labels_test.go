package labels

import (
	"testing"
)

func TestValidateKey(t *testing.T) {
	tests := []struct {
		key     string
		wantErr bool
	}{
		{"role", false},
		{"chk_id", false},
		{"chunk_num", false},
		{"session_log_path", false},
		{"", true},                // empty
		{"Role", true},            // uppercase
		{"chk-id", true},          // hyphen
		{"123abc", true},          // starts with number
		{"chunk num", true},       // space
	}

	for _, tt := range tests {
		err := ValidateKey(tt.key)
		if (err != nil) != tt.wantErr {
			t.Errorf("ValidateKey(%q) error = %v, wantErr %v", tt.key, err, tt.wantErr)
		}
	}
}

func TestValidateValue(t *testing.T) {
	tests := []struct {
		key     string
		value   string
		wantErr bool
	}{
		{KeyRole, "cc", false},
		{KeyRole, "invalid", true},
		{KeySource, "manual", false},
		{KeySource, "autogen", false},
		{KeySource, "unknown", true},
		{KeyConfidence, "high", false},
		{KeyConfidence, "extreme", true},
		{KeyStatus, "active", false},
		{KeyStatus, "pending", false},
		{KeyStatus, "invalid", true},
		{KeyAssignee, "cc", false},
		{KeyAssignee, "admin", true}, // admin can't be assignee
		{KeyRepo, "myrepo", false},   // no validation for repo
		{KeyChkID, "chk-123", false}, // no validation for chk_id
	}

	for _, tt := range tests {
		err := ValidateValue(tt.key, tt.value)
		if (err != nil) != tt.wantErr {
			t.Errorf("ValidateValue(%q, %q) error = %v, wantErr %v", tt.key, tt.value, err, tt.wantErr)
		}
	}
}

func TestParse(t *testing.T) {
	tests := []struct {
		label    string
		wantKey  string
		wantVal  string
		wantErr  bool
	}{
		{"role:cc", "role", "cc", false},
		{"chk_id:chk-123", "chk_id", "chk-123", false},
		{"session_log_path:/path/to/file.jsonl", "session_log_path", "/path/to/file.jsonl", false},
		{"invalid", "", "", true},
		{"", "", "", true},
	}

	for _, tt := range tests {
		key, val, err := Parse(tt.label)
		if (err != nil) != tt.wantErr {
			t.Errorf("Parse(%q) error = %v, wantErr %v", tt.label, err, tt.wantErr)
			continue
		}
		if key != tt.wantKey || val != tt.wantVal {
			t.Errorf("Parse(%q) = (%q, %q), want (%q, %q)", tt.label, key, val, tt.wantKey, tt.wantVal)
		}
	}
}

func TestNormalizeKey(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"chkid", KeyChkID},
		{"chk-id", KeyChkID},
		{"checkpoint_id", KeyChkID},
		{"session-log-path", KeySessionLogPath},
		{"plan-id", KeyPlanID},
		{"chunk-num", KeyChunkNum},
		{"Role", "role"},
		{"some_key", "some_key"},
		{"UPPER-CASE", "upper_case"},
	}

	for _, tt := range tests {
		got := NormalizeKey(tt.input)
		if got != tt.want {
			t.Errorf("NormalizeKey(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestFormat(t *testing.T) {
	got := Format("role", "cc")
	want := "role:cc"
	if got != want {
		t.Errorf("Format() = %q, want %q", got, want)
	}
}

func TestLabelSet(t *testing.T) {
	ls := NewLabelSet().
		Add(KeyRole, "cc").
		Add(KeyChkID, "chk-123").
		AddIf(KeyPlanID, "plan-abc").
		AddIf(KeyMilestoneID, "") // should be skipped

	args := ls.Args()
	if len(args) != 6 { // 3 labels * 2 args each
		t.Errorf("Args() len = %d, want 6", len(args))
	}

	strs := ls.Strings()
	if len(strs) != 3 {
		t.Errorf("Strings() len = %d, want 3", len(strs))
	}

	expected := []string{"role:cc", "chk_id:chk-123", "plan_id:plan-abc"}
	for i, s := range strs {
		if s != expected[i] {
			t.Errorf("Strings()[%d] = %q, want %q", i, s, expected[i])
		}
	}
}

func TestValidStatuses(t *testing.T) {
	// Verify all status lists are valid
	for _, s := range ValidPlanStatuses {
		if err := ValidateValue(KeyStatus, s); err != nil {
			t.Errorf("ValidPlanStatuses %q should be valid: %v", s, err)
		}
	}
	for _, s := range ValidMilestoneStatuses {
		if err := ValidateValue(KeyStatus, s); err != nil {
			t.Errorf("ValidMilestoneStatuses %q should be valid: %v", s, err)
		}
	}
	for _, s := range ValidTaskletStatuses {
		if err := ValidateValue(KeyStatus, s); err != nil {
			t.Errorf("ValidTaskletStatuses %q should be valid: %v", s, err)
		}
	}
}
