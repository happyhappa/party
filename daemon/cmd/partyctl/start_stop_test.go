package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/norm/relay-daemon/internal/contract"
	"github.com/norm/relay-daemon/internal/recycle"
)

func TestRunStartWithHydrateAndPaneOverride(t *testing.T) {
	restore := stubLifecycleFuncs()
	defer restore()

	var sent []string
	tmuxSendLiteralFunc = func(paneID, text string) error {
		sent = append(sent, paneID+"|literal|"+text)
		return nil
	}
	tmuxSendKeyFunc = func(paneID, key string) error {
		sent = append(sent, paneID+"|key|"+key)
		return nil
	}
	panePIDFunc = func(paneID string) int {
		if paneID != "%9" {
			t.Fatalf("panePID called with %q", paneID)
		}
		return 4242
	}
	assembleHydration = func(opts recycle.HydrationOptions) (*recycle.HydrationPayload, error) {
		return &recycle.HydrationPayload{Role: opts.Role, LogTail: "recent"}, nil
	}
	sendRelayDirectFunc = func(role, body string) error {
		sent = append(sent, role+"|relay|"+body)
		return nil
	}

	dir := t.TempDir()
	contractPath := writePartyctlContract(t, dir, contract.RoleSpec{Name: "cx", Tool: "codex"})

	cmd := newRootCmd()
	cmd.SetArgs([]string{"start", "cx", "--contract-path", contractPath, "--set-pane", "cx=%9", "--hydrate"})
	out := new(strings.Builder)
	cmd.SetOut(out)
	cmd.SetErr(out)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	state, err := recycle.LoadState(dir, "cx")
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if state.State != recycle.StateReady || state.AgentPID != 4242 {
		t.Fatalf("unexpected state: %+v", state)
	}
	if !strings.Contains(out.String(), "Started cx (pane %9, pid 4242)") {
		t.Fatalf("unexpected output: %q", out.String())
	}
	if len(sent) < 3 {
		t.Fatalf("expected tmux launch and relay hydration, got %v", sent)
	}
	if !strings.Contains(sent[0], "%9|literal|") || !strings.Contains(sent[0], "exec 'codex'") {
		t.Fatalf("missing launch command in %v", sent)
	}
	if sent[1] != "%9|key|Enter" {
		t.Fatalf("missing Enter key send: %v", sent)
	}
	if !strings.Contains(sent[2], "cx|relay|## Recovery Context (Recycle)") {
		t.Fatalf("missing hydration relay send: %v", sent)
	}
}

func TestRunStopGraceful(t *testing.T) {
	restore := stubLifecycleFuncs()
	defer restore()

	var calls []string
	tmuxSendLiteralFunc = func(paneID, text string) error {
		calls = append(calls, paneID+"|literal|"+text)
		return nil
	}
	tmuxSendKeyFunc = func(paneID, key string) error {
		calls = append(calls, paneID+"|key|"+key)
		return nil
	}
	gracefulKillFunc = func(pid int, grace time.Duration) error {
		calls = append(calls, "graceful")
		if pid != 777 {
			t.Fatalf("gracefulKill pid=%d", pid)
		}
		if grace != 12*time.Second {
			t.Fatalf("grace=%s", grace)
		}
		return nil
	}

	dir := t.TempDir()
	contractPath := writePartyctlContract(t, dir, contract.RoleSpec{Name: "cc", Tool: "claude_code", PaneID: "%2"})
	state := &recycle.RecycleState{
		State:     recycle.StateReady,
		EnteredAt: time.Now().UTC(),
		AgentPID:  777,
	}
	if err := state.Save(dir, "cc"); err != nil {
		t.Fatal(err)
	}

	cmd := newRootCmd()
	cmd.SetArgs([]string{"stop", "cc", "--contract-path", contractPath})
	out := new(strings.Builder)
	cmd.SetOut(out)
	cmd.SetErr(out)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	gotState, err := recycle.LoadState(dir, "cc")
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if gotState.State != recycle.StateReady || gotState.AgentPID != 0 {
		t.Fatalf("unexpected state after stop: %+v", gotState)
	}
	if want := []string{"%2|literal|/exit", "%2|key|Enter", "graceful"}; strings.Join(calls, ",") != strings.Join(want, ",") {
		t.Fatalf("calls=%v want=%v", calls, want)
	}
	if !strings.Contains(out.String(), "Stopped cc") {
		t.Fatalf("unexpected output: %q", out.String())
	}
}

