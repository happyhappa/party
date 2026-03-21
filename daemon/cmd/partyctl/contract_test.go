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

func TestContractInitWritesFile(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir()) // isolate registry
	dir := t.TempDir()
	// Create daemon/ and bin/ so resolveProjectRoot finds it
	os.MkdirAll(filepath.Join(dir, "daemon"), 0o755)
	os.MkdirAll(filepath.Join(dir, "bin"), 0o755)
	stateDir := filepath.Join(dir, ".relay", "state")
	os.MkdirAll(stateDir, 0o755)

	t.Setenv("RELAY_STATE_DIR", stateDir)
	t.Setenv("RELAY_SHARE_DIR", filepath.Join(dir, ".relay"))
	t.Setenv("RELAY_MAIN_DIR", dir)

	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"contract", "init"})

	// Run from the project dir so resolveProjectRoot succeeds
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	contractPath := filepath.Join(stateDir, "party-contract.json")
	if _, err := os.Stat(contractPath); err != nil {
		t.Fatalf("contract file not created: %v", err)
	}

	// Verify it's loadable
	c, err := contract.LoadContract(contractPath)
	if err != nil {
		t.Fatalf("LoadContract: %v", err)
	}
	if c.Version != 1 {
		t.Errorf("version = %d, want 1", c.Version)
	}

	// Verify output contains the path
	if !strings.Contains(out.String(), contractPath) {
		t.Errorf("expected output to contain contract path, got: %s", out.String())
	}
}

func TestContractInitRequiresStateDir(t *testing.T) {
	t.Setenv("RELAY_STATE_DIR", "")

	root := newRootCmd()
	root.SetArgs([]string{"contract", "init"})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected error when RELAY_STATE_DIR not set")
	}
}

func TestContractShowOutputsJSON(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "daemon"), 0o755)
	os.MkdirAll(filepath.Join(dir, "bin"), 0o755)
	stateDir := filepath.Join(dir, ".relay", "state")
	os.MkdirAll(stateDir, 0o755)

	t.Setenv("RELAY_STATE_DIR", stateDir)
	t.Setenv("RELAY_SHARE_DIR", filepath.Join(dir, ".relay"))
	t.Setenv("RELAY_MAIN_DIR", dir)

	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"contract", "show"})

	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var c contract.Contract
	if err := json.Unmarshal(out.Bytes(), &c); err != nil {
		t.Fatalf("unmarshal: %v (raw: %s)", err, out.String())
	}
	if c.Version != 1 {
		t.Errorf("version = %d, want 1", c.Version)
	}
	if c.Project.Name == "" {
		t.Error("project name should not be empty")
	}
}
