package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/norm/relay-daemon/internal/admin"
	"github.com/norm/relay-daemon/internal/autogen"
	cfgpkg "github.com/norm/relay-daemon/internal/config"
	"github.com/norm/relay-daemon/internal/haiku"
	inbox "github.com/norm/relay-daemon/internal/inbox"
	logpkg "github.com/norm/relay-daemon/internal/log"
	"github.com/norm/relay-daemon/internal/state"
	"github.com/norm/relay-daemon/internal/supervisor"
	tmuxpkg "github.com/norm/relay-daemon/internal/tmux"
	"github.com/norm/relay-daemon/pkg/envelope"
)

func main() {
	cfg, err := cfgpkg.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
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
	if offsets, err := inbox.LoadOffsets(filepath.Join(cfg.ShareDir, "relay", "processed", "offsets.json")); err != nil {
		log.Printf("warning: failed to load offsets: %v", err)
	} else {
		watcher.SetOffsets(offsets)
	}

	// Initialize admin daemon for checkpoint coordination
	adminCfg := admin.DefaultConfig()
	adminCfg.StateDir = cfg.StateDir

	// Create PodConfig for session log discovery
	// Worktree paths loaded from environment (set by ~/.party/env.sh)
	podCfg := &admin.PodConfig{
		PodName: "party",
		Panes:   cfg.PaneTargets,
		Worktrees: map[string]string{
			"oc": os.Getenv("PARTY_OC_WORKTREE"),
			"cc": os.Getenv("PARTY_CC_WORKTREE"),
			"cx": os.Getenv("PARTY_CX_WORKTREE"),
		},
	}

	// Create Haiku client for autogen summaries
	haikuClient, err := haiku.New(haiku.DefaultConfig())
	if err != nil {
		log.Printf("warning: failed to create haiku client: %v (autogen will use heuristic fallback)", err)
		haikuClient = nil
	}

	// Create AutogenGenerator with Haiku client
	autogenCfg := autogen.DefaultConfig()
	autogenCfg.HaikuClient = haikuClient
	gen := autogen.New(autogenCfg)

	adminDaemon := admin.New(adminCfg, podCfg, logger, injector, gen)

	// Create message router for admin-destined messages
	messageRouter := admin.NewMessageRouter(adminDaemon, func(env *envelope.Envelope) error {
		return injector.Inject(env)
	})

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
	go func() {
		if err := adminDaemon.Start(ctx); err != nil {
			log.Printf("admin daemon: %v", err)
		}
	}()
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

			// Record relay activity for checkpoint timing
			adminDaemon.RecordRelayActivity()

			// Handle broadcast to all agents
			if env.To == "all" {
				for _, target := range []string{"oc", "cc", "cx"} {
					cloned := *env
					cloned.To = target
					if err := injector.Inject(&cloned); err != nil {
						_ = logger.Log(logpkg.NewEvent("error", env.From, target).WithMsgID(env.MsgID).WithError(err.Error()))
						continue
					}
				}
				continue
			}

			// Route message (admin-destined messages handled internally)
			handled, err := messageRouter.Route(env)
			if err != nil {
				_ = logger.Log(logpkg.NewEvent("error", env.From, env.To).WithMsgID(env.MsgID).WithError(err.Error()))
				continue
			}
			if handled {
				// Message was handled by admin, already logged
				continue
			}
		}
	}
}
