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
	oldSetOption := tmuxSetOptionFunc
	oldRespawnPane := tmuxRespawnPaneFunc
	oldIsPaneDead := isPaneDeadFunc
	oldSendRelayMessage := sendRelayMessageFunc
	oldSendRelayDirect := sendRelayDirectFunc
	oldGracefulKill := gracefulKillFunc
	oldForceKill := forceKillPIDFunc
	oldAssembleHydration := assembleHydration

	// Default stubs for new funcs
	tmuxSetOptionFunc = func(paneID, option, value string) error { return nil }
	tmuxRespawnPaneFunc = func(paneID string) error { return nil }
	isPaneDeadFunc = func(paneID string) bool { return false }

	return func() {
		panePIDFunc = oldPanePID
		tmuxSendLiteralFunc = oldSendLiteral
		tmuxSendKeyFunc = oldSendKey
		tmuxSetOptionFunc = oldSetOption
		tmuxRespawnPaneFunc = oldRespawnPane
		isPaneDeadFunc = oldIsPaneDead
		sendRelayMessageFunc = oldSendRelayMessage
		sendRelayDirectFunc = oldSendRelayDirect
		gracefulKillFunc = oldGracefulKill
		forceKillPIDFunc = oldForceKill
		assembleHydration = oldAssembleHydration
	}
}

