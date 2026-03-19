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

func healthTestContract(t *testing.T) (*contract.Contract, string) {
	t.Helper()
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "state")
	os.MkdirAll(stateDir, 0o755)
	os.MkdirAll(filepath.Join(dir, "log"), 0o755)
	os.MkdirAll(filepath.Join(dir, "outbox"), 0o755)

	c := &contract.Contract{
		Version: 1,
		Project: contract.ProjectSpec{Name: "test", RootDir: dir, MainDir: dir},
		Paths:   contract.PathSpec{StateDir: stateDir, ShareDir: dir, LogDir: filepath.Join(dir, "log"), InboxDir: filepath.Join(dir, "outbox")},
		Session: contract.SessionSpec{Name: "test", WindowName: "main"},
		Roles: []contract.RoleSpec{
			{Name: "oc", Tool: "claude_code", WorktreeDir: dir},
			{Name: "cc", Tool: "claude_code", WorktreeDir: dir},
		},
		Layout: contract.LayoutSpec{SchemaVersion: 1},
		Tools: map[string]contract.AgentToolSpec{
			"claude_code": {
				Name: "claude_code",
				Telemetry: contract.TelemetrySpec{
					HasSidecar:  true,
					SidecarPath: filepath.Join(stateDir, "telemetry-${role}.json"),
					ContextKey:  "context_pct",
				},
				Recycle: contract.RecycleSpec{
					ThresholdUsedPct: 65,
					ExitCommand:      "/exit",
				},
			},
		},
	}

	contractPath := filepath.Join(stateDir, "party-contract.json")
	if err := contract.WriteContract(c, contractPath); err != nil {
		t.Fatal(err)
	}
	return c, contractPath
}

func writeSidecar(t *testing.T, stateDir, role string, pct int) {
	t.Helper()
	data := []byte(`{"role":"` + role + `","context_pct":` + json.Number(strings.Repeat("0", 0)).String() + `}`)
	// Use a proper JSON marshal
	sidecar := map[string]interface{}{
		"role":        role,
		"context_pct": pct,
		"updated_at":  "2026-03-19T03:00:00Z",
	}
	var err error
	data, err = json.Marshal(sidecar)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(stateDir, "telemetry-"+role+".json")
	os.WriteFile(path, data, 0o644)
}

func TestHealthJSONOutput(t *testing.T) {
	_, contractPath := healthTestContract(t)
	stateDir := filepath.Dir(contractPath)

	// Write sidecar telemetry for oc at 50% (below threshold)
	writeSidecar(t, stateDir, "oc", 50)
	writeSidecar(t, stateDir, "cc", 50)

	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"health", "--contract-path", contractPath, "--format", "json"})

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var results []roleHealth
	if err := json.Unmarshal(out.Bytes(), &results); err != nil {
		t.Fatalf("unmarshal: %v (raw: %s)", err, out.String())
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	for _, r := range results {
		if r.ContextUsedPct != 50 {
			t.Errorf("%s: context = %d, want 50", r.Role, r.ContextUsedPct)
		}
		if r.Exceeded {
			t.Errorf("%s: should not exceed at 50%%", r.Role)
		}
		if r.RecycleTriggered {
			t.Errorf("%s: should not trigger recycle at 50%%", r.Role)
		}
	}
}

func TestHealthThresholdExceeded(t *testing.T) {
	_, contractPath := healthTestContract(t)
	stateDir := filepath.Dir(contractPath)

	// Write cc at 70% (above 65% threshold)
	writeSidecar(t, stateDir, "cc", 70)
	writeSidecar(t, stateDir, "oc", 40)

	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"health", "--contract-path", contractPath, "--format", "json"})

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var results []roleHealth
	if err := json.Unmarshal(out.Bytes(), &results); err != nil {
		t.Fatalf("unmarshal: %v (raw: %s)", err, out.String())
	}

	for _, r := range results {
		if r.Role == "cc" {
			if !r.Exceeded {
				t.Error("cc should exceed threshold at 70%")
			}
			if !r.RecycleTriggered {
				t.Error("cc should trigger recycle (state was ready)")
			}
			if r.RecycleState != "exiting" {
				t.Errorf("cc recycle_state = %q, want exiting", r.RecycleState)
			}
		}
		if r.Role == "oc" {
			if r.Exceeded {
				t.Error("oc should not exceed threshold at 40%")
			}
		}
	}
}

func TestHealthRoleFilter(t *testing.T) {
	_, contractPath := healthTestContract(t)
	stateDir := filepath.Dir(contractPath)

	writeSidecar(t, stateDir, "oc", 30)
	writeSidecar(t, stateDir, "cc", 30)

	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"health", "--contract-path", contractPath, "--format", "json", "--role", "oc"})

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var results []roleHealth
	json.Unmarshal(out.Bytes(), &results)
	if len(results) != 1 {
		t.Fatalf("expected 1 result with --role oc, got %d", len(results))
	}
	if results[0].Role != "oc" {
		t.Errorf("role = %q, want oc", results[0].Role)
	}
}

func TestHealthHumanOutput(t *testing.T) {
	_, contractPath := healthTestContract(t)
	stateDir := filepath.Dir(contractPath)

	writeSidecar(t, stateDir, "oc", 50)
	writeSidecar(t, stateDir, "cc", 50)

	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"health", "--contract-path", contractPath, "--format", "human"})

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	output := out.String()
	if !strings.Contains(output, "oc") || !strings.Contains(output, "cc") {
		t.Errorf("expected both roles in output, got: %s", output)
	}
	if !strings.Contains(output, "50%") {
		t.Errorf("expected 50%% in output, got: %s", output)
	}
}

func TestHealthMissingSidecar(t *testing.T) {
	_, contractPath := healthTestContract(t)
	// Don't write any sidecar files

	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"health", "--contract-path", contractPath, "--format", "json"})

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var results []roleHealth
	json.Unmarshal(out.Bytes(), &results)
	for _, r := range results {
		if r.Error == "" {
			t.Errorf("%s: expected error for missing sidecar", r.Role)
		}
	}
}
