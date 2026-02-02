package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Config holds relay daemon configuration.
type Config struct {
	ShareDir          string
	InboxDir          string
	LogDir            string
	StateDir          string
	AttacksDir        string
	StuckThreshold    time.Duration
	NagInterval       time.Duration
	MaxNagDuration    time.Duration
	TmuxSession       string
	PaneMapPath       string
	PaneTargets       map[string]string
	PromptGating      string
	QueueMaxAge       time.Duration
	PaneTailEnabled   bool
	PaneTailInterval  time.Duration
	PaneTailLines     int
	PaneTailRotations int
	PaneTailDir       string
}

// Default returns the default configuration.
func Default() *Config {
	home, _ := os.UserHomeDir()
	shareDir := filepath.Join(home, "llm-share")
	return &Config{
		ShareDir:          shareDir,
		InboxDir:          filepath.Join(shareDir, "relay", "outbox"),
		LogDir:            filepath.Join(shareDir, "relay", "log"),
		StateDir:          filepath.Join(shareDir, "relay", "state"),
		AttacksDir:        filepath.Join(shareDir, "attacks"),
		StuckThreshold:    5 * time.Minute,
		NagInterval:       5 * time.Minute,
		MaxNagDuration:    30 * time.Minute,
		TmuxSession:       "party",
		PaneMapPath:       filepath.Join(shareDir, "relay", "state", "panes.json"),
		PaneTargets:       map[string]string{},
		PromptGating:      "all",
		QueueMaxAge:       5 * time.Minute,
		PaneTailEnabled:   false,
		PaneTailInterval:  30 * time.Second,
		PaneTailLines:     150,
		PaneTailRotations: 7,
		PaneTailDir:       filepath.Join(shareDir, "relay", "pane-tails"),
	}
}

// Load returns configuration loaded from environment variables or defaults.
func Load() (*Config, error) {
	cfg := Default()
	overrideString(&cfg.ShareDir, "RELAY_SHARE_DIR")
	cfg.InboxDir = envOr(cfg.InboxDir, "RELAY_INBOX_DIR")
	cfg.LogDir = envOr(cfg.LogDir, "RELAY_LOG_DIR")
	cfg.StateDir = envOr(cfg.StateDir, "RELAY_STATE_DIR")
	cfg.AttacksDir = envOr(cfg.AttacksDir, "RELAY_ATTACKS_DIR")
	cfg.TmuxSession = envOr(cfg.TmuxSession, "RELAY_TMUX_SESSION")
	cfg.PaneMapPath = envOr(cfg.PaneMapPath, "RELAY_PANE_MAP")
	overrideBool(&cfg.PaneTailEnabled, "RELAY_PANE_TAIL_ENABLED")
	overrideDuration(&cfg.PaneTailInterval, "RELAY_PANE_TAIL_INTERVAL")
	overrideInt(&cfg.PaneTailLines, "RELAY_PANE_TAIL_LINES")
	overrideInt(&cfg.PaneTailRotations, "RELAY_PANE_TAIL_ROTATIONS")
	cfg.PaneTailDir = envOr(cfg.PaneTailDir, "RELAY_PANE_TAIL_DIR")

	overrideDuration(&cfg.StuckThreshold, "RELAY_STUCK_THRESHOLD")
	overrideDuration(&cfg.NagInterval, "RELAY_NAG_INTERVAL")
	overrideDuration(&cfg.MaxNagDuration, "RELAY_MAX_NAG_DURATION")

	cfg.PromptGating = envOr(cfg.PromptGating, "RELAY_PROMPT_GATING")
	overrideDuration(&cfg.QueueMaxAge, "RELAY_QUEUE_MAX_AGE")

	return cfg, nil
}

// LoadPaneMap loads pane targets from PaneMapPath into PaneTargets.
func (c *Config) LoadPaneMap() error {
	raw, err := os.ReadFile(c.PaneMapPath)
	if err != nil {
		return err
	}

	var paneMap map[string]string
	if err := json.Unmarshal(raw, &paneMap); err != nil {
		return fmt.Errorf("decode pane map: %w", err)
	}

	c.PaneTargets = make(map[string]string, len(paneMap))
	for key, val := range paneMap {
		c.PaneTargets[strings.ToLower(key)] = val
	}
	return nil
}

func envOr(current, key string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return current
}

func overrideString(dest *string, key string) {
	if val := os.Getenv(key); val != "" {
		*dest = val
	}
}

func overrideDuration(dest *time.Duration, key string) {
	if val := os.Getenv(key); val != "" {
		if parsed, err := time.ParseDuration(val); err == nil {
			*dest = parsed
		}
	}
}

func overrideBool(dest *bool, key string) {
	if val := os.Getenv(key); val != "" {
		switch strings.ToLower(val) {
		case "1", "true", "yes", "y", "on":
			*dest = true
		case "0", "false", "no", "n", "off":
			*dest = false
		}
	}
}

func overrideInt(dest *int, key string) {
	if val := os.Getenv(key); val != "" {
		if parsed, err := strconv.Atoi(val); err == nil {
			*dest = parsed
		}
	}
}
