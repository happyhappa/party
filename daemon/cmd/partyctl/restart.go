package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	configpkg "github.com/norm/relay-daemon/internal/config"
	"github.com/norm/relay-daemon/internal/contract"
	"github.com/norm/relay-daemon/internal/recycle"
	"github.com/spf13/cobra"
)

var (
	panePIDFunc          = panePID
	tmuxSendLiteralFunc  = tmuxSendLiteral
	tmuxSendKeyFunc      = tmuxSendKey
	sendRelayMessageFunc = sendRelayMessage
	sendRelayDirectFunc  = sendRelayDirect
	gracefulKillFunc     = recycle.GracefulKill
	forceKillPIDFunc     = forceKillPID
	assembleHydration    = recycle.AssembleHydration
)

func newRestartCmd() *cobra.Command {
	var contractPath string
	var force bool

	cmd := &cobra.Command{
		Use:   "restart <role>",
		Short: "Recycle a role: exit, relaunch, and hydrate",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRestart(cmd, contractPath, args[0], force, time.Now().UTC())
		},
	}
	cmd.Flags().StringVar(&contractPath, "contract-path", "", "path to contract JSON")
	cmd.Flags().BoolVar(&force, "force", false, "skip graceful exit and go straight to SIGKILL")
	return cmd
}

