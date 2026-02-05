package sessionmap

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSessionMapSetGetMapping(t *testing.T) {
	m := NewSessionMap("test-pod", "")

	mapping := &RoleSessionMapping{
		WorktreePath:   "/path/to/worktree",
		SessionLogPath: "/path/to/session.jsonl",
		SessionLogType: SessionLogTypeClaude,
		Valid:          true,
	}

	m.SetMapping("cc", mapping)

	got, ok := m.GetMapping("cc")
	if !ok {
		t.Fatal("expected mapping to exist")
	}

	if got.WorktreePath != mapping.WorktreePath {
		t.Errorf("WorktreePath = %q, want %q", got.WorktreePath, mapping.WorktreePath)
	}
	if got.SessionLogPath != mapping.SessionLogPath {
		t.Errorf("SessionLogPath = %q, want %q", got.SessionLogPath, mapping.SessionLogPath)
	}
	if got.Role != "cc" {
		t.Errorf("Role = %q, want %q", got.Role, "cc")
	}
}

func TestSessionMapGetSessionLogPath(t *testing.T) {
	m := NewSessionMap("test-pod", "")

	// No mapping
	_, err := m.GetSessionLogPath("cc")
	if err == nil {
		t.Error("expected error for missing mapping")
	}

	// Invalid mapping
	m.SetMapping("cc", &RoleSessionMapping{
		Valid: false,
		Error: "test error",
	})
	_, err = m.GetSessionLogPath("cc")
	if err == nil {
		t.Error("expected error for invalid mapping")
	}

	// Valid mapping
	m.SetMapping("cc", &RoleSessionMapping{
		SessionLogPath: "/path/to/log.jsonl",
		Valid:          true,
	})
	path, err := m.GetSessionLogPath("cc")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if path != "/path/to/log.jsonl" {
		t.Errorf("path = %q, want %q", path, "/path/to/log.jsonl")
	}
}

func TestSessionMapGetAllSessionLogPaths(t *testing.T) {
	m := NewSessionMap("test-pod", "")

	m.SetMapping("oc", &RoleSessionMapping{
		SessionLogPath: "/path/oc.jsonl",
		Valid:          true,
	})
	m.SetMapping("cc", &RoleSessionMapping{
		SessionLogPath: "/path/cc.jsonl",
		Valid:          true,
	})
	m.SetMapping("cx", &RoleSessionMapping{
		SessionLogPath: "",
		Valid:          false,
	})

	paths := m.GetAllSessionLogPaths()

	if len(paths) != 2 {
		t.Errorf("expected 2 paths, got %d", len(paths))
	}
	if paths["oc"] != "/path/oc.jsonl" {
		t.Errorf("oc path = %q, want %q", paths["oc"], "/path/oc.jsonl")
	}
	if paths["cc"] != "/path/cc.jsonl" {
		t.Errorf("cc path = %q, want %q", paths["cc"], "/path/cc.jsonl")
	}
	if _, ok := paths["cx"]; ok {
		t.Error("cx should not be in paths (invalid)")
	}
}

func TestSessionMapSaveLoad(t *testing.T) {
	dir := t.TempDir()

	m := NewSessionMap("test-pod", dir)
	m.SetMapping("cc", &RoleSessionMapping{
		WorktreePath:   "/path/to/worktree",
		SessionLogPath: "/path/to/session.jsonl",
		SessionLogType: SessionLogTypeClaude,
		Valid:          true,
	})

	if err := m.Save(); err != nil {
		t.Fatalf("Save error: %v", err)
	}

	// Verify file exists
	statePath := filepath.Join(dir, "session-map-test-pod.json")
	if _, err := os.Stat(statePath); err != nil {
		t.Fatalf("state file not created: %v", err)
	}

	// Load into new map
	m2 := NewSessionMap("test-pod", dir)
	if err := m2.Load(); err != nil {
		t.Fatalf("Load error: %v", err)
	}

	got, ok := m2.GetMapping("cc")
	if !ok {
		t.Fatal("expected mapping after load")
	}
	if got.SessionLogPath != "/path/to/session.jsonl" {
		t.Errorf("SessionLogPath after load = %q, want %q", got.SessionLogPath, "/path/to/session.jsonl")
	}
}

func TestSessionMapVerify(t *testing.T) {
	dir := t.TempDir()

	// Create a valid session log
	validLog := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(validLog, []byte(`{"test": true}`), 0o644); err != nil {
		t.Fatal(err)
	}

	m := NewSessionMap("test-pod", "")

	// Valid path
	m.SetMapping("cc", &RoleSessionMapping{
		SessionLogPath: validLog,
		Valid:          true,
	})

	// Invalid path
	m.SetMapping("cx", &RoleSessionMapping{
		SessionLogPath: "/nonexistent/path.jsonl",
		Valid:          true,
	})

	m.Verify()

	ccMapping, _ := m.GetMapping("cc")
	if !ccMapping.Valid {
		t.Error("cc mapping should be valid")
	}
	if ccMapping.LastModified.IsZero() {
		t.Error("cc LastModified should be set")
	}

	cxMapping, _ := m.GetMapping("cx")
	if cxMapping.Valid {
		t.Error("cx mapping should be invalid")
	}
	if cxMapping.Error == "" {
		t.Error("cx Error should be set")
	}
}

func TestLoadSessionMapFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session-map.json")

	content := `{
		"pod_name": "test-pod",
		"updated_at": "2024-01-01T00:00:00Z",
		"mappings": {
			"cc": {
				"role": "cc",
				"session_log_path": "/path/to/log.jsonl",
				"valid": true
			}
		}
	}`

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	m, err := LoadSessionMapFromFile(path)
	if err != nil {
		t.Fatalf("LoadSessionMapFromFile error: %v", err)
	}

	if m.PodName != "test-pod" {
		t.Errorf("PodName = %q, want %q", m.PodName, "test-pod")
	}

	ccMapping, ok := m.GetMapping("cc")
	if !ok {
		t.Fatal("expected cc mapping")
	}
	if ccMapping.SessionLogPath != "/path/to/log.jsonl" {
		t.Errorf("SessionLogPath = %q, want %q", ccMapping.SessionLogPath, "/path/to/log.jsonl")
	}
}

func TestRoleSessionMappingTimes(t *testing.T) {
	m := NewSessionMap("test-pod", "")

	before := time.Now()
	m.SetMapping("cc", &RoleSessionMapping{
		SessionLogPath: "/path/log.jsonl",
		Valid:          true,
	})
	after := time.Now()

	mapping, _ := m.GetMapping("cc")
	if mapping.LastVerified.Before(before) || mapping.LastVerified.After(after) {
		t.Error("LastVerified should be between before and after")
	}

	if m.UpdatedAt.Before(before) || m.UpdatedAt.After(after) {
		t.Error("UpdatedAt should be between before and after")
	}
}
