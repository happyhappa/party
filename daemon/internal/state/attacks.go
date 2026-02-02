package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// AttackState captures minimal attack status plus events.
type AttackState struct {
	AttackID     string       `json:"attack_id"`
	Status       string       `json:"status"`
	LastUpdated  time.Time    `json:"last_updated"`
	CurrentPhase int          `json:"current_phase"`
	CurrentChunk int          `json:"current_chunk"`
	Events       []StateEvent `json:"events,omitempty"`
}

// StateEvent records state transitions or notable events.
type StateEvent struct {
	Timestamp time.Time `json:"ts"`
	Kind      string    `json:"kind"`
	Actor     string    `json:"actor"`
	Message   string    `json:"message,omitempty"`
}

// AttackWatcher loads and tracks attack files.
type AttackWatcher struct {
	attacksDir string
	mu         sync.RWMutex
	attacks    map[string]*AttackState
	paths      map[string]string
}

func NewAttackWatcher(attacksDir string) *AttackWatcher {
	return &AttackWatcher{
		attacksDir: attacksDir,
		attacks:    make(map[string]*AttackState),
		paths:      make(map[string]string),
	}
}

// Scan refreshes attack state from disk.
func (w *AttackWatcher) Scan() error {
	entries, err := os.ReadDir(w.attacksDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	attacks := make(map[string]*AttackState)
	paths := make(map[string]string)

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(w.attacksDir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var state AttackState
		if err := json.Unmarshal(data, &state); err != nil {
			return err
		}
		attackID := state.AttackID
		if attackID == "" {
			attackID = entry.Name()[:len(entry.Name())-len(filepath.Ext(entry.Name()))]
			state.AttackID = attackID
		}
		attacks[attackID] = &state
		paths[attackID] = path
	}

	w.mu.Lock()
	w.attacks = attacks
	w.paths = paths
	w.mu.Unlock()
	return nil
}

// Get returns a copy of the attack state.
func (w *AttackWatcher) Get(attackID string) *AttackState {
	w.mu.RLock()
	state := w.attacks[attackID]
	if state == nil {
		w.mu.RUnlock()
		return nil
	}
	copy := *state
	w.mu.RUnlock()
	return &copy
}

// OpenAttacks returns a snapshot of tracked attacks.
func (w *AttackWatcher) OpenAttacks() []*AttackState {
	w.mu.RLock()
	out := make([]*AttackState, 0, len(w.attacks))
	for _, state := range w.attacks {
		copy := *state
		out = append(out, &copy)
	}
	w.mu.RUnlock()
	return out
}

// IsStale returns true if the attack has not been updated recently.
func (w *AttackWatcher) IsStale(attack *AttackState, threshold time.Duration) bool {
	if attack == nil {
		return false
	}
	return time.Since(attack.LastUpdated) > threshold
}

// AppendEvent appends a state event and writes to disk.
func (w *AttackWatcher) AppendEvent(attackID string, event StateEvent) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	state := w.attacks[attackID]
	path := w.paths[attackID]
	if path == "" {
		path = filepath.Join(w.attacksDir, attackID+".json")
	}
	if state == nil {
		state = &AttackState{AttackID: attackID}
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	state.Events = append(state.Events, event)
	state.LastUpdated = time.Now().UTC()

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return err
	}

	w.attacks[attackID] = state
	w.paths[attackID] = path
	return nil
}
