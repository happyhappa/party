package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/norm/relay-daemon/internal/contract"
)

// isolateRegistry points XDG_CONFIG_HOME to a temp dir so registry
// operations don't collide with real sessions or other tests.
func isolateRegistry(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
}

// writeDiscoveryContract creates a valid contract file and returns its path.
func writeDiscoveryContract(t *testing.T, dir string) string {
	t.Helper()
	os.MkdirAll(filepath.Join(dir, "daemon"), 0o755)
	os.MkdirAll(filepath.Join(dir, "bin"), 0o755)
	os.MkdirAll(filepath.Join(dir, "main"), 0o755)
	stateDir := filepath.Join(dir, ".relay", "state")
	os.MkdirAll(stateDir, 0o755)
	os.MkdirAll(filepath.Join(dir, ".relay", "log"), 0o755)
	os.MkdirAll(filepath.Join(dir, ".relay", "outbox"), 0o755)

	c := contract.DefaultContract(dir, filepath.Join(dir, "main"))
	c.Paths.StateDir = stateDir
	c.Paths.ShareDir = filepath.Join(dir, ".relay")
	c.Paths.LogDir = filepath.Join(dir, ".relay", "log")
	c.Paths.InboxDir = filepath.Join(dir, ".relay", "outbox")
	c.Paths.ContractPath = filepath.Join(stateDir, "party-contract.json")
	c.Paths.PaneMap = filepath.Join(stateDir, "panes.json")
	// Clear unexpanded template vars from role worktree dirs
	for i := range c.Roles {
		c.Roles[i].WorktreeDir = dir
	}

	contractPath := filepath.Join(stateDir, "party-contract.json")
	if err := contract.WriteContract(c, contractPath); err != nil {
		t.Fatalf("WriteContract: %v", err)
	}
	return contractPath
}

func TestDiscoveryExplicitPathWins(t *testing.T) {
	isolateRegistry(t)
	dir := t.TempDir()
	contractPath := writeDiscoveryContract(t, dir)

	// Register a different session to ensure explicit path takes priority
	contract.RegisterSession(contract.RegistryEntry{
		ProjectName:  "decoy",
		ContractPath: "/nonexistent/contract.json",
		ProjectRoot:  "/nonexistent",
		TmuxSession:  "party-decoy",
	})

	root := newRootCmd()
	root.SilenceUsage = true
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"validate", "--contract-path", contractPath, "--format", "json"})

	// Clear env to ensure explicit path is the only discovery path
	t.Setenv("RELAY_STATE_DIR", "")

	_ = root.Execute() // may exit 1 for missing binaries — that's ok

	// Should have loaded the contract successfully (got JSON output with "valid" key)
	var result map[string]any
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("expected valid JSON output, got: %s", out.String())
	}
	// Presence of "valid" key means contract was loaded and validated
	if _, ok := result["valid"]; !ok {
		t.Errorf("expected 'valid' key in output, got: %v", result)
	}
}

func TestDiscoveryRegistryFindsContract(t *testing.T) {
	isolateRegistry(t)
	dir := t.TempDir()
	contractPath := writeDiscoveryContract(t, dir)
	projectName := filepath.Base(dir)

	entry := contract.RegistryEntry{
		ProjectName:  projectName,
		ContractPath: contractPath,
		ProjectRoot:  dir,
		TmuxSession:  "party-" + projectName,
	}
	if err := contract.RegisterSession(entry); err != nil {
		t.Fatalf("RegisterSession: %v", err)
	}

	// Clear env — force registry discovery
	t.Setenv("RELAY_STATE_DIR", "")

	root := newRootCmd()
	root.SilenceUsage = true
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"validate", "--project", projectName, "--format", "json"})

	_ = root.Execute()

	var result map[string]any
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("expected valid JSON output, got: %s", out.String())
	}
	if _, ok := result["valid"]; !ok {
		t.Errorf("expected 'valid' key in output, got: %v", result)
	}
}

func TestDiscoveryProjectFlagSelectsCorrectSession(t *testing.T) {
	isolateRegistry(t)
	dir1 := t.TempDir()
	contractPath1 := writeDiscoveryContract(t, dir1)
	name1 := filepath.Base(dir1)

	dir2 := t.TempDir()
	writeDiscoveryContract(t, dir2)
	name2 := filepath.Base(dir2)

	contract.RegisterSession(contract.RegistryEntry{
		ProjectName: name1, ContractPath: contractPath1,
		ProjectRoot: dir1, TmuxSession: "party-" + name1,
	})
	contract.RegisterSession(contract.RegistryEntry{
		ProjectName: name2, ContractPath: filepath.Join(dir2, ".relay", "state", "party-contract.json"),
		ProjectRoot: dir2, TmuxSession: "party-" + name2,
	})

	t.Setenv("RELAY_STATE_DIR", "")

	root := newRootCmd()
	root.SilenceUsage = true
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"validate", "--project", name1, "--format", "json"})

	_ = root.Execute()

	var result map[string]any
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("expected valid JSON output, got: %s", out.String())
	}
}

