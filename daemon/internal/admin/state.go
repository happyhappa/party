package admin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// AdminState holds persisted admin state.
type AdminState struct {
	LastRelayActivity  time.Time            `json:"last_relay_activity"`
	LastLogGrowth      map[string]time.Time `json:"last_log_growth"`
	LastCheckpointTime map[string]time.Time `json:"last_checkpoint_time"`
	CooldownUntil      map[string]time.Time `json:"cooldown_until"`
}

// SaveState persists admin state to disk.
func (a *Admin) SaveState() error {
	if a.cfg.StateDir == "" {
		return nil
	}

	a.mu.Lock()
	state := AdminState{
		LastRelayActivity:  a.lastRelayActivity,
		LastLogGrowth:      a.lastLogGrowth,
		LastCheckpointTime: a.lastCheckpointTime,
		CooldownUntil:      a.cooldownUntil,
	}
	a.mu.Unlock()

	path := filepath.Join(a.cfg.StateDir, "admin-state.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}

	// Atomic write
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

// LoadState restores admin state from disk.
func (a *Admin) LoadState() error {
	if a.cfg.StateDir == "" {
		return nil
	}

	path := filepath.Join(a.cfg.StateDir, "admin-state.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var state AdminState
	if err := json.Unmarshal(data, &state); err != nil {
		return err
	}

	a.mu.Lock()
	a.lastRelayActivity = state.LastRelayActivity
	if state.LastLogGrowth != nil {
		a.lastLogGrowth = state.LastLogGrowth
	}
	if state.LastCheckpointTime != nil {
		a.lastCheckpointTime = state.LastCheckpointTime
	}
	if state.CooldownUntil != nil {
		a.cooldownUntil = state.CooldownUntil
	}
	a.mu.Unlock()

	return nil
}
