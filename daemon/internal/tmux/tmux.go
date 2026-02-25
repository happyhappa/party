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

// Tmux provides helpers for interacting with tmux.
type Tmux struct{}

func New() *Tmux {
	return &Tmux{}
}

var paneSendLocks sync.Map

func getSendLock(target string) *sync.Mutex {
	actual, _ := paneSendLocks.LoadOrStore(target, &sync.Mutex{})
	return actual.(*sync.Mutex)
}

// SendToPane sends a message to a specific pane reliably.
func (t *Tmux) SendToPane(pane, message string) error {
	if pane == "" {
		return errors.New("tmux: empty pane target")
	}

	lock := getSendLock(pane)
	lock.Lock()
	defer lock.Unlock()

	if err := t.loadBuffer("relay-msg", message); err != nil {
		return err
	}

	if _, err := t.run("paste-buffer", "-b", "relay-msg", "-t", pane, "-d"); err != nil {
		return err
	}

	time.Sleep(1 * time.Second)

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

// loadBuffer writes content to a tmux buffer via stdin.
func (t *Tmux) loadBuffer(bufferName, content string) error {
	cmd := exec.Command("tmux", "load-buffer", "-b", bufferName, "-")
	cmd.Stdin = strings.NewReader(content)
	out, err := cmd.CombinedOutput()
	output := strings.TrimSpace(string(out))
	if err != nil {
		return fmt.Errorf("tmux [load-buffer -b %s -]: %w (%s)", bufferName, err, output)
	}
	return nil
}
