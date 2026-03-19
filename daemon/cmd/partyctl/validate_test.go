package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/norm/relay-daemon/internal/contract"
)

func writeTestContract(t *testing.T, c *contract.Contract) string {
	t.Helper()
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "state")
	os.MkdirAll(stateDir, 0o755)

	if c.Paths.StateDir == "" {
		c.Paths.StateDir = stateDir
	}

	contractPath := filepath.Join(stateDir, "party-contract.json")
	if err := contract.WriteContract(c, contractPath); err != nil {
		t.Fatal(err)
	}
	return contractPath
}

func validTestContract(t *testing.T) *contract.Contract {
	t.Helper()
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "state")
	logDir := filepath.Join(dir, "log")
	inboxDir := filepath.Join(dir, "outbox")
	for _, d := range []string{stateDir, logDir, inboxDir} {
		os.MkdirAll(d, 0o755)
	}

	return &contract.Contract{
		Version: 1,
		Project: contract.ProjectSpec{Name: "test", RootDir: dir, MainDir: dir},
		Paths:   contract.PathSpec{StateDir: stateDir, ShareDir: dir, LogDir: logDir, InboxDir: inboxDir},
		Session: contract.SessionSpec{Name: "test", WindowName: "main"},
		Roles:   []contract.RoleSpec{{Name: "oc", Tool: "test_tool", WorktreeDir: dir}},
		Layout:  contract.LayoutSpec{SchemaVersion: 1, Panes: []contract.PaneLayoutSpec{{Role: "oc"}}},
		Tools: map[string]contract.AgentToolSpec{
			"test_tool": {Name: "test_tool", Launch: contract.CommandSpec{Command: "true"}},
		},
	}
}

func TestValidateHumanValid(t *testing.T) {
	c := validTestContract(t)
	contractPath := writeTestContract(t, c)

	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"validate", "--contract-path", contractPath, "--format", "human"})

	err := root.Execute()
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out.String(), "valid") {
		t.Errorf("expected 'valid' in output, got: %s", out.String())
	}
}

func TestValidateJSONValid(t *testing.T) {
	c := validTestContract(t)
	contractPath := writeTestContract(t, c)

	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"validate", "--contract-path", contractPath, "--format", "json"})

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var result contract.ValidationResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal: %v (raw: %s)", err, out.String())
	}
	if !result.Valid {
		t.Errorf("expected valid=true, got errors: %v", result.Errors)
	}
}

func TestValidateInvalidContractExitCode1(t *testing.T) {
	c := validTestContract(t)
	// Add a role that references a nonexistent tool
	c.Roles = append(c.Roles, contract.RoleSpec{Name: "bad", Tool: "nonexistent", WorktreeDir: c.Project.RootDir})
	contractPath := writeTestContract(t, c)

	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"validate", "--contract-path", contractPath, "--format", "human"})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected error for invalid contract")
	}
	var ee *exitError
	if !errors.As(err, &ee) {
		t.Fatalf("expected exitError, got %T: %v", err, err)
	}
	if ee.code != 1 {
		t.Errorf("expected exit code 1, got %d", ee.code)
	}
}

func TestValidateLoadFailureExitCode2(t *testing.T) {
	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"validate", "--contract-path", "/nonexistent/contract.json", "--format", "human"})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected error for missing contract")
	}
	var ee *exitError
	if !errors.As(err, &ee) {
		t.Fatalf("expected exitError, got %T: %v", err, err)
	}
	if ee.code != 2 {
		t.Errorf("expected exit code 2, got %d", ee.code)
	}
}

func TestValidateLoadFailureJSONOutput(t *testing.T) {
	root := newRootCmd()
	root.SilenceUsage = true
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&bytes.Buffer{}) // discard stderr
	root.SetArgs([]string{"validate", "--contract-path", "/nonexistent/contract.json", "--format", "json"})

	root.Execute() // ignore error, we want the output

	var result map[string]interface{}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal: %v (raw: %s)", err, out.String())
	}
	if result["valid"] != false {
		t.Errorf("expected valid=false, got %v", result["valid"])
	}
	errs, ok := result["errors"].([]interface{})
	if !ok || len(errs) == 0 {
		t.Fatalf("expected errors array, got %v", result["errors"])
	}
}
