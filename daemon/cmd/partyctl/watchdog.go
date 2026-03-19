package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/norm/relay-daemon/internal/contract"
	"github.com/norm/relay-daemon/internal/pane"
	"github.com/norm/relay-daemon/internal/recycle"
	"github.com/spf13/cobra"
)

func newWatchdogCmd() *cobra.Command {
	var tick time.Duration
	var once bool
	var dryRun bool
	var contractPath string

	cmd := &cobra.Command{
		Use:   "watchdog",
		Short: "Long-running health check and brief generation loop",
		Long: `Singleton loop that replaces admin-health-check.sh, admin-watchdog.sh,
and continuous-brief-loop.sh. Runs health checks, triggers recycles when
context thresholds are exceeded, and generates continuous briefs.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := loadOrBuildContract(contractPath)
			if err != nil {
				return fmt.Errorf("load contract: %w", err)
			}
			cfg := watchdogConfig{
				tick:     tick,
				once:     once,
				dryRun:   dryRun,
				contract: c,
				stateDir: c.Paths.StateDir,
			}
			return runWatchdog(cmd, cfg)
		},
	}
	cmd.Flags().DurationVar(&tick, "tick", 30*time.Second, "interval between watchdog cycles")
	cmd.Flags().BoolVar(&once, "once", false, "run one cycle then exit (for testing)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "log actions but don't trigger recycles or briefs")
	cmd.Flags().StringVar(&contractPath, "contract-path", "", "path to contract JSON")
	return cmd
}

type watchdogConfig struct {
	tick     time.Duration
	once     bool
	dryRun   bool
	contract *contract.Contract
	stateDir string
}

// watchdogEvent is a structured log entry emitted to stdout.
type watchdogEvent struct {
	Timestamp string `json:"timestamp"`
	Role      string `json:"role,omitempty"`
	Action    string `json:"action"`
	Result    string `json:"result"`
	Detail    string `json:"detail,omitempty"`
}

func logEvent(cmd *cobra.Command, role, action, result, detail string) {
	ev := watchdogEvent{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Role:      role,
		Action:    action,
		Result:    result,
		Detail:    detail,
	}
	data, _ := json.Marshal(ev)
	fmt.Fprintln(cmd.OutOrStdout(), string(data))
}

func runWatchdog(cmd *cobra.Command, cfg watchdogConfig) error {
	// Singleton enforcement: pidfile + flock
	pidPath := filepath.Join(cfg.stateDir, "watchdog.pid")
	lockPath := filepath.Join(cfg.stateDir, "watchdog.lock")

	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("open watchdog lock: %w", err)
	}
	defer lockFile.Close()

	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return fmt.Errorf("another watchdog is already running (flock on %s)", lockPath)
	}
	defer syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)

	// Write pidfile
	if err := os.WriteFile(pidPath, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0o644); err != nil {
		return fmt.Errorf("write pidfile: %w", err)
	}
	defer os.Remove(pidPath)

	// Signal handling for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigCh
		logEvent(cmd, "", "shutdown", "received", "signal")
		cancel()
	}()

	logEvent(cmd, "", "startup", "ok", fmt.Sprintf("tick=%s once=%v dry_run=%v", cfg.tick, cfg.once, cfg.dryRun))

	// Per-role last brief time tracking
	lastBriefTime := map[string]time.Time{}

	for {
		if ctx.Err() != nil {
			break
		}

		runWatchdogCycle(cmd, cfg, lastBriefTime)

		if cfg.once {
			break
		}

		select {
		case <-ctx.Done():
		case <-time.After(cfg.tick):
		}
	}

	logEvent(cmd, "", "shutdown", "clean", "")
	return nil
}

func runWatchdogCycle(cmd *cobra.Command, cfg watchdogConfig, lastBriefTime map[string]time.Time) {
	for _, role := range cfg.contract.Roles {
		// Load recycle state
		state, err := recycle.LoadState(cfg.stateDir, role.Name)
		if err != nil {
			logEvent(cmd, role.Name, "health", "error", fmt.Sprintf("load state: %v", err))
			continue
		}

		// Skip roles that are mid-recycle
		if state.State != recycle.StateReady {
			logEvent(cmd, role.Name, "health", "skip", fmt.Sprintf("state=%s", state.State))
			continue
		}

		// Health check: get context usage
		tool, ok := cfg.contract.Tools[role.Tool]
		if !ok {
			logEvent(cmd, role.Name, "health", "error", fmt.Sprintf("unknown tool %q", role.Tool))
			continue
		}

		contextPct, source, err := getContextPct(cfg, role, tool)
		if err != nil {
			logEvent(cmd, role.Name, "health", "telemetry_error", err.Error())
			continue
		}

		threshold := tool.Recycle.ThresholdUsedPct
		exceeded := contextPct >= threshold && threshold > 0

		logEvent(cmd, role.Name, "health", "ok",
			fmt.Sprintf("context=%d%% threshold=%d%% exceeded=%v source=%s pid=%d",
				contextPct, threshold, exceeded, source, state.AgentPID))

		// Trigger recycle if threshold exceeded
		if exceeded {
			if cfg.dryRun {
				logEvent(cmd, role.Name, "recycle", "dry_run",
					fmt.Sprintf("would trigger recycle at %d%%", contextPct))
			} else {
				triggered := triggerRecycle(cfg, role.Name, contextPct, threshold)
				if triggered {
					logEvent(cmd, role.Name, "recycle", "triggered",
						fmt.Sprintf("context %d%% >= threshold %d%%", contextPct, threshold))
				} else {
					logEvent(cmd, role.Name, "recycle", "skipped", "state changed or lock busy")
				}
			}
			continue // Don't generate briefs when recycling
		}

		// Brief cadence check
		cadence := tool.Recycle.BriefCadence.Duration
		if cadence <= 0 {
			cadence = 5 * time.Minute
		}
		minDelta := int64(tool.Recycle.BriefMinDelta)
		if minDelta <= 0 {
			minDelta = recycle.DefaultBriefMinDelta
		}

		lastBrief, hasPrev := lastBriefTime[role.Name]
		if !hasPrev || time.Since(lastBrief) >= cadence {
			generated := maybeGenerateBrief(cmd, cfg, role.Name, state, minDelta)
			if generated {
				lastBriefTime[role.Name] = time.Now()
			}
		}
	}
}

// getContextPct reads context usage for a role, using sidecar for CC/OC and pane parsing for CX.
func getContextPct(cfg watchdogConfig, role contract.RoleSpec, tool contract.AgentToolSpec) (int, string, error) {
	if tool.Telemetry.HasSidecar {
		pct, _, err := readSidecarContext(cfg.stateDir, role.Name, tool.Telemetry)
		if err != nil {
			return 0, "sidecar", err
		}
		return pct, "sidecar", nil
	}

	// Non-sidecar (CX): parse pane text via tmux capture
	pct, err := readContextFromPane(cfg.stateDir, role.Name, tool)
	if err != nil {
		return 0, "pane", err
	}
	return pct, "pane", nil
}

// readContextFromPane captures a pane via tmux and parses context percentage
// using the contract's PaneParserSpec for the tool.
func readContextFromPane(stateDir, role string, tool contract.AgentToolSpec) (int, error) {
	// Read pane ID from panes.json
	panesPath := filepath.Join(stateDir, "panes.json")
	data, err := os.ReadFile(panesPath)
	if err != nil {
		return 0, fmt.Errorf("read panes.json: %w", err)
	}
	var panesFile struct {
		Panes map[string]string `json:"panes"`
	}
	if err := json.Unmarshal(data, &panesFile); err != nil {
		return 0, fmt.Errorf("parse panes.json: %w", err)
	}
	paneID, ok := panesFile.Panes[role]
	if !ok || paneID == "" {
		return 0, fmt.Errorf("no pane ID for role %s", role)
	}

	// Capture pane text
	out, err := exec.Command("tmux", "capture-pane", "-t", paneID, "-p").Output()
	if err != nil {
		return 0, fmt.Errorf("tmux capture-pane: %w", err)
	}

	// Contract-driven parsing via PaneParserSpec
	state := pane.ParsePaneStateFromSpec(tool.PaneParser, string(out))
	if state.ContextPct < 0 {
		return 0, fmt.Errorf("context percentage not found in pane text")
	}
	return state.ContextPct, nil
}

// triggerRecycle acquires the flock and transitions a role from ready to exiting.
func triggerRecycle(cfg watchdogConfig, role string, contextPct, threshold int) bool {
	lock, err := recycle.AcquireLock(cfg.stateDir, role)
	if err != nil {
		return false
	}
	defer lock.Release()

	state, err := recycle.LoadState(cfg.stateDir, role)
	if err != nil || state.State != recycle.StateReady {
		return false
	}

	if err := state.Transition(recycle.StateExiting); err != nil {
		return false
	}
	state.RecycleReason = fmt.Sprintf("context %d%% >= threshold %d%%", contextPct, threshold)
	if err := state.Save(cfg.stateDir, role); err != nil {
		return false
	}

	// Best-effort relay notification
	if relayPath, err := exec.LookPath("relay"); err == nil {
		exec.Command(relayPath, "send", "all",
			fmt.Sprintf("%s is recycling (context %d%%), stand by", role, contextPct)).Run()
	}

	// Shell out to recycle-agent.sh for the full lifecycle
	scriptDir := findScriptDir()
	recycleScript := filepath.Join(scriptDir, "recycle-agent.sh")
	if _, err := os.Stat(recycleScript); err == nil {
		recycleCmd := exec.Command(recycleScript, role, "--reason", state.RecycleReason)
		recycleCmd.Env = append(os.Environ(),
			"RELAY_STATE_DIR="+cfg.stateDir,
			"RELAY_SHARE_DIR="+cfg.contract.Paths.ShareDir,
		)
		recycleCmd.Stdout = os.Stdout
		recycleCmd.Stderr = os.Stderr
		go recycleCmd.Run() // Run in background so watchdog continues
	}

	return true
}

// findScriptDir locates the admin scripts directory relative to the partyctl binary.
func findScriptDir() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return ""
	}
	// partyctl is at daemon/cmd/partyctl or ~/.local/bin/partyctl
	// scripts are at daemon/scripts/admin/
	// Try relative to exe first
	candidates := []string{
		filepath.Join(filepath.Dir(exe), "..", "..", "scripts", "admin"),
		filepath.Join(filepath.Dir(exe), "..", "daemon", "scripts", "admin"),
	}
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && info.IsDir() {
			return c
		}
	}
	return ""
}

// maybeGenerateBrief checks transcript delta and generates a brief if warranted.
func maybeGenerateBrief(cmd *cobra.Command, cfg watchdogConfig, role string, state *recycle.RecycleState, minDelta int64) bool {
	if state.TranscriptPath == "" {
		return false
	}

	info, err := os.Stat(state.TranscriptPath)
	if err != nil {
		return false
	}

	delta := info.Size() - state.LastBriefOffset
	if delta < minDelta {
		return false
	}

	if cfg.dryRun {
		logEvent(cmd, role, "brief", "dry_run",
			fmt.Sprintf("would generate brief (delta=%d bytes)", delta))
		return true
	}

	// Find required paths
	filterPath, _ := exec.LookPath("party-jsonl-filter")
	promptPath := findPromptPath()

	opts := recycle.BriefOptions{
		Role:           role,
		TranscriptPath: state.TranscriptPath,
		StartOffset:    state.LastBriefOffset,
		EndOffset:      info.Size(),
		SessionID:      state.SessionID,
		FilterPath:     filterPath,
		PromptPath:     promptPath,
		Generator:      "codex",
		Source:         "continuous",
		BeadsDir:       os.Getenv("BEADS_DIR"),
	}

	result, err := recycle.GenerateBrief(opts)
	if err != nil {
		logEvent(cmd, role, "brief", "error", err.Error())
		return false
	}

	// Update recycle state with new offset
	lock, lockErr := recycle.AcquireLock(cfg.stateDir, role)
	if lockErr != nil {
		logEvent(cmd, role, "brief", "lock_error", lockErr.Error())
		return true // Brief was generated, just couldn't update offset
	}
	defer lock.Release()

	state, err = recycle.LoadState(cfg.stateDir, role)
	if err == nil {
		state.LastBriefOffset = opts.EndOffset
		state.LastBriefAt = time.Now().UTC()
		state.Save(cfg.stateDir, role)
	}

	// Cleanup old briefs
	if beadsDir := os.Getenv("BEADS_DIR"); beadsDir != "" {
		recycle.CleanupOldBriefs(beadsDir, role, 3)
	}

	logEvent(cmd, role, "brief", "generated",
		fmt.Sprintf("bead=%s range=%d-%d", result.BeadID, result.ByteRange[0], result.ByteRange[1]))
	return true
}

// findPromptPath locates the brief prompt file.
func findPromptPath() string {
	candidates := []string{
		// Relative to current working dir
		"daemon/scripts/party-brief-prompt.txt",
		"scripts/party-brief-prompt.txt",
	}
	// Also check relative to executable
	if exe, err := os.Executable(); err == nil {
		if exe, err = filepath.EvalSymlinks(exe); err == nil {
			candidates = append(candidates,
				filepath.Join(filepath.Dir(exe), "..", "..", "scripts", "party-brief-prompt.txt"),
				filepath.Join(filepath.Dir(exe), "..", "daemon", "scripts", "party-brief-prompt.txt"),
			)
		}
	}
	// Check PATH-adjacent
	if p, err := exec.LookPath("party-jsonl-filter"); err == nil {
		candidates = append(candidates,
			filepath.Join(filepath.Dir(p), "party-brief-prompt.txt"),
		)
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			abs, _ := filepath.Abs(c)
			return abs
		}
	}
	return ""
}

// readPanesJSON reads panes.json to get pane IDs.
func readPanesJSON(stateDir string) (map[string]string, error) {
	data, err := os.ReadFile(filepath.Join(stateDir, "panes.json"))
	if err != nil {
		return nil, err
	}
	var f struct {
		Panes map[string]string `json:"panes"`
	}
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, err
	}
	return f.Panes, nil
}

