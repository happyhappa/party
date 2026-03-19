package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/norm/relay-daemon/internal/contract"
)

func TestCollectStatusRows(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 3, 19, 12, 0, 0, 0, time.UTC)

	if err := os.WriteFile(filepath.Join(dir, "telemetry-oc.json"), []byte(`{
  "role": "oc",
  "timestamp": 1760000000,
  "context_pct": 42,
  "model_id": "claude-opus",
  "model_display": "Opus",
  "session_id": "sess-oc",
  "cost_usd": 1.25
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "recycle-oc.json"), []byte(`{
  "state": "ready",
  "entered_at": "2026-03-19T11:58:00Z",
  "agent_pid": 1234,
  "session_id": "sess-oc",
  "last_brief_at": "2026-03-19T11:55:00Z"
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "recycle-cx.json"), []byte(`{
  "state": "hydrating",
  "entered_at": "2026-03-19T11:59:30Z",
  "agent_pid": 2222,
  "session_id": "sess-cx"
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	c := &contract.Contract{
		Paths: contract.PathSpec{StateDir: dir},
		Roles: []contract.RoleSpec{
			{Name: "oc", Tool: "claude_code"},
			{Name: "cx", Tool: "codex"},
		},
		Tools: map[string]contract.AgentToolSpec{
			"claude_code": {Name: "claude_code", Telemetry: contract.TelemetrySpec{HasSidecar: true}},
			"codex":       {Name: "codex", Telemetry: contract.TelemetrySpec{HasSidecar: false}},
		},
	}

	rows, err := collectStatusRows(c, now)
	if err != nil {
		t.Fatalf("collectStatusRows: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("len(rows) = %d, want 2", len(rows))
	}

	oc := rows[0]
	if oc.Role != "oc" {
		t.Fatalf("rows[0].Role = %q, want oc", oc.Role)
	}
	if oc.ContextPct == nil || *oc.ContextPct != 42 {
		t.Fatalf("oc context = %v, want 42", oc.ContextPct)
	}
	if oc.RecycleState != "ready" {
		t.Fatalf("oc recycle state = %q, want ready", oc.RecycleState)
	}
	if oc.AgentPID != 1234 {
		t.Fatalf("oc pid = %d, want 1234", oc.AgentPID)
	}
	if oc.UptimeSeconds != 120 {
		t.Fatalf("oc uptime = %d, want 120", oc.UptimeSeconds)
	}
	if oc.LastBriefAt != "2026-03-19T11:55:00Z" {
		t.Fatalf("oc last_brief_at = %q", oc.LastBriefAt)
	}

	cx := rows[1]
	if cx.ContextPct != nil {
		t.Fatalf("cx context = %v, want nil", cx.ContextPct)
	}
	if cx.RecycleState != "hydrating" {
		t.Fatalf("cx recycle state = %q, want hydrating", cx.RecycleState)
	}
	if cx.UptimeSeconds != 30 {
		t.Fatalf("cx uptime = %d, want 30", cx.UptimeSeconds)
	}
}

func TestCollectStatusRowsInvalidRecycleFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "recycle-oc.json"), []byte("{invalid"), 0o644); err != nil {
		t.Fatal(err)
	}

	c := &contract.Contract{
		Paths: contract.PathSpec{StateDir: dir},
		Roles: []contract.RoleSpec{{Name: "oc", Tool: "claude_code"}},
		Tools: map[string]contract.AgentToolSpec{
			"claude_code": {Name: "claude_code", Telemetry: contract.TelemetrySpec{HasSidecar: true}},
		},
	}

	rows, err := collectStatusRows(c, time.Now())
	if err != nil {
		t.Fatalf("collectStatusRows: %v", err)
	}
	if rows[0].Error == "" {
		t.Fatal("expected error for invalid recycle file")
	}
}

func TestRunStatusJSON(t *testing.T) {
	dir := t.TempDir()
	contractPath := filepath.Join(dir, "contract.json")
	c := &contract.Contract{
		Version: 1,
		Project: contract.ProjectSpec{Name: "demo", RootDir: dir},
		Paths:   contract.PathSpec{StateDir: dir},
		Session: contract.SessionSpec{Name: "party-demo"},
		Roles:   []contract.RoleSpec{{Name: "oc", Tool: "claude_code"}},
		Tools: map[string]contract.AgentToolSpec{
			"claude_code": {Name: "claude_code", Telemetry: contract.TelemetrySpec{HasSidecar: true}},
		},
	}
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(contractPath, data, 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := newRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"status", "--contract-path", contractPath, "--format", "json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var rows []statusRow
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("json output: %v\n%s", err, out.String())
	}
	if len(rows) != 1 || rows[0].Role != "oc" {
		t.Fatalf("unexpected rows: %+v", rows)
	}
}

func TestRenderStatusHuman(t *testing.T) {
	var out bytes.Buffer
	ctx := 33
	renderStatusHuman(&out, []statusRow{{
		Role:          "oc",
		Tool:          "claude_code",
		ContextPct:    &ctx,
		RecycleState:  "ready",
		LastBriefAt:   "2026-03-19T11:55:00Z",
		AgentPID:      999,
		UptimeSeconds: 75,
	}})

	text := out.String()
	for _, want := range []string{"ROLE", "oc", "claude_code", "33%", "ready", "999", "1m15s"} {
		if !strings.Contains(text, want) {
			t.Fatalf("output missing %q:\n%s", want, text)
		}
	}
}
