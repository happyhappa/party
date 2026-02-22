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
	ShareDir            string
	InboxDir            string
	LogDir              string
	StateDir            string
	AttacksDir          string
	StuckThreshold      time.Duration
	NagInterval         time.Duration
	MaxNagDuration      time.Duration
	TmuxSession         string
	PaneMapPath         string
	PaneTargets         map[string]string
	PromptGating        string
	QueueMaxAge         time.Duration
	PaneTailEnabled     bool
	PaneTailInterval    time.Duration
	PaneTailLines       int
	PaneTailRotations   int
	PaneTailDir         string
	PaneMapVersion      int
	PaneMapRegisteredAt string
}

// Default returns the default configuration.
func Default() *Config {
	home, _ := os.UserHomeDir()
	shareDir := filepath.Join(home, "llm-share")
	return &Config{
		ShareDir:          shareDir,
		InboxDir:          filepath.Join(home, ".local", "share", "relay", "outbox"),
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

// paneMapV2 represents the new nested pane map format with metadata.
type paneMapV2 struct {
	Panes        map[string]string `json:"panes"`
	Version      int               `json:"version"`
	RegisteredAt string            `json:"registered_at"`
}

// LoadPaneMap loads pane targets from PaneMapPath into PaneTargets.
// Supports both the new nested format (with "panes", "version", "registered_at")
// and the old flat format ({"oc":"%0","cc":"%1","cx":"%2"}) for backward compat.
func (c *Config) LoadPaneMap() error {
	raw, err := os.ReadFile(c.PaneMapPath)
	if err != nil {
		return err
	}

	// Try new nested format first
	var v2 paneMapV2
	if err := json.Unmarshal(raw, &v2); err == nil && v2.Panes != nil {
		c.PaneTargets = make(map[string]string, len(v2.Panes))
		for key, val := range v2.Panes {
			c.PaneTargets[strings.ToLower(key)] = val
		}
		c.PaneMapVersion = v2.Version
		c.PaneMapRegisteredAt = v2.RegisteredAt
		return nil
	}

	// Fall back to flat format
	var flat map[string]string
	if err := json.Unmarshal(raw, &flat); err != nil {
		return fmt.Errorf("decode pane map: %w", err)
	}

	c.PaneTargets = make(map[string]string, len(flat))
	for key, val := range flat {
		c.PaneTargets[strings.ToLower(key)] = val
	}
	c.PaneMapVersion = 0
	c.PaneMapRegisteredAt = ""
	return nil
}

// IsPaneMapStale returns true if the pane map's registered_at timestamp
// is before lastRecycleTime, indicating stale pane mappings.
func (c *Config) IsPaneMapStale(lastRecycleTime time.Time) bool {
	if c.PaneMapRegisteredAt == "" {
		return true
	}
	registered, err := time.Parse(time.RFC3339, c.PaneMapRegisteredAt)
	if err != nil {
		return true
	}
	return registered.Before(lastRecycleTime)
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
