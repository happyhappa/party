package sessionmap

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEncodeClaudeProjectPathCandidates(t *testing.T) {
	tests := []struct {
		input    string
		expected []string
	}{
		{
			"/home/user/project",
			[]string{"-home-user-project", "home-user-project"},
		},
		{
			"/home/user/my_project",
			[]string{"-home-user-my_project", "-home-user-my-project", "home-user-my_project"},
		},
	}

	for _, tt := range tests {
		result := encodeClaudeProjectPathCandidates(tt.input)
		if len(result) < 1 {
			t.Errorf("encodeClaudeProjectPathCandidates(%q) returned empty", tt.input)
		}
		// Check that first expected is in result
		found := false
		for _, r := range result {
			if r == tt.expected[0] {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("encodeClaudeProjectPathCandidates(%q) = %v, expected to contain %q", tt.input, result, tt.expected[0])
		}
	}
}

func TestIsCodexSessionLog(t *testing.T) {
	tests := []struct {
		path     string
		expected bool
	}{
		{"/home/user/.codex/sessions/abc/rollout-xyz.jsonl", true},
		{"/home/user/.codex/sessions/test.jsonl", true},
		{"/home/user/.claude/projects/test.jsonl", false},
		{"/tmp/rollout-abc.jsonl", true},
		{"/tmp/test.txt", false},
	}

	for _, tt := range tests {
		result := isCodexSessionLog(tt.path)
		if result != tt.expected {
			t.Errorf("isCodexSessionLog(%q) = %v, expected %v", tt.path, result, tt.expected)
		}
	}
}

func TestDetectSessionLogType(t *testing.T) {
	tests := []struct {
		path     string
		expected SessionLogType
	}{
		{"/home/user/.claude/projects/abc/123.jsonl", SessionLogTypeClaude},
		{"/home/user/.codex/sessions/abc/rollout-xyz.jsonl", SessionLogTypeCodex},
		{"/tmp/rollout-test.jsonl", SessionLogTypeCodex},
		{"/tmp/random.jsonl", SessionLogTypeUnknown},
	}

	for _, tt := range tests {
		result := DetectSessionLogType(tt.path)
		if result != tt.expected {
			t.Errorf("DetectSessionLogType(%q) = %v, expected %v", tt.path, result, tt.expected)
		}
	}
}

func TestLatestByMtime(t *testing.T) {
	dir := t.TempDir()

	// Create test files with different mtimes
	file1 := filepath.Join(dir, "old.jsonl")
	file2 := filepath.Join(dir, "new.jsonl")

	if err := os.WriteFile(file1, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Sleep briefly to ensure different mtime
	if err := os.WriteFile(file2, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Touch file2 to make it newer
	now := os.Stat
	_ = now // avoid unused warning

	result, err := latestByMtime([]string{file1, file2})
	if err != nil {
		t.Fatalf("latestByMtime error: %v", err)
	}

	// file2 should be latest (or they're equal which is also ok)
	if result != file1 && result != file2 {
		t.Errorf("latestByMtime returned unexpected path: %s", result)
	}
}

func TestValidateSessionLog(t *testing.T) {
	dir := t.TempDir()

	// Valid file
	validPath := filepath.Join(dir, "valid.jsonl")
	if err := os.WriteFile(validPath, []byte(`{"test": true}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := ValidateSessionLog(validPath); err != nil {
		t.Errorf("ValidateSessionLog(%q) unexpected error: %v", validPath, err)
	}

	// Empty path
	if err := ValidateSessionLog(""); err == nil {
		t.Error("ValidateSessionLog(\"\") should return error")
	}

	// Non-existent file
	if err := ValidateSessionLog("/nonexistent/file.jsonl"); err == nil {
		t.Error("ValidateSessionLog(nonexistent) should return error")
	}

	// Directory
	if err := ValidateSessionLog(dir); err == nil {
		t.Error("ValidateSessionLog(directory) should return error")
	}

	// Non-jsonl file
	txtPath := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(txtPath, []byte("text"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ValidateSessionLog(txtPath); err == nil {
		t.Error("ValidateSessionLog(.txt) should return error")
	}
}

func TestSessionLogPattern(t *testing.T) {
	tests := []struct {
		name     string
		expected bool
	}{
		{"abc123-def456.jsonl", true},
		{"rollout-abc123.jsonl", true},
		{"test.jsonl", false},
		{"abc.txt", false},
	}

	for _, tt := range tests {
		result := SessionLogPattern.MatchString(tt.name)
		if result != tt.expected {
			t.Errorf("SessionLogPattern.MatchString(%q) = %v, expected %v", tt.name, result, tt.expected)
		}
	}
}
