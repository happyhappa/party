package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/norm/relay-daemon/internal/contract"
	"github.com/spf13/cobra"
)

// setupConfigureTest creates a temp dir with a contract and a JSON config file,
// then returns a cobra command wired to capture output.
func setupConfigureTest(t *testing.T, configContent string, mutations []contract.ConfigMutationSpec) (*cobra.Command, string, string) {
	t.Helper()
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "state")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}

	configPath := filepath.Join(dir, "settings.json")
	if configContent != "" {
		if err := os.WriteFile(configPath, []byte(configContent), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	c := &contract.Contract{
		Version: 1,
		Project: contract.ProjectSpec{Name: "test", RootDir: dir, MainDir: dir},
		Paths:   contract.PathSpec{StateDir: stateDir, ShareDir: dir, LogDir: filepath.Join(dir, "log"), InboxDir: filepath.Join(dir, "outbox")},
		Session: contract.SessionSpec{Name: "test", WindowName: "main"},
		Roles:   []contract.RoleSpec{{Name: "oc", Tool: "claude_code"}},
		Layout:  contract.LayoutSpec{SchemaVersion: 1},
		Tools: map[string]contract.AgentToolSpec{
			"claude_code": {
				Name: "claude_code",
				ConfigFiles: []contract.ConfigFileSpec{{
					Name:      "test_config",
					Format:    "json",
					Path:      configPath,
					Required:  false,
					Mutations: mutations,
				}},
			},
		},
	}

	contractPath := filepath.Join(stateDir, "party-contract.json")
	if err := contract.WriteContract(c, contractPath); err != nil {
		t.Fatal(err)
	}

	root := newRootCmd()
	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stdout)

	return root, contractPath, configPath
}

func TestConfigureApplyDryRun(t *testing.T) {
	mutations := []contract.ConfigMutationSpec{{
		Path:      "statusLine.type",
		Action:    "ensure_exists",
		ValueType: "string",
		StringValue: "command",
		CreateParents: true,
	}}
	root, contractPath, _ := setupConfigureTest(t, `{}`, mutations)

	root.SetArgs([]string{"configure", "apply", "--dry-run", "--contract-path", contractPath, "--format", "json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var stdout bytes.Buffer
	root.SetOut(&stdout)
	// Re-run to capture output
	root2, contractPath2, _ := setupConfigureTest(t, `{}`, mutations)
	var out bytes.Buffer
	root2.SetOut(&out)
	root2.SetArgs([]string{"configure", "apply", "--dry-run", "--contract-path", contractPath2, "--format", "json"})
	if err := root2.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var results []fileResult
	if err := json.Unmarshal(out.Bytes(), &results); err != nil {
		t.Fatalf("unmarshal output: %v (raw: %s)", err, out.String())
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if !results[0].Changed {
		t.Error("expected changed=true for new ensure_exists mutation")
	}
}

func TestConfigureApplySkipUnchanged(t *testing.T) {
	mutations := []contract.ConfigMutationSpec{{
		Path:          "statusLine.type",
		Action:        "replace",
		ValueType:     "string",
		StringValue:   "command",
		CreateParents: true,
	}}
	// Pre-populate the config with the expected value
	root, contractPath, configPath := setupConfigureTest(t, `{"statusLine":{"type":"command"}}`, mutations)

	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"configure", "apply", "--contract-path", contractPath, "--format", "human"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	output := out.String()
	if !strings.Contains(output, "unchanged") || !strings.Contains(output, "No changes needed") {
		t.Errorf("expected unchanged output, got: %s", output)
	}

	// Verify file was not rewritten (mtime should not change, but checking content is more reliable)
	before, _ := os.ReadFile(configPath)
	if string(before) != `{"statusLine":{"type":"command"}}` {
		t.Errorf("file was rewritten when it shouldn't have been: %s", string(before))
	}
}

func TestConfigureApplyTOMLCommentWarning(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "state")
	os.MkdirAll(stateDir, 0o755)

	tomlPath := filepath.Join(dir, "config.toml")
	os.WriteFile(tomlPath, []byte("# this is a comment\n[tui]\nstatus_line = [\"old\"]\n"), 0o644)

	c := &contract.Contract{
		Version: 1,
		Project: contract.ProjectSpec{Name: "test", RootDir: dir, MainDir: dir},
		Paths:   contract.PathSpec{StateDir: stateDir, ShareDir: dir, LogDir: filepath.Join(dir, "log"), InboxDir: filepath.Join(dir, "outbox")},
		Session: contract.SessionSpec{Name: "test", WindowName: "main"},
		Roles:   []contract.RoleSpec{{Name: "oc", Tool: "test_tool"}},
		Layout:  contract.LayoutSpec{SchemaVersion: 1},
		Tools: map[string]contract.AgentToolSpec{
			"test_tool": {
				Name: "test_tool",
				ConfigFiles: []contract.ConfigFileSpec{{
					Name:   "test_toml",
					Format: "toml",
					Path:   tomlPath,
					Mutations: []contract.ConfigMutationSpec{{
						Path:       "tui.status_line",
						Action:     "replace",
						ValueType:  "string_array",
						ArrayValue: []string{"new"},
					}},
				}},
			},
		},
	}

	contractPath := filepath.Join(stateDir, "party-contract.json")
	contract.WriteContract(c, contractPath)

	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"configure", "apply", "--dry-run", "--contract-path", contractPath, "--format", "human"})
	root.Execute()

	if !strings.Contains(out.String(), "comments") {
		t.Errorf("expected TOML comment warning, got: %s", out.String())
	}
}

func TestBackupName(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"/home/user/.claude/settings.json", "home-user-.claude-settings.json.bak"},
		{"/home/user/.codex/settings.json", "home-user-.codex-settings.json.bak"},
		{"/home/user/.codex/config.toml", "home-user-.codex-config.toml.bak"},
	}
	backupDir := "/tmp/backups"
	for _, tt := range tests {
		got := backupName(tt.path, backupDir)
		wantFull := filepath.Join(backupDir, tt.want)
		if got != wantFull {
			t.Errorf("backupName(%q) = %q, want %q", tt.path, got, wantFull)
		}
	}

	// Verify no collision between different dirs with same basename
	a := backupName("/home/user/.claude/settings.json", backupDir)
	b := backupName("/home/user/.codex/settings.json", backupDir)
	if a == b {
		t.Errorf("backup name collision: %q == %q", a, b)
	}
}
