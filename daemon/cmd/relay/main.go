package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/norm/relay-daemon/internal/adminpane"
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

func main() {
	cfg, err := cfgpkg.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	// Fix 2: Acquire exclusive lockfile to prevent duplicate daemons
	lockPath := filepath.Join(cfg.StateDir, "relay-daemon.lock")
	lockFile, err := acquireLockfile(lockPath)
	if err != nil {
		log.Fatalf("another relay-daemon is already running (lock %s): %v", lockPath, err)
	}
	defer lockFile.Close()

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
		cfg.PaneTargets = map[string]string{"oc": "%0", "cc": "%1", "admin": "%2", "cx": "%3"}
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
	if offsets, err := inbox.LoadOffsets(filepath.Join(cfg.ShareDir, "relay", "processed", "offsets.json")); err != nil {
		log.Printf("warning: failed to load offsets: %v", err)
	} else {
		watcher.SetOffsets(offsets)
	}

	// Admin pane (Addendum A)
	var adminTimer *adminpane.AdminTimer
	if adminPaneID, ok := cfg.PaneTargets["admin"]; ok {
		log.Printf("admin pane enabled (pane %s)", adminPaneID)
		adminTimer = adminpane.NewAdminTimer(injector, cfg, logger)

		adminDir := filepath.Join(filepath.Dir(cfg.StateDir), "admin")
		recycler := adminpane.NewRecycler(mux, cfg, logger, adminPaneID, adminDir)
		adminTimer.SetRecycler(recycler)

		// Idle detection
		if len(cfg.ClaudeProjectDirs) > 0 {
			idleDetector := adminpane.NewIdleDetector(cfg.ClaudeProjectDirs, cfg.IdleBackstopInterval)
			adminTimer.SetIdleDetector(idleDetector)
			log.Printf("idle detection enabled (%d project dirs, backstop %s)", len(cfg.ClaudeProjectDirs), cfg.IdleBackstopInterval)
		}
	} else {
		log.Printf("warning: no 'admin' in pane map; admin timer disabled")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigs
		cancel()
	}()

	errCh := make(chan error, 2)

	go func() {
		if err := watcher.Start(ctx); err != nil {
			errCh <- err
		}
	}()
	go injector.Start(ctx)
	go func() {
		if err := super.Start(ctx); err != nil {
			errCh <- err
		}
	}()
	if adminTimer != nil {
		go adminTimer.Start(ctx)
	}
	if paneTailer != nil {
		go paneTailer.Start(ctx)
	}

	go func() {
		<-ctx.Done()
		offsetPath := filepath.Join(cfg.ShareDir, "relay", "processed", "offsets.json")
		if err := watcher.SaveOffsets(offsetPath); err != nil {
			log.Printf("warning: failed to save offsets: %v", err)
		}
	}()

	for {
		select {
		case err := <-errCh:
			if err != nil {
				log.Printf("relay error: %v", err)
				cancel()
				return
			}
		case env, ok := <-watcher.Events():
			if !ok {
				return
			}
			_ = logger.Log(logpkg.NewEvent(logpkg.EventTypeReceived, env.From, env.To).WithMsgID(env.MsgID))

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