func runRestart(cmd *cobra.Command, contractPath, role string, force bool, now time.Time) error {
	c, roleSpec, toolSpec, err := loadContractRoleAndTool(contractPath, role)
	if err != nil {
		return err
	}

	lock, err := recycle.AcquireLock(c.Paths.StateDir, roleSpec.Name)
	if err != nil {
		return err
	}
	defer lock.Release()

	state, err := recycle.LoadState(c.Paths.StateDir, roleSpec.Name)
	if err != nil {
		return err
	}
	prevSessionID := state.SessionID
	prevTranscript := state.TranscriptPath

	paneID, err := lookupPaneID(c, roleSpec.Name)
	if err != nil {
		return err
	}

	if err := state.Transition(recycle.StateExiting); err != nil {
		return err
	}
	state.RecycleReason = "manual"
	state.EnteredAt = now
	if err := state.Save(c.Paths.StateDir, roleSpec.Name); err != nil {
		return fmt.Errorf("save exiting state: %w", err)
	}

	state.RelayAvailable = sendRelayMessageFunc(roleSpec.Name, fmt.Sprintf("%s is recycling, stand by", roleSpec.Name)) == nil
	if err := state.Save(c.Paths.StateDir, roleSpec.Name); err != nil {
		return fmt.Errorf("save relay state: %w", err)
	}

	if !force {
		if err := sendExitCommand(paneID, toolSpec.Recycle.ExitCommand); err != nil {
			return fmt.Errorf("send exit command: %w", err)
		}
	}

	if err := state.Transition(recycle.StateConfirming); err != nil {
		return err
	}
	if err := state.Save(c.Paths.StateDir, roleSpec.Name); err != nil {
		return fmt.Errorf("save confirming state: %w", err)
	}

	killStart := time.Now()
	if force {
		if err := forceKillPIDFunc(state.AgentPID); err != nil {
			_ = state.TransitionFailed(err.Error())
			_ = state.Save(c.Paths.StateDir, roleSpec.Name)
			return err
		}
	} else {
		if err := gracefulKillFunc(state.AgentPID, toolSpec.Recycle.GracePeriod.Duration); err != nil {
			_ = state.TransitionFailed(err.Error())
			_ = state.Save(c.Paths.StateDir, roleSpec.Name)
			return err
		}
	}
	if recycle.WasForceKilled(killStart, toolSpec.Recycle.GracePeriod.Duration) {
		if err := state.TransitionDegraded("process required force kill"); err == nil {
			_ = state.Save(c.Paths.StateDir, roleSpec.Name)
		}
	}

	if state.State == recycle.StateDegraded {
		if err := state.Transition(recycle.StateRelaunching); err != nil {
			return err
		}
	} else {
		if err := state.Transition(recycle.StateRelaunching); err != nil {
			return err
		}
	}
	if err := state.Save(c.Paths.StateDir, roleSpec.Name); err != nil {
		return fmt.Errorf("save relaunching state: %w", err)
	}

	if err := relaunchRole(paneID, c, roleSpec, toolSpec); err != nil {
		return handleRestartFailure(state, c.Paths.StateDir, roleSpec.Name, err)
	}

	time.Sleep(1 * time.Second)
	state.AgentPID = panePIDFunc(paneID)
	if state.TranscriptPath == "" {
		state.TranscriptPath = prevTranscript
	}
	if err := state.Transition(recycle.StateHydrating); err != nil {
		return err
	}
	if err := state.Save(c.Paths.StateDir, roleSpec.Name); err != nil {
		return fmt.Errorf("save hydrating state: %w", err)
	}

	payload, _ := assembleHydration(recycle.HydrationOptions{
		Role:           roleSpec.Name,
		PrevSessionID:  prevSessionID,
		TranscriptPath: prevTranscript,
		BeadsDir:       c.Paths.BeadsDir,
		InboxDir:       c.Paths.InboxDir,
	})
	if payload != nil {
		_ = sendRelayDirectFunc(roleSpec.Name, payload.FormatForInjection())
	}

	ackSeen, err := waitForAck(c.Paths.InboxDir, roleSpec.Name, now, 30*time.Second)
	if err != nil {
		return err
	}
	if !ackSeen {
		if state.FailureCount >= 2 {
			_ = state.TransitionFailed("timed out waiting for back online ACK")
		} else {
			_ = state.TransitionDegraded("timed out waiting for back online ACK")
		}
		_ = state.Save(c.Paths.StateDir, roleSpec.Name)
		return fmt.Errorf("timed out waiting for %s back online ACK", roleSpec.Name)
	}

	if state.State == recycle.StateDegraded {
		if err := state.Transition(recycle.StateRelaunching); err != nil {
			return err
		}
		if err := state.Transition(recycle.StateHydrating); err != nil {
			return err
		}
	}
	if err := state.Transition(recycle.StateReady); err != nil {
		return err
	}
	state.EnteredAt = now
	if err := state.Save(c.Paths.StateDir, roleSpec.Name); err != nil {
		return fmt.Errorf("save ready state: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Recycled %s (pane %s, pid %d)\n", roleSpec.Name, paneID, state.AgentPID)
	return nil
}

func loadContractRoleAndTool(contractPath, role string) (*contract.Contract, contract.RoleSpec, contract.AgentToolSpec, error) {
	c, err := loadOrBuildContract(contractPath)
	if err != nil {
		return nil, contract.RoleSpec{}, contract.AgentToolSpec{}, fmt.Errorf("load contract: %w", err)
	}
	for _, roleSpec := range c.Roles {
		if roleSpec.Name == role {
			toolSpec, ok := c.Tools[roleSpec.Tool]
			if !ok {
				return nil, contract.RoleSpec{}, contract.AgentToolSpec{}, fmt.Errorf("role %q references unknown tool %q", role, roleSpec.Tool)
			}
			return c, roleSpec, toolSpec, nil
		}
	}
	return nil, contract.RoleSpec{}, contract.AgentToolSpec{}, fmt.Errorf("unknown role %q", role)
}

func lookupPaneID(c *contract.Contract, role string) (string, error) {
	for _, roleSpec := range c.Roles {
		if roleSpec.Name == role && roleSpec.PaneID != "" {
			return roleSpec.PaneID, nil
		}
	}

	path := c.Paths.PaneMap
	if path == "" {
		path = filepath.Join(c.Paths.StateDir, "panes.json")
	}
	panes, err := configpkg.ReadPaneMap(path)
	if err != nil {
		return "", fmt.Errorf("read pane map: %w", err)
	}
	paneID := panes[strings.ToLower(role)]
	if paneID == "" {
		return "", fmt.Errorf("role %q not found in pane map %s", role, path)
	}
	return paneID, nil
}

func sendExitCommand(paneID, exitCommand string) error {
	switch exitCommand {
	case "", "/exit":
		if err := tmuxSendLiteralFunc(paneID, "/exit"); err != nil {
			return err
		}
		return tmuxSendKeyFunc(paneID, "Enter")
	case "ctrl-c":
		if err := tmuxSendKeyFunc(paneID, "C-c"); err != nil {
			return err
		}
		if err := tmuxSendLiteralFunc(paneID, "exit"); err != nil {
			return err
		}
		return tmuxSendKeyFunc(paneID, "Enter")
	default:
		if err := tmuxSendLiteralFunc(paneID, exitCommand); err != nil {
			return err
		}
		return tmuxSendKeyFunc(paneID, "Enter")
	}
}

func forceKillPID(pid int) error {
	if pid <= 0 {
		return fmt.Errorf("invalid pid: %d", pid)
	}
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil && err != syscall.ESRCH {
		return fmt.Errorf("send SIGKILL to %d: %w", pid, err)
	}
	for i := 0; i < 10; i++ {
		if !recycle.IsAlive(pid) {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("pid %d still alive after SIGKILL", pid)
}

func relaunchRole(paneID string, c *contract.Contract, role contract.RoleSpec, tool contract.AgentToolSpec) error {
	command := buildLaunchCommand(c, role, tool)
	if err := tmuxSendLiteralFunc(paneID, command); err != nil {
		return err
	}
	return tmuxSendKeyFunc(paneID, "Enter")
}

func buildLaunchCommand(c *contract.Contract, role contract.RoleSpec, tool contract.AgentToolSpec) string {
	env := map[string]string{
		"AGENT_ROLE":         role.Name,
		"RELAY_MAIN_DIR":     os.Getenv("RELAY_MAIN_DIR"),
		"RELAY_SHARE_DIR":    c.Paths.ShareDir,
		"RELAY_STATE_DIR":    c.Paths.StateDir,
		"RELAY_LOG_DIR":      c.Paths.LogDir,
		"RELAY_INBOX_DIR":    c.Paths.InboxDir,
		"RELAY_TMUX_SESSION": os.Getenv("RELAY_TMUX_SESSION"),
		"BEADS_DIR":          c.Paths.BeadsDir,
	}
	for k, v := range tool.Launch.Env {
		env[k] = v
	}
	for k, v := range role.Env {
		env[k] = v
	}

	parts := make([]string, 0, len(env)+len(tool.Launch.Args)+6)
	for _, key := range sortedKeys(env) {
		if env[key] == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("export %s=%s", key, shellQuote(env[key])))
	}
	if role.WorktreeDir != "" {
		parts = append(parts, "cd "+shellQuote(role.WorktreeDir))
	}
	var cmdParts []string
	cmdParts = append(cmdParts, shellQuote(tool.Launch.Command))
	for _, arg := range tool.Launch.Args {
		cmdParts = append(cmdParts, shellQuote(arg))
	}
	parts = append(parts, "exec "+strings.Join(cmdParts, " "))
	return strings.Join(parts, " && ")
}

func panePID(paneID string) int {
	out, err := exec.Command("tmux", "display-message", "-p", "-t", paneID, "#{pane_pid}").Output()
	if err != nil {
		return 0
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(out)))
	return pid
}

func tmuxSendLiteral(paneID, text string) error {
	return exec.Command("tmux", "send-keys", "-t", paneID, "-l", text).Run()
}

func tmuxSendKey(paneID, key string) error {
	return exec.Command("tmux", "send-keys", "-t", paneID, key).Run()
}

func sendRelayMessage(role, body string) error {
	return exec.Command("relay", "send", "--from", "admin", "all", body).Run()
}

func sendRelayDirect(role, body string) error {
	return exec.Command("relay", "send", "--from", "admin", role, body).Run()
}

func waitForAck(inboxDir, role string, start time.Time, timeout time.Duration) (bool, error) {
	roleDir := filepath.Join(inboxDir, role)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		seen, err := scanAckMessages(roleDir, role, start)
		if err != nil {
			return false, err
		}
		if seen {
			return true, nil
		}
		time.Sleep(time.Second)
	}
	return false, nil
}

func scanAckMessages(dir, role string, start time.Time) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	needle := strings.ToLower(role + " back online")
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".msg") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(start) {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if strings.Contains(strings.ToLower(string(data)), needle) {
			return true, nil
		}
	}
	return false, nil
}

func handleRestartFailure(state *recycle.RecycleState, stateDir, role string, err error) error {
	if state.FailureCount >= 2 {
		_ = state.TransitionFailed(err.Error())
	} else {
		_ = state.TransitionDegraded(err.Error())
	}
	_ = state.Save(stateDir, role)
	return err
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func execLookPath(name string) (string, error) {
	return exec.LookPath(name)
}
