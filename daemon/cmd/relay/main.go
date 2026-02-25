package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"sync"
	"syscall"
	"time"

	"github.com/norm/relay-daemon/internal/checkpoint"
	cfgpkg "github.com/norm/relay-daemon/internal/config"
	inbox "github.com/norm/relay-daemon/internal/inbox"
	logpkg "github.com/norm/relay-daemon/internal/log"
	"github.com/norm/relay-daemon/internal/state"
	"github.com/norm/relay-daemon/internal/supervisor"
	tmuxpkg "github.com/norm/relay-daemon/internal/tmux"
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

func (e daemonError) Error() string {
	return e.detail
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

			// Handle checkpoint content directly in relay and write beads using
			// single-writer daemon ownership.
			if env.To == "admin" && env.Kind == "checkpoint_content" {
				cc, err := checkpoint.Parse(env.Payload)
				if err != nil {
					_ = logger.Log(logpkg.NewEvent("checkpoint_content_error", env.From, "admin").WithMsgID(env.MsgID).WithError(err.Error()))
					continue
				}

				// Normalize checkpoint correlation key at daemon-write time.
				// Keep the agent-provided chk_id for traceability.
				originalChkID := cc.ChkID
				if cc.Labels == nil {
					cc.Labels = map[string]string{}
				}
				cc.Labels["agent_chk_id"] = originalChkID
				cycleKey := fmt.Sprintf("cycle-%d", time.Now().UTC().Unix()/60)
				cc.ChkID = cycleKey

				if cc.Role != env.From {
					_ = logger.Log(logpkg.NewEvent("checkpoint_content_error", env.From, "admin").WithMsgID(env.MsgID).WithChkID(cc.ChkID).WithError("role mismatch"))
					continue
				}
				beadID, err := checkpoint.WriteBead(cc)
				if err != nil {
					_ = logger.Log(logpkg.NewEvent("checkpoint_bead_error", env.From, "admin").WithMsgID(env.MsgID).WithChkID(cc.ChkID).WithError(err.Error()))
					continue
				}
				_ = logger.Log(logpkg.NewEvent(logpkg.EventTypeCheckpointAck, env.From, "admin").WithMsgID(env.MsgID).WithChkID(cc.ChkID).WithStatus("written:" + beadID))
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
