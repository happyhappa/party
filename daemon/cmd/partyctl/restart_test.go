package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/norm/relay-daemon/internal/contract"
)

func TestBuildLaunchCommand(t *testing.T) {
	t.Setenv("RELAY_TMUX_SESSION", "party-demo")
	c := &contract.Contract{
		Paths: contract.PathSpec{
			ShareDir: "/share",
			StateDir: "/state",
			LogDir:   "/log",
			InboxDir: "/outbox",
			BeadsDir: "/beads",
		},
	}
	role := contract.RoleSpec{
		Name:        "cx",
		WorktreeDir: "/work/cx-wt",
		Env:         map[string]string{"EXTRA": "1"},
	}
	tool := contract.AgentToolSpec{
		Launch: contract.CommandSpec{
			Command: "codex",
			Args:    []string{"exec", "-p", "hello world"},
			Env:     map[string]string{"TOOL_ENV": "yes"},
		},
	}
	got := buildLaunchCommand(c, role, tool)
	for _, want := range []string{
		"export AGENT_ROLE='cx'",
		"export RELAY_STATE_DIR='/state'",
		"export TOOL_ENV='yes'",
		"cd '/work/cx-wt'",
		"exec 'codex' 'exec' '-p' 'hello world'",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("launch command missing %q:\n%s", want, got)
		}
	}
}

func TestScanAckMessages(t *testing.T) {
	dir := t.TempDir()
	start := time.Now().Add(-time.Minute)
	if err := os.WriteFile(filepath.Join(dir, "001.msg"), []byte("TO: all\nKIND: chat\n---\noc back online\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	seen, err := scanAckMessages(dir, "oc", start)
	if err != nil {
		t.Fatalf("scanAckMessages: %v", err)
	}
	if !seen {
		t.Fatal("expected ACK to be detected")
	}
}

func TestLookupPaneIDFromPaneMap(t *testing.T) {
	dir := t.TempDir()
	paneMap := filepath.Join(dir, "panes.json")
	if err := os.WriteFile(paneMap, []byte(`{"panes":{"oc":"%1"},"version":1,"registered_at":"2026-03-19T00:00:00Z"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	c := &contract.Contract{
		Paths: contract.PathSpec{StateDir: dir, PaneMap: paneMap},
		Roles: []contract.RoleSpec{{Name: "oc", Tool: "claude_code"}},
	}
	got, err := lookupPaneID(c, "oc")
	if err != nil {
		t.Fatalf("lookupPaneID: %v", err)
	}
	if got != "%1" {
		t.Fatalf("pane id = %q, want %%1", got)
	}
}
