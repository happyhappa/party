package adminpane

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/norm/relay-daemon/internal/config"
	logpkg "github.com/norm/relay-daemon/internal/log"
)

// TmuxClient is the interface for tmux operations used by Recycler.
// Both *tmux.Tmux and test mocks implement this.
type TmuxClient interface {
	Run(args ...string) (string, error)
	SendToPane(pane, message string) error
}

// Recycler handles admin pane capture, shutdown, and relaunch (Prestige model).
type Recycler struct {
	tmux        TmuxClient
	cfg         *config.Config
	logger      *logpkg.EventLog
	adminPaneID string
	adminDir    string
}

// NewRecycler creates a new Recycler.
func NewRecycler(tmux TmuxClient, cfg *config.Config, logger *logpkg.EventLog, adminPaneID, adminDir string) *Recycler {
	return &Recycler{
		tmux:        tmux,
		cfg:         cfg,
		logger:      logger,
		adminPaneID: adminPaneID,
		adminDir:    adminDir,
	}
}

// NeedsRecycle returns true if the admin pane should be recycled based on
// cycle count or uptime thresholds.
func (r *Recycler) NeedsRecycle(cycles int, startTime time.Time) bool {
	return cycles >= r.cfg.AdminRecycleCycles || time.Since(startTime) >= r.cfg.AdminMaxUptime
}

// Recycle performs the Prestige recycle sequence:
// 1. Capture pane tail to last-life.txt
// 2. Inject /exit
// 3. Poll for shell prompt
// 4. Relaunch claude
func (r *Recycler) Recycle(ctx context.Context) error {
	log.Printf("admin recycle: starting (pane %s)", r.adminPaneID)

	// 1. Capture pane tail → last-life.txt
	if err := r.capturePaneTail(); err != nil {
		log.Printf("admin recycle: capture tail failed (continuing): %v", err)
		// Non-fatal — continue with recycle even if capture fails
	}

	// 2. Inject /exit via SendToPane (handles locking, Enter retry, wake)
	if err := r.tmux.SendToPane(r.adminPaneID, "/exit"); err != nil {
		return fmt.Errorf("inject /exit: %w", err)
	}

	// 3. Poll for shell prompt (reuse prompt detection patterns from injector)
	if err := r.waitForPrompt(ctx, 30*time.Second); err != nil {
		return fmt.Errorf("wait for prompt: %w", err)
	}

	// 4. Relaunch claude via SendToPane
	relaunchCmd := r.cfg.AdminRelaunchCmd
	if err := r.tmux.SendToPane(r.adminPaneID, relaunchCmd); err != nil {
		return fmt.Errorf("inject relaunch: %w", err)
	}

	log.Printf("admin recycle: complete")
	r.logEvent("admin_recycle_complete", "")
	return nil
}

func (r *Recycler) capturePaneTail() error {
	out, err := r.tmux.Run("capture-pane", "-t", r.adminPaneID, "-p", "-S", "-200")
	if err != nil {
		return fmt.Errorf("capture-pane: %w", err)
	}

	stateDir := filepath.Join(r.adminDir, "state")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return fmt.Errorf("mkdir state: %w", err)
	}

	path := filepath.Join(stateDir, "last-life.txt")
	if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
		return fmt.Errorf("write last-life.txt: %w", err)
	}

	log.Printf("admin recycle: captured pane tail to %s (%d bytes)", path, len(out))
	return nil
}

// promptPrefixes are the shell prompt markers to detect (same as injector.go IsPaneReady).
var promptPrefixes = []string{"❯", "›", "⏵", "?", "$", "%", ">"}

func (r *Recycler) waitForPrompt(ctx context.Context, timeout time.Duration) error {
	deadline := time.After(timeout)
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline:
			return fmt.Errorf("timeout after %v waiting for shell prompt", timeout)
		case <-ticker.C:
			if r.isPromptVisible() {
				return nil
			}
		}
	}
}

func (r *Recycler) isPromptVisible() bool {
	out, err := r.tmux.Run("capture-pane", "-t", r.adminPaneID, "-p", "-S", "-5")
	if err != nil {
		return false
	}

	last := lastNonEmptyLine(out)
	if last == "" {
		return false
	}

	trimmed := strings.TrimSpace(last)
	for _, prefix := range promptPrefixes {
		if strings.HasPrefix(trimmed, prefix) {
			return true
		}
	}
	return false
}

func lastNonEmptyLine(out string) string {
	lines := strings.Split(out, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line != "" {
			return line
		}
	}
	return ""
}

func (r *Recycler) logEvent(eventType, detail string) {
	if r.logger == nil {
		return
	}
	evt := logpkg.NewEvent(eventType, "relay", "admin")
	if detail != "" {
		evt = evt.WithError(detail)
	}
	_ = r.logger.Log(evt)
}
