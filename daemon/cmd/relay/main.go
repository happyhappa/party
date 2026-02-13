package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/norm/relay-daemon/internal/admin"
	"github.com/norm/relay-daemon/internal/adminpane"
	cfgpkg "github.com/norm/relay-daemon/internal/config"
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

	// Admin pane mode (Addendum A) or legacy admin daemon
	var adminTimer *adminpane.AdminTimer
	var adminDaemon *admin.Admin
	var messageRouter *admin.MessageRouter

	if cfg.AdminEnabled {
		if adminPaneID, ok := cfg.PaneTargets["admin"]; ok {
			log.Printf("admin pane mode enabled (pane %s)", adminPaneID)
			adminTimer = adminpane.NewAdminTimer(injector, cfg, logger)

			// AdminDir defaults to ~/party/{project}/admin — use state dir parent as fallback
			adminDir := filepath.Join(filepath.Dir(cfg.StateDir), "admin")
			recycler := adminpane.NewRecycler(mux, cfg, logger, adminPaneID, adminDir)
			adminTimer.SetRecycler(recycler)
		} else {
			log.Printf("warning: RELAY_ADMIN_ENABLED=true but no 'admin' in pane map; falling back to legacy admin")
			cfg.AdminEnabled = false
		}
	}

	if !cfg.AdminEnabled {
		// Legacy admin daemon for checkpoint coordination
		adminCfg := admin.DefaultConfig()
		adminCfg.StateDir = cfg.StateDir
		if cfg.CheckpointIdleThreshold != nil {
			adminCfg.RelayIdleThreshold = *cfg.CheckpointIdleThreshold
		}
		if cfg.CheckpointLogStable != nil {
			adminCfg.SessionLogStableThreshold = *cfg.CheckpointLogStable
		}
		if cfg.CheckpointMinInterval != nil {
			adminCfg.MinCheckpointInterval = *cfg.CheckpointMinInterval
		}
		if cfg.CheckpointCooldown != nil {
			adminCfg.CooldownAfterCheckpoint = *cfg.CheckpointCooldown
		}
		if cfg.CheckpointACKTimeout != nil {
			adminCfg.ACKTimeout = *cfg.CheckpointACKTimeout
		}
		adminDaemon = admin.New(adminCfg, nil, logger, injector, nil)

		// Create message router for admin-destined messages
		messageRouter = admin.NewMessageRouter(adminDaemon, func(env *envelope.Envelope) error {
			return injector.Inject(env)
		})
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
	if cfg.AdminEnabled && adminTimer != nil {
		go adminTimer.Start(ctx)
	} else if adminDaemon != nil {
		go func() {
			if err := adminDaemon.Start(ctx); err != nil {
				log.Printf("admin daemon: %v", err)
			}
		}()
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

			// Record relay activity for checkpoint timing (legacy mode only)
			if adminDaemon != nil {
				adminDaemon.RecordRelayActivity()
			}

			// Handle broadcast to all agents
			if env.To == "all" {
				broadcastTargets := []string{"oc", "cc", "cx"}
				if cfg.AdminEnabled {
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

			// Admin pane mode: handle admin-destined messages directly
			if env.To == "admin" && cfg.AdminEnabled {
				// Check for ACK messages (admin skills output "ACK health-check..." etc.)
				if adminTimer != nil && strings.HasPrefix(strings.TrimSpace(env.Payload), "ACK ") {
					adminTimer.RecordACK()
					_ = logger.Log(logpkg.NewEvent("admin_ack_received", env.From, "admin").WithMsgID(env.MsgID))
				}
				// Forward directly to admin pane via injector
				if err := injector.Inject(env); err != nil {
					_ = logger.Log(logpkg.NewEvent("error", env.From, "admin").WithMsgID(env.MsgID).WithError(err.Error()))
				}
				continue
			}

			// Legacy mode: route message (admin-destined messages handled internally)
			if messageRouter != nil {
				handled, err := messageRouter.Route(env)
				if err != nil {
					_ = logger.Log(logpkg.NewEvent("error", env.From, env.To).WithMsgID(env.MsgID).WithError(err.Error()))
					continue
				}
				if handled {
					continue
				}
			}
		}
	}
}