func TestStopSetsRemainOnExit(t *testing.T) {
	restore := stubLifecycleFuncs()
	defer restore()

	var setOptions []string
	tmuxSetOptionFunc = func(paneID, option, value string) error {
		setOptions = append(setOptions, paneID+"|"+option+"="+value)
		return nil
	}
	tmuxSendLiteralFunc = func(paneID, text string) error { return nil }
	tmuxSendKeyFunc = func(paneID, key string) error { return nil }
	gracefulKillFunc = func(pid int, grace time.Duration) error { return nil }

	dir := t.TempDir()
	contractPath := writePartyctlContract(t, dir, contract.RoleSpec{Name: "cc", Tool: "claude_code", PaneID: "%2"})
	state := &recycle.RecycleState{
		State:     recycle.StateReady,
		EnteredAt: time.Now().UTC(),
		AgentPID:  555,
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

	if len(setOptions) == 0 {
		t.Fatal("expected tmuxSetOption to be called for remain-on-exit")
	}
	if setOptions[0] != "%2|remain-on-exit=on" {
		t.Fatalf("setOptions = %v, want %%2|remain-on-exit=on", setOptions)
	}
}

func TestStartRespawnsDeadPane(t *testing.T) {
	restore := stubLifecycleFuncs()
	defer restore()

	var respawned []string
	var sent []string
	isPaneDeadFunc = func(paneID string) bool {
		return true // simulate dead pane
	}
	tmuxRespawnPaneFunc = func(paneID string) error {
		respawned = append(respawned, paneID)
		return nil
	}
	tmuxSendLiteralFunc = func(paneID, text string) error {
		sent = append(sent, paneID+"|literal|"+text)
		return nil
	}
	tmuxSendKeyFunc = func(paneID, key string) error {
		sent = append(sent, paneID+"|key|"+key)
		return nil
	}
	panePIDFunc = func(paneID string) int { return 8888 }

	dir := t.TempDir()
	contractPath := writePartyctlContract(t, dir, contract.RoleSpec{Name: "cx", Tool: "codex", PaneID: "%5"})

	cmd := newRootCmd()
	cmd.SetArgs([]string{"start", "cx", "--contract-path", contractPath})
	out := new(strings.Builder)
	cmd.SetOut(out)
	cmd.SetErr(out)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if len(respawned) != 1 || respawned[0] != "%5" {
		t.Fatalf("expected respawn of %%5, got %v", respawned)
	}
	if len(sent) < 2 {
		t.Fatalf("expected launch commands after respawn, got %v", sent)
	}
	if !strings.Contains(out.String(), "Started cx (pane %5, pid 8888)") {
		t.Fatalf("unexpected output: %q", out.String())
	}
}

func TestStartAlivePane_NoRespawn(t *testing.T) {
	restore := stubLifecycleFuncs()
	defer restore()

	var respawned []string
	isPaneDeadFunc = func(paneID string) bool {
		return false // pane is alive
	}
	tmuxRespawnPaneFunc = func(paneID string) error {
		respawned = append(respawned, paneID)
		return nil
	}
	tmuxSendLiteralFunc = func(paneID, text string) error { return nil }
	tmuxSendKeyFunc = func(paneID, key string) error { return nil }
	panePIDFunc = func(paneID string) int { return 7777 }

	dir := t.TempDir()
	contractPath := writePartyctlContract(t, dir, contract.RoleSpec{Name: "cc", Tool: "claude_code", PaneID: "%3"})

	cmd := newRootCmd()
	cmd.SetArgs([]string{"start", "cc", "--contract-path", contractPath})
	out := new(strings.Builder)
	cmd.SetOut(out)
	cmd.SetErr(out)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if len(respawned) != 0 {
		t.Fatalf("should not respawn alive pane, got %v", respawned)
	}
}

func TestRestartSetsRemainOnExitAndRespawns(t *testing.T) {
	restore := stubLifecycleFuncs()
	defer restore()

	var setOptions []string
	var respawned []string
	tmuxSetOptionFunc = func(paneID, option, value string) error {
		setOptions = append(setOptions, paneID+"|"+option+"="+value)
		return nil
	}
	// After kill, the pane becomes dead
	killDone := false
	isPaneDeadFunc = func(paneID string) bool {
		return killDone
	}
	tmuxRespawnPaneFunc = func(paneID string) error {
		respawned = append(respawned, paneID)
		return nil
	}
	tmuxSendLiteralFunc = func(paneID, text string) error { return nil }
	tmuxSendKeyFunc = func(paneID, key string) error { return nil }
	sendRelayMessageFunc = func(role, body string) error { return nil }
	gracefulKillFunc = func(pid int, grace time.Duration) error {
		killDone = true
		return nil
	}
	panePIDFunc = func(paneID string) int { return 9999 }
	assembleHydration = func(opts recycle.HydrationOptions) (*recycle.HydrationPayload, error) {
		return nil, nil
	}
	sendRelayDirectFunc = func(role, body string) error { return nil }

	dir := t.TempDir()
	ackDir := filepath.Join(dir, "inbox", "oc")
	os.MkdirAll(ackDir, 0o755)
	contractPath := writePartyctlContract(t, dir, contract.RoleSpec{Name: "oc", Tool: "claude_code", PaneID: "%1"})

	// Write ACK message after a short delay so its ModTime is after the command's start time
	go func() {
		time.Sleep(2 * time.Second)
		os.WriteFile(filepath.Join(ackDir, "001.msg"), []byte("oc back online"), 0o644)
	}()

	state := &recycle.RecycleState{
		State:     recycle.StateReady,
		EnteredAt: time.Now().UTC(),
		AgentPID:  1234,
	}
	if err := state.Save(dir, "oc"); err != nil {
		t.Fatal(err)
	}

	cmd := newRootCmd()
	cmd.SetArgs([]string{"restart", "oc", "--contract-path", contractPath})
	out := new(strings.Builder)
	cmd.SetOut(out)
	cmd.SetErr(out)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// Verify remain-on-exit was set
	if len(setOptions) == 0 {
		t.Fatal("expected remain-on-exit to be set")
	}
	found := false
	for _, opt := range setOptions {
		if opt == "%1|remain-on-exit=on" {
			found = true
		}
	}
	if !found {
		t.Fatalf("remain-on-exit not set for %%1, got %v", setOptions)
	}

	// Verify dead pane was respawned
	if len(respawned) != 1 || respawned[0] != "%1" {
		t.Fatalf("expected respawn of %%1, got %v", respawned)
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