func TestDiscoveryMultipleSessionsRequiresProject(t *testing.T) {
	isolateRegistry(t)
	dir1 := t.TempDir()
	contractPath1 := writeDiscoveryContract(t, dir1)
	name1 := filepath.Base(dir1)

	dir2 := t.TempDir()
	contractPath2 := writeDiscoveryContract(t, dir2)
	name2 := filepath.Base(dir2)

	contract.RegisterSession(contract.RegistryEntry{
		ProjectName: name1, ContractPath: contractPath1,
		ProjectRoot: dir1, TmuxSession: "party-" + name1,
	})
	contract.RegisterSession(contract.RegistryEntry{
		ProjectName: name2, ContractPath: contractPath2,
		ProjectRoot: dir2, TmuxSession: "party-" + name2,
	})

	t.Setenv("RELAY_STATE_DIR", "")

	// cd to a dir not under either project
	origDir, _ := os.Getwd()
	os.Chdir(os.TempDir())
	defer os.Chdir(origDir)

	root := newRootCmd()
	root.SilenceUsage = true
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"validate", "--format", "json"})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected error with multiple sessions and no --project")
	}

	// The "multiple" message appears in the JSON error output from validate
	if !strings.Contains(out.String(), "multiple") {
		t.Errorf("expected 'multiple' in output, got: %s", out.String())
	}
}

func TestContractInitAutoRegisters(t *testing.T) {
	isolateRegistry(t)
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "daemon"), 0o755)
	os.MkdirAll(filepath.Join(dir, "bin"), 0o755)
	stateDir := filepath.Join(dir, ".relay", "state")
	os.MkdirAll(stateDir, 0o755)

	t.Setenv("RELAY_STATE_DIR", stateDir)
	t.Setenv("RELAY_SHARE_DIR", filepath.Join(dir, ".relay"))
	t.Setenv("RELAY_MAIN_DIR", dir)

	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	root := newRootCmd()
	root.SilenceUsage = true
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"contract", "init"})

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	sessions, err := contract.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}

	// Load the contract to get the actual project name (resolveProjectRoot may walk past dir)
	contractPath := filepath.Join(stateDir, "party-contract.json")
	c, err := contract.LoadContract(contractPath)
	if err != nil {
		t.Fatalf("LoadContract: %v", err)
	}
	projectName := c.Project.Name

	found := false
	for _, s := range sessions {
		if s.ProjectName == projectName {
			found = true
			if s.ContractPath != contractPath {
				t.Errorf("registered contract path = %s, want %s", s.ContractPath, contractPath)
			}
			break
		}
	}
	if !found {
		t.Errorf("contract init did not auto-register session (found %d sessions, want project=%s)", len(sessions), projectName)
	}

	// Cleanup
	contract.DeregisterSession(projectName)
}

func TestContractSessionsCmd(t *testing.T) {
	isolateRegistry(t)
	name := "test-sessions-cmd"
	entry := contract.RegistryEntry{
		ProjectName:  name,
		ContractPath: "/tmp/test/contract.json",
		ProjectRoot:  "/tmp/test",
		TmuxSession:  "party-test",
	}
	if err := contract.RegisterSession(entry); err != nil {
		t.Fatalf("RegisterSession: %v", err)
	}

	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"contract", "sessions"})

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if !strings.Contains(out.String(), name) {
		t.Errorf("expected output to contain %q, got: %s", name, out.String())
	}
	if !strings.Contains(out.String(), "party-test") {
		t.Errorf("expected output to contain tmux session, got: %s", out.String())
	}
}

func TestContractDeregisterCmd(t *testing.T) {
	isolateRegistry(t)
	name := "test-deregister-cmd"
	entry := contract.RegistryEntry{
		ProjectName:  name,
		ContractPath: "/tmp/test/contract.json",
		ProjectRoot:  "/tmp/test",
		TmuxSession:  "party-test",
	}
	if err := contract.RegisterSession(entry); err != nil {
		t.Fatalf("RegisterSession: %v", err)
	}

	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"contract", "deregister", name})

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if !strings.Contains(out.String(), "Deregistered") {
		t.Errorf("expected 'Deregistered' in output, got: %s", out.String())
	}

	sessions, _ := contract.ListSessions()
	for _, s := range sessions {
		if s.ProjectName == name {
			t.Error("session still registered after deregister")
		}
	}
}
