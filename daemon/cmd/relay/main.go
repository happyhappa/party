package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"syscall"
	"time"

	cfgpkg "github.com/norm/relay-daemon/internal/config"
	inbox "github.com/norm/relay-daemon/internal/inbox"
	logpkg "github.com/norm/relay-daemon/internal/log"
	"github.com/norm/relay-daemon/internal/state"
	"github.com/norm/relay-daemon/internal/supervisor"
	tmuxpkg "github.com/norm/relay-daemon/internal/tmux"
)

const (
	taskBeadFreshWindow       = 30 * time.Minute
	classifierPromptTimeout   = 45 * time.Second
	defaultClassifierStatus   = "in_progress"
	classifierStatusBlocked   = "blocked"
	classifierStatusCompleted = "completed"
	classifierStatusStale     = "stale"
)

// acquireLockfile takes an exclusive non-blocking flock on the given path.
// Returns the open file (caller must keep it open) or an error if already locked.
func acquireLockfile(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

type tombstone struct {
	Timestamp     string `json:"timestamp"`
	Reason        string `json:"reason"`
	Detail        string `json:"detail"`
	PID           int    `json:"pid"`
	UptimeSeconds int64  `json:"uptime_seconds"`
}

type daemonError struct {
	reason string
	detail string
}

type activeBeadState struct {
	BeadID       string `json:"bead_id"`
	MsgSeq       int    `json:"msg_seq"`
	LastActivity string `json:"last_activity"`
	BeadStatus   string `json:"bead_status,omitempty"`
}

type classifierRequest struct {
	Role    string
	BeadID  string
	MsgSeq  int
	Context string
}

type classifierState struct {
	running bool
	pending *classifierRequest
}

type taskBeadManager struct {
	stateDir string
	repo     string
	bdPath   string
	mu       sync.Mutex
	stateMu  sync.Mutex
	byRole   map[string]*classifierState
}

func (e daemonError) Error() string {
	return e.detail
}

func newTaskBeadManager(stateDir, repo string) *taskBeadManager {
	if strings.TrimSpace(repo) == "" {
		repo = "unknown"
	}
	bdPath, err := resolveBDPath()
	if err != nil {
		log.Printf("warning: bd not found, task bead operations disabled: %v", err)
	}
	return &taskBeadManager{
		stateDir: stateDir,
		repo:     repo,
		bdPath:   bdPath,
		byRole: map[string]*classifierState{
			"cc": {},
			"cx": {},
		},
	}
}

func isTaskAgent(role string) bool {
	return role == "cc" || role == "cx"
}

func (m *taskBeadManager) statePath(role string) string {
	return filepath.Join(m.stateDir, "active-bead-"+role)
}

func (m *taskBeadManager) loadState(role string) (*activeBeadState, error) {
	path := m.statePath(role)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var s activeBeadState
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	if strings.TrimSpace(s.BeadID) == "" {
		return nil, nil
	}
	return &s, nil
}

func (m *taskBeadManager) saveState(role string, s *activeBeadState) error {
	if s == nil {
		return os.Remove(m.statePath(role))
	}
	payload, err := json.Marshal(s)
	if err != nil {
		return err
	}
	path := m.statePath(role)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, payload, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func parseClassifierStatus(output string) string {
	token := strings.ToLower(strings.TrimSpace(output))
	switch token {
	case defaultClassifierStatus, classifierStatusBlocked, classifierStatusCompleted:
		return token
	default:
		return defaultClassifierStatus
	}
}

func normalizeBeadStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "open", defaultClassifierStatus, classifierStatusBlocked, classifierStatusCompleted, classifierStatusStale:
		return strings.ToLower(strings.TrimSpace(status))
	default:
		return "open"
	}
}

func resolveBDPath() (string, error) {
	bdPath, err := exec.LookPath("bd")
	if err == nil {
		return bdPath, nil
	}
	for _, p := range []string{
		filepath.Join(os.Getenv("HOME"), "go", "bin", "bd"),
		filepath.Join(os.Getenv("HOME"), ".local", "bin", "bd"),
	} {
		if _, statErr := os.Stat(p); statErr == nil {
			return p, nil
		}
	}
	return "", err
}

