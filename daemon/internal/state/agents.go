package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// AgentState represents an agent lifecycle state.
type AgentState string

const (
	AgentIdle     AgentState = "idle"
	AgentSpawning AgentState = "spawning"
	AgentRunning  AgentState = "running"
	AgentWorking  AgentState = "working"
	AgentStuck    AgentState = "stuck"
	AgentDone     AgentState = "done"
	AgentStopped  AgentState = "stopped"
	AgentDead     AgentState = "dead"
)

// AgentInfo holds state and last activity timestamp.
type AgentInfo struct {
	State    AgentState `json:"state"`
	LastSeen time.Time  `json:"last_seen"`
}

// AgentTracker persists and serves agent states.
type AgentTracker struct {
	statePath string
	mu        sync.RWMutex
	agents    map[string]*AgentInfo
}

func NewAgentTracker(stateDir string) *AgentTracker {
	return &AgentTracker{
		statePath: filepath.Join(stateDir, "agents.json"),
		agents:    make(map[string]*AgentInfo),
	}
}

func (t *AgentTracker) Update(agentID string, state AgentState) error {
	t.mu.Lock()
	info, ok := t.agents[agentID]
	if !ok {
		info = &AgentInfo{}
		t.agents[agentID] = info
	}
	info.State = state
	info.LastSeen = time.Now().UTC()
	t.mu.Unlock()

	return t.Save()
}

func (t *AgentTracker) Get(agentID string) *AgentInfo {
	t.mu.RLock()
	info := t.agents[agentID]
	if info == nil {
		t.mu.RUnlock()
		return nil
	}
	copy := *info
	t.mu.RUnlock()
	return &copy
}

func (t *AgentTracker) MarkActivity(agentID string) {
	t.mu.Lock()
	info, ok := t.agents[agentID]
	if !ok {
		info = &AgentInfo{State: AgentRunning}
		t.agents[agentID] = info
	}
	info.LastSeen = time.Now().UTC()
	t.mu.Unlock()

	_ = t.Save()
}

func (t *AgentTracker) Save() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	data, err := json.MarshalIndent(t.agents, "", "  ")
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(t.statePath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(t.statePath, data, 0o644)
}

func (t *AgentTracker) Load() error {
	data, err := os.ReadFile(t.statePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var agents map[string]*AgentInfo
	if err := json.Unmarshal(data, &agents); err != nil {
		return err
	}

	t.mu.Lock()
	if agents == nil {
		agents = make(map[string]*AgentInfo)
	}
	t.agents = agents
	t.mu.Unlock()
	return nil
}
