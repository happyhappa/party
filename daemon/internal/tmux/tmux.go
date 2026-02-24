package tmux

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// vimModeEnabled checks if RELAY_VIM_MODE is set to true.
// When enabled, sends Escape before Enter to exit vim INSERT mode.
// Default: false (no Escape sent).
func vimModeEnabled() bool {
	return strings.ToLower(os.Getenv("RELAY_VIM_MODE")) == "true"
}

var paneNudgeLocks sync.Map

// Tmux provides helpers for interacting with tmux.
type Tmux struct{}

func New() *Tmux {
	return &Tmux{}
}

func getNudgeLock(target string) *sync.Mutex {
	actual, _ := paneNudgeLocks.LoadOrStore(target, &sync.Mutex{})
	return actual.(*sync.Mutex)
}

// IsSessionAttached returns true if the session has any clients attached.
func (t *Tmux) IsSessionAttached(target string) bool {
	attached, err := t.run("display-message", "-t", target, "-p", "#{session_attached}")
	return err == nil && attached == "1"
}

// WakePane triggers a SIGWINCH in a pane by resizing it slightly then restoring.
func (t *Tmux) WakePane(target string) {
	_, _ = t.run("resize-pane", "-t", target, "-y", "-1")
	time.Sleep(50 * time.Millisecond)
	_, _ = t.run("resize-pane", "-t", target, "-y", "+1")
}

// WakePaneIfDetached triggers a SIGWINCH only if the session is detached.
func (t *Tmux) WakePaneIfDetached(target string) {
	if t.IsSessionAttached(target) {
		return
	}
	t.WakePane(target)
}

// SendToPane sends a message to a specific pane reliably.
func (t *Tmux) SendToPane(pane, message string) error {
	if pane == "" {
		return errors.New("tmux: empty pane target")
	}

	lock := getNudgeLock(pane)
	lock.Lock()
	defer lock.Unlock()

	if _, err := t.run("send-keys", "-t", pane, "-l", message); err != nil {
		return err
	}

	time.Sleep(2 * time.Second)

	// Only send Escape if vim mode is enabled (to exit INSERT mode)
	if vimModeEnabled() {
		_, _ = t.run("send-keys", "-t", pane, "Escape")
		time.Sleep(100 * time.Millisecond)
	}

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(200 * time.Millisecond)
		}
		if _, err := t.run("send-keys", "-t", pane, "Enter"); err != nil {
			lastErr = err
			continue
		}
		t.WakePaneIfDetached(pane)
		return nil
	}

	return fmt.Errorf("tmux: failed to send Enter after 3 attempts: %w", lastErr)
}

// GetPaneByTitle returns the pane ID matching the given title within a session.
func (t *Tmux) GetPaneByTitle(session, title string) (string, error) {
	if session == "" || title == "" {
		return "", errors.New("tmux: session and title required")
	}
	output, err := t.run("list-panes", "-t", session, "-F", "#{pane_title}:#{pane_id}")
	if err != nil {
		return "", err
	}
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		if parts[0] == title {
			return parts[1], nil
		}
	}
	return "", fmt.Errorf("tmux: pane with title %q not found", title)
}

// Run exposes a tmux command execution for other components.
func (t *Tmux) Run(args ...string) (string, error) {
	return t.run(args...)
}

// run executes a tmux command and returns trimmed output.
func (t *Tmux) run(args ...string) (string, error) {
	cmd := exec.Command("tmux", args...)
	out, err := cmd.CombinedOutput()
	output := strings.TrimSpace(string(out))
	if err != nil {
		return output, fmt.Errorf("tmux %v: %w", args, err)
	}
	return output, nil
}