func TestRunStopForce(t *testing.T) {
	restore := stubLifecycleFuncs()
	defer restore()

	var forced int
	forceKillPIDFunc = func(pid int) error {
		forced = pid
		return nil
	}
	tmuxSendLiteralFunc = func(paneID, text string) error {
		t.Fatalf("tmuxSendLiteral should not be called in force mode")
		return nil
	}
	gracefulKillFunc = func(pid int, grace time.Duration) error {
		t.Fatalf("gracefulKill should not be called in force mode")
		return nil
	}

	dir := t.TempDir()
	contractPath := writePartyctlContract(t, dir, contract.RoleSpec{Name: "oc", Tool: "claude_code", PaneID: "%1"})
	state := &recycle.RecycleState{
		State:     recycle.StateReady,
		EnteredAt: time.Now().UTC(),
		AgentPID:  9191,
	}
	if err := state.Save(dir, "oc"); err != nil {
		t.Fatal(err)
	}

	cmd := newRootCmd()
	cmd.SetArgs([]string{"stop", "oc", "--contract-path", contractPath, "--force"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if forced != 9191 {
		t.Fatalf("forceKillPID pid=%d want 9191", forced)
	}
}

func stubLifecycleFuncs() func() {
	oldPanePID := panePIDFunc
	oldSendLiteral := tmuxSendLiteralFunc
	oldSendKey := tmuxSendKeyFunc
	oldSendRelayMessage := sendRelayMessageFunc
	oldSendRelayDirect := sendRelayDirectFunc
	oldGracefulKill := gracefulKillFunc
	oldForceKill := forceKillPIDFunc
	oldAssembleHydration := assembleHydration

	return func() {
		panePIDFunc = oldPanePID
		tmuxSendLiteralFunc = oldSendLiteral
		tmuxSendKeyFunc = oldSendKey
		sendRelayMessageFunc = oldSendRelayMessage
		sendRelayDirectFunc = oldSendRelayDirect
		gracefulKillFunc = oldGracefulKill
		forceKillPIDFunc = oldForceKill
		assembleHydration = oldAssembleHydration
	}
}

func writePartyctlContract(t *testing.T, stateDir string, role contract.RoleSpec) string {
	t.Helper()

	c := contract.Contract{
		Version: contract.CurrentVersion,
		Project: contract.ProjectSpec{
			Name:    "demo",
			RootDir: stateDir,
		},
		Paths: contract.PathSpec{
			StateDir: stateDir,
			ShareDir: filepath.Join(stateDir, "share"),
			LogDir:   filepath.Join(stateDir, "log"),
			InboxDir: filepath.Join(stateDir, "inbox"),
			BeadsDir: filepath.Join(stateDir, "beads"),
		},
		Session: contract.SessionSpec{
			Name: "party",
		},
		Roles: []contract.RoleSpec{role},
		Tools: map[string]contract.AgentToolSpec{
			"claude_code": {
				Name: "claude_code",
				Launch: contract.CommandSpec{
					Command: "claude",
				},
				Recycle: contract.RecycleSpec{
					ExitCommand: "/exit",
					GracePeriod: contract.Duration{Duration: 12 * time.Second},
				},
			},
			"codex": {
				Name: "codex",
				Launch: contract.CommandSpec{
					Command: "codex",
				},
				Recycle: contract.RecycleSpec{
					ExitCommand: "ctrl-c",
					GracePeriod: contract.Duration{Duration: 9 * time.Second},
				},
			},
		},
	}

	data, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	path := filepath.Join(stateDir, "contract.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}