func (m *taskBeadManager) buildBDCommand(ctx context.Context, args ...string) (*exec.Cmd, error) {
	if strings.TrimSpace(m.bdPath) == "" {
		return nil, fmt.Errorf("bd not found")
	}
	fullArgs := append([]string{}, args...)
	if beadsDir := strings.TrimSpace(os.Getenv("BEADS_DIR")); beadsDir != "" {
		dbPath := filepath.Join(beadsDir, "beads.db")
		if _, err := os.Stat(dbPath); err == nil {
			fullArgs = append([]string{"--db", dbPath}, fullArgs...)
		}
	}
	return exec.CommandContext(ctx, m.bdPath, fullArgs...), nil
}

func (m *taskBeadManager) bdCombinedOutput(timeout time.Duration, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd, err := m.buildBDCommand(ctx, args...)
	if err != nil {
		return "", err
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func (m *taskBeadManager) appendToBead(beadID, message string) error {
	_, err := m.bdCombinedOutput(15*time.Second, "update", beadID, "--append-notes", message)
	return err
}

func (m *taskBeadManager) beadBody(beadID string) (string, error) {
	out, err := m.bdCombinedOutput(15*time.Second, "show", beadID, "--body")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func (m *taskBeadManager) updateBeadStatus(beadID, status string) error {
	_, err := m.bdCombinedOutput(15*time.Second, "update", beadID, "--status", status)
	return err
}

func (m *taskBeadManager) createTaskBead(target, sender, message string, now time.Time) (string, error) {
	title := fmt.Sprintf("%s task %s", target, now.UTC().Format("2006-01-02 15:04"))
	args := []string{
		"create",
		"--type", "task",
		// bd create defaults new task beads to open.
		"--title", title,
		"--label", "role:" + target,
		"--label", "from:" + sender,
		"--label", "repo:" + m.repo,
		"--label", "source:relay_task",
		"--body", message,
	}
	out, err := m.bdCombinedOutput(20*time.Second, args...)
	if err != nil {
		return "", err
	}
	fields := strings.Fields(out)
	if len(fields) == 0 {
		return "", fmt.Errorf("bd create returned empty bead id")
	}
	return fields[0], nil
}

func (m *taskBeadManager) noteForMessage(envFrom, envTo, payload string, now time.Time) string {
	return fmt.Sprintf("[%s] %s -> %s\n%s", now.UTC().Format(time.RFC3339), envFrom, envTo, payload)
}

func (m *taskBeadManager) classifierContext(taskBody, response string) string {
	base := strings.TrimSpace(taskBody)
	if base == "" {
		base = "(task body unavailable)"
	}
	return base + "\n---\nResponse: " + strings.TrimSpace(response)
}

func (m *taskBeadManager) touchStateIfMatchLocked(role, beadID string, msgSeq int, status string) bool {
	state, err := m.loadState(role)
	if err != nil || state == nil {
		return false
	}
	if state.BeadID != beadID || state.MsgSeq != msgSeq {
		return false
	}
	state.LastActivity = time.Now().UTC().Format(time.RFC3339)
	state.BeadStatus = normalizeBeadStatus(status)
	if err := m.saveState(role, state); err != nil {
		log.Printf("task classifier save state error role=%s bead=%s seq=%d: %v", role, beadID, msgSeq, err)
		return false
	}
	return true
}

func (m *taskBeadManager) sweepStaleActiveBeads() {
	now := time.Now()
	for _, role := range []string{"cc", "cx"} {
		m.stateMu.Lock()
		state, err := m.loadState(role)
		if err != nil || state == nil {
			m.stateMu.Unlock()
			continue
		}
		if normalizeBeadStatus(state.BeadStatus) == classifierStatusCompleted || normalizeBeadStatus(state.BeadStatus) == classifierStatusStale {
			m.stateMu.Unlock()
			continue
		}
		last, parseErr := time.Parse(time.RFC3339, state.LastActivity)
		if parseErr != nil || now.Sub(last) < taskBeadFreshWindow {
			m.stateMu.Unlock()
			continue
		}
		if err := m.updateBeadStatus(state.BeadID, classifierStatusStale); err != nil {
			m.stateMu.Unlock()
			log.Printf("task bead stale sweep warning role=%s bead=%s: %v", role, state.BeadID, err)
			continue
		}
		_ = m.touchStateIfMatchLocked(role, state.BeadID, state.MsgSeq, classifierStatusStale)
		m.stateMu.Unlock()
	}
}

func (m *taskBeadManager) classifyAsync(req *classifierRequest) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), classifierPromptTimeout)
	defer cancel()
	prompt := "Classify this response to a task assignment. Default to in_progress if ambiguous. Reply with exactly one word: in_progress, blocked, or completed."
	cmd := exec.CommandContext(ctx, "codex", "-q", "-a", "full-auto", prompt)
	cmd.Stdin = strings.NewReader(req.Context)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("codex classifier: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return parseClassifierStatus(string(out)), nil
}

func (m *taskBeadManager) scheduleClassifier(role string, req *classifierRequest) {
	m.mu.Lock()
	st, ok := m.byRole[role]
	if !ok {
		m.mu.Unlock()
		return
	}
	if st.running {
		st.pending = req
		m.mu.Unlock()
		return
	}
	st.running = true
	m.mu.Unlock()

	go m.runClassifier(role, req)
}

func (m *taskBeadManager) runClassifier(role string, req *classifierRequest) {
	for {
		status, err := m.classifyAsync(req)
		if err != nil {
			log.Printf("task classifier error role=%s bead=%s seq=%d: %v", role, req.BeadID, req.MsgSeq, err)
		} else {
			m.stateMu.Lock()
			state, loadErr := m.loadState(role)
			if loadErr != nil {
				log.Printf("task classifier state load error role=%s bead=%s seq=%d: %v", role, req.BeadID, req.MsgSeq, loadErr)
			} else if state != nil && state.BeadID == req.BeadID && state.MsgSeq == req.MsgSeq {
				if updateErr := m.updateBeadStatus(req.BeadID, status); updateErr != nil {
					log.Printf("task classifier status update error role=%s bead=%s seq=%d: %v", role, req.BeadID, req.MsgSeq, updateErr)
				} else {
					_ = m.touchStateIfMatchLocked(role, req.BeadID, req.MsgSeq, status)
				}
			}
			m.stateMu.Unlock()
		}

		m.mu.Lock()
		st := m.byRole[role]
		if st == nil || st.pending == nil {
			if st != nil {
				st.running = false
			}
			m.mu.Unlock()
			return
		}
		next := st.pending
		st.pending = nil
		m.mu.Unlock()
		req = next
	}
}

func (m *taskBeadManager) handleOCToAgent(envFrom, envTo, payload string) error {
	now := time.Now()
	m.stateMu.Lock()
	state, err := m.loadState(envTo)
	m.stateMu.Unlock()
	if err != nil {
		return fmt.Errorf("load active bead state: %w", err)
	}

	if state != nil {
		last, parseErr := time.Parse(time.RFC3339, state.LastActivity)
		if parseErr != nil || now.Sub(last) >= taskBeadFreshWindow {
			if staleErr := m.updateBeadStatus(state.BeadID, classifierStatusStale); staleErr != nil {
				log.Printf("task bead stale update warning role=%s bead=%s: %v", envTo, state.BeadID, staleErr)
			} else {
				state.BeadStatus = classifierStatusStale
			}
		}
		status := normalizeBeadStatus(state.BeadStatus)
		if status == classifierStatusCompleted || status == classifierStatusStale {
			state = nil
		}
	}

	if state == nil {
		beadID, createErr := m.createTaskBead(envTo, envFrom, payload, now)
		if createErr != nil {
			return fmt.Errorf("create task bead: %w", createErr)
		}
		newState := &activeBeadState{
			BeadID:       beadID,
			MsgSeq:       1,
			LastActivity: now.UTC().Format(time.RFC3339),
			BeadStatus:   "open",
		}
		m.stateMu.Lock()
		defer m.stateMu.Unlock()
		return m.saveState(envTo, newState)
	}

	note := m.noteForMessage(envFrom, envTo, payload, now)
	if err := m.appendToBead(state.BeadID, note); err != nil {
		return fmt.Errorf("append OC->agent to active bead: %w", err)
	}
	m.stateMu.Lock()
	latest, err := m.loadState(envTo)
	if err != nil {
		m.stateMu.Unlock()
		return fmt.Errorf("load active bead state for update: %w", err)
	}
	if latest == nil || latest.BeadID != state.BeadID {
		m.stateMu.Unlock()
		return nil
	}
	state = latest
	state.MsgSeq++
	state.LastActivity = now.UTC().Format(time.RFC3339)
	defer m.stateMu.Unlock()
	return m.saveState(envTo, state)
}

func (m *taskBeadManager) handleAgentToOC(envFrom, envTo, payload string) error {
	now := time.Now()
	m.stateMu.Lock()
	state, err := m.loadState(envFrom)
	m.stateMu.Unlock()
	if err != nil {
		return fmt.Errorf("load active bead state: %w", err)
	}
	if state == nil {
		return nil
	}
	note := m.noteForMessage(envFrom, envTo, payload, now)
	if err := m.appendToBead(state.BeadID, note); err != nil {
		return fmt.Errorf("append agent->OC to active bead: %w", err)
	}
	m.stateMu.Lock()
	latest, err := m.loadState(envFrom)
	if err != nil {
		m.stateMu.Unlock()
		return fmt.Errorf("load active bead state for update: %w", err)
	}
	if latest == nil || latest.BeadID != state.BeadID {
		m.stateMu.Unlock()
		return nil
	}
	state = latest
	state.MsgSeq++
	state.LastActivity = now.UTC().Format(time.RFC3339)
	if err := m.saveState(envFrom, state); err != nil {
		m.stateMu.Unlock()
		return fmt.Errorf("save active bead state: %w", err)
	}
	m.stateMu.Unlock()
	taskBody, bodyErr := m.beadBody(state.BeadID)
	if bodyErr != nil {
		log.Printf("task bead body lookup warning role=%s bead=%s: %v", envFrom, state.BeadID, bodyErr)
	}
	req := &classifierRequest{
		Role:    envFrom,
		BeadID:  state.BeadID,
		MsgSeq:  state.MsgSeq,
		Context: m.classifierContext(taskBody, payload),
	}
	m.scheduleClassifier(envFrom, req)
	return nil
}

func writeTombstone(stateDir, reason, detail string, pid int, startedAt time.Time) error {
	path := filepath.Join(stateDir, "last-exit.json")
	tmp := path + ".tmp"
	data, err := json.Marshal(tombstone{
		Timestamp:     time.Now().UTC().Format(time.RFC3339),
		Reason:        reason,
		Detail:        detail,
		PID:           pid,
		UptimeSeconds: int64(time.Since(startedAt).Seconds()),
	})
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func main() {
	cfg, err := cfgpkg.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		log.Fatalf("config: %v", err)
	}

	buildInfo := "unknown"
	if bi, ok := debug.ReadBuildInfo(); ok {
		buildInfo = fmt.Sprintf("%s %s", bi.Main.Path, bi.Main.Version)
	}
	log.Printf(
		"relay-daemon starting pid=%d ppid=%d state_dir=%s inbox_dir=%s pane_map_path=%s build=%s",
		os.Getpid(),
		os.Getppid(),
		cfg.StateDir,
		cfg.InboxDir,
		cfg.PaneMapPath,
		buildInfo,
	)

	startedAt := time.Now()
	var exitMu sync.Mutex
	exitReason := "error"
	exitDetail := "unknown"
	setExit := func(reason, detail string) {
		exitMu.Lock()
		defer exitMu.Unlock()
		exitReason = reason
		exitDetail = detail
	}
	getExit := func() (string, string) {
		exitMu.Lock()
		defer exitMu.Unlock()
		return exitReason, exitDetail
	}
	defer func() {
		reason, detail := getExit()
		if err := writeTombstone(cfg.StateDir, reason, detail, os.Getpid(), startedAt); err != nil {
			log.Printf("warning: failed to write tombstone: %v", err)
		}
		log.Printf("relay-daemon exiting reason=%s detail=%s", reason, detail)
	}()

	// Fix 2: Acquire exclusive lockfile to prevent duplicate daemons
	lockPath := filepath.Join(cfg.StateDir, "relay-daemon.lock")
	lockFile, err := acquireLockfile(lockPath)
	if err != nil {
		log.Fatalf("another relay-daemon is already running (lock %s): %v", lockPath, err)
	}
	defer lockFile.Close()
	pidPath := filepath.Join(cfg.StateDir, "relay-daemon.pid")
	if err := os.WriteFile(pidPath, []byte(fmt.Sprintf("%d", os.Getpid())), 0644); err != nil {
		log.Printf("warning: could not write PID file: %v", err)
	}
	defer os.Remove(pidPath)

	// Fix 3: Clean stale session-map files from previous runs
	staleFiles, _ := filepath.Glob(filepath.Join(cfg.StateDir, "session-map-*.json"))
	for _, f := range staleFiles {
		log.Printf("removing stale session-map: %s", f)
		os.Remove(f)
	}

	logger := logpkg.NewEventLog(cfg.LogDir)
	mux := tmuxpkg.New()
	repo := "unknown"
	if cwd, err := os.Getwd(); err == nil {
		repo = filepath.Base(cwd)
	}
	taskBeads := newTaskBeadManager(cfg.StateDir, repo)
	if err := cfg.LoadPaneMap(); err != nil {
		log.Printf("warning: could not load pane map: %v (using defaults)", err)
		cfg.PaneTargets = map[string]string{"oc": "%0", "cc": "%1", "cx": "%2"}
	}
	injector := tmuxpkg.NewInjector(mux, cfg.PaneTargets)
	injector.SetLogger(logger)
	injector.SetPromptGating(cfg.PromptGating)
	injector.SetQueueMaxAge(cfg.QueueMaxAge)

	agents := state.NewAgentTracker(cfg.StateDir)
	if err := agents.Load(); err != nil {
		log.Printf("warning: failed to load agent state: %v", err)
	}
	attacks := state.NewAttackWatcher(cfg.AttacksDir)
	nagger := supervisor.NewNagger(attacks, injector, logger, cfg.StuckThreshold, cfg.NagInterval, cfg.MaxNagDuration)
	recovery := supervisor.NewRecoveryHandler(injector, logger)
	super := supervisor.NewSupervisor(agents, attacks, nagger, recovery, 60*time.Second)
	var paneTailer *supervisor.PaneTailer
	if cfg.PaneTailEnabled {
		paneTailer = supervisor.NewPaneTailer(mux, cfg.PaneTargets, cfg.PaneTailLines, cfg.PaneTailRotations, cfg.PaneTailDir, cfg.PaneTailInterval, logger)
	}

	watcher, err := inbox.NewWatcher(cfg.InboxDir)
	if err != nil {
		log.Fatalf("watcher: %v", err)
	}
	defer watcher.Close()
	if offsets, err := inbox.LoadOffsets(filepath.Join(cfg.StateDir, "offsets.json")); err != nil {
		log.Printf("warning: failed to load offsets: %v", err)
	} else {
		watcher.SetOffsets(offsets)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		sig := <-sigs
		log.Printf("signal received: %s", sig)
		setExit("signal", sig.String())
		cancel()
	}()

	errCh := make(chan error, 5)
	runProtected := func(name string, fn func() error) {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					stack := debug.Stack()
					detail := fmt.Sprintf("%s panic: %v", name, r)
					log.Printf("%s\n%s", detail, stack)
					if err := writeTombstone(cfg.StateDir, "panic", detail, os.Getpid(), startedAt); err != nil {
						log.Printf("warning: failed to write panic tombstone: %v", err)
					}
					errCh <- daemonError{reason: "panic", detail: detail}
				}
			}()
			if err := fn(); err != nil {
				errCh <- daemonError{reason: "error", detail: fmt.Sprintf("%s: %v", name, err)}
			}
		}()
	}

	// Hot-reload panes.json when it changes on disk.
	runProtected("pane-map-reload", func() error {
		var lastMod time.Time
		if info, err := os.Stat(cfg.PaneMapPath); err == nil {
			lastMod = info.ModTime()
		}

		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return nil
			case <-ticker.C:
				info, err := os.Stat(cfg.PaneMapPath)
				if err != nil {
					continue
				}
				if info.ModTime().Equal(lastMod) {
					continue
				}

				lastMod = info.ModTime()
				targets, err := cfgpkg.ReadPaneMap(cfg.PaneMapPath)
				if err != nil {
					log.Printf("pane map reload failed: %v", err)
					continue
				}
				injector.UpdateTargets(targets)
				log.Printf("pane map reloaded: %v", targets)
			}
		}
	})

	runProtected("watcher", func() error {
		return watcher.Start(ctx)
	})
	runProtected("injector", func() error {
		injector.Start(ctx)
		return nil
	})
	runProtected("supervisor", func() error {
		return super.Start(ctx)
	})
	runProtected("task-bead-stale-sweep", func() error {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-ticker.C:
				taskBeads.sweepStaleActiveBeads()
			}
		}
	})
	if paneTailer != nil {
		runProtected("pane-tailer", func() error {
			paneTailer.Start(ctx)
			return nil
		})
	}

	go func() {
		<-ctx.Done()
		offsetPath := filepath.Join(cfg.StateDir, "offsets.json")
		if err := watcher.SaveOffsets(offsetPath); err != nil {
			log.Printf("warning: failed to save offsets: %v", err)
		}
	}()

	for {
		select {
		case err := <-errCh:
			if err != nil {
				reason := "error"
				detail := err.Error()
				var dErr daemonError
				if errors.As(err, &dErr) {
					reason = dErr.reason
					detail = dErr.detail
				}
				setExit(reason, detail)
				log.Printf("relay error: %v", detail)
				cancel()
				return
			}
		case env, ok := <-watcher.Events():
			if !ok {
				if ctx.Err() != nil {
					reason, detail := getExit()
					if reason == "error" && detail == "unknown" {
						setExit("signal", "context canceled")
					}
				} else {
					setExit("error", "watcher events channel closed unexpectedly")
				}
				return
			}
			_ = logger.Log(logpkg.NewEvent(logpkg.EventTypeReceived, env.From, env.To).WithMsgID(env.MsgID))

			if env.Kind == "chat" && env.From == "oc" && isTaskAgent(env.To) {
				from := env.From
				to := env.To
				payload := env.Payload
				msgID := env.MsgID
				go func() {
					if err := taskBeads.handleOCToAgent(from, to, payload); err != nil {
						log.Printf("task bead OC->agent warning from=%s to=%s msg=%s: %v", from, to, msgID, err)
						_ = logger.Log(logpkg.NewEvent("task_bead_error", from, to).WithMsgID(msgID).WithError(err.Error()))
					}
				}()
			}
			if env.Kind == "chat" && env.To == "oc" && isTaskAgent(env.From) {
				from := env.From
				to := env.To
				payload := env.Payload
				msgID := env.MsgID
				go func() {
					if err := taskBeads.handleAgentToOC(from, to, payload); err != nil {
						log.Printf("task bead agent->OC warning from=%s to=%s msg=%s: %v", from, to, msgID, err)
						_ = logger.Log(logpkg.NewEvent("task_bead_error", from, to).WithMsgID(msgID).WithError(err.Error()))
					}
				}()
			}

			// Deprecated: checkpoint_content handler retained as no-op for in-flight producers. Remove in Phase C.
			if env.To == "admin" && env.Kind == "checkpoint_content" {
				log.Printf("deprecated: checkpoint_content message from=%s, ignoring (RFC-006)", env.From)
				_ = logger.Log(logpkg.NewEvent(logpkg.EventTypeCheckpointAck, env.From, "admin").WithMsgID(env.MsgID).WithStatus("ignored:deprecated"))
				continue
			}

			// Handle broadcast to all agents (including admin if present)
			if env.To == "all" {
				broadcastTargets := []string{"oc", "cc", "cx"}
				if _, ok := cfg.PaneTargets["admin"]; ok {
					broadcastTargets = append(broadcastTargets, "admin")
				}
				for _, target := range broadcastTargets {
					cloned := *env
					cloned.To = target
					if err := injector.Inject(&cloned); err != nil {
						_ = logger.Log(logpkg.NewEvent("error", env.From, target).WithMsgID(env.MsgID).WithError(err.Error()))
						continue
					}
				}
				continue
			}

			// Admin-destined messages: forward to pane
			if env.To == "admin" {
				if err := injector.Inject(env); err != nil {
					_ = logger.Log(logpkg.NewEvent("error", env.From, "admin").WithMsgID(env.MsgID).WithError(err.Error()))
				}
				continue
			}

			// Standard message routing via injector
			if err := injector.Inject(env); err != nil {
				_ = logger.Log(logpkg.NewEvent("error", env.From, env.To).WithMsgID(env.MsgID).WithError(err.Error()))
			}
		}
	}
}
