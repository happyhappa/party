package sessionmap

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// RoleSessionMapping maps a role to its session log information.
type RoleSessionMapping struct {
	Role           string         `json:"role"`
	WorktreePath   string         `json:"worktree_path"`
	SessionLogPath string         `json:"session_log_path"`
	SessionLogType SessionLogType `json:"session_log_type"`
	PaneID         string         `json:"pane_id,omitempty"`
	LastVerified   time.Time      `json:"last_verified"`
	LastModified   time.Time      `json:"last_modified,omitempty"`
	Valid          bool           `json:"valid"`
	Error          string         `json:"error,omitempty"`
}

// SessionMap holds the mapping of roles to session logs for a pod.
type SessionMap struct {
	PodName   string                        `json:"pod_name"`
	UpdatedAt time.Time                     `json:"updated_at"`
	Mappings  map[string]*RoleSessionMapping `json:"mappings"` // role -> mapping

	mu       sync.RWMutex
	stateDir string
}

// NewSessionMap creates a new session map for a pod.
func NewSessionMap(podName, stateDir string) *SessionMap {
	return &SessionMap{
		PodName:  podName,
		Mappings: make(map[string]*RoleSessionMapping),
		stateDir: stateDir,
	}
}

// SetMapping sets or updates the mapping for a role.
func (m *SessionMap) SetMapping(role string, mapping *RoleSessionMapping) {
	m.mu.Lock()
	defer m.mu.Unlock()

	mapping.Role = role
	mapping.LastVerified = time.Now()
	m.Mappings[role] = mapping
	m.UpdatedAt = time.Now()
}

// GetMapping returns the mapping for a role.
func (m *SessionMap) GetMapping(role string) (*RoleSessionMapping, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	mapping, ok := m.Mappings[role]
	return mapping, ok
}

// GetSessionLogPath returns the session log path for a role.
func (m *SessionMap) GetSessionLogPath(role string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	mapping, ok := m.Mappings[role]
	if !ok {
		return "", fmt.Errorf("no mapping for role: %s", role)
	}
	if !mapping.Valid {
		return "", fmt.Errorf("mapping invalid for role %s: %s", role, mapping.Error)
	}
	return mapping.SessionLogPath, nil
}

// GetAllSessionLogPaths returns all valid session log paths keyed by role.
func (m *SessionMap) GetAllSessionLogPaths() map[string]string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[string]string)
	for role, mapping := range m.Mappings {
		if mapping.Valid && mapping.SessionLogPath != "" {
			result[role] = mapping.SessionLogPath
		}
	}
	return result
}

// DiscoverAndUpdate discovers session logs for all configured worktrees and updates mappings.
func (m *SessionMap) DiscoverAndUpdate(worktrees map[string]string, panes map[string]string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for role, worktreePath := range worktrees {
		mapping := &RoleSessionMapping{
			Role:         role,
			WorktreePath: worktreePath,
			PaneID:       panes[role],
		}

		// Determine discovery method based on role
		var sessionPath string
		var err error

		switch role {
		case "oc", "cc":
			// Claude agents - use worktree path discovery
			sessionPath, err = DiscoverClaudeSessionLog(worktreePath)
			if err == nil {
				mapping.SessionLogType = SessionLogTypeClaude
			}

		case "cx":
			// Codex agent - try lsof first, then directory scan
			if panes[role] != "" {
				sessionPath, err = DiscoverCodexSessionLogByPaneID(panes[role])
			}
			if err != nil || sessionPath == "" {
				sessionPath, err = DiscoverCodexSessionLogByDir()
			}
			if err == nil {
				mapping.SessionLogType = SessionLogTypeCodex
			}

		default:
			// Unknown role - try Claude first, then Codex
			sessionPath, err = DiscoverClaudeSessionLog(worktreePath)
			if err != nil {
				sessionPath, err = DiscoverCodexSessionLogByDir()
			}
			if sessionPath != "" {
				mapping.SessionLogType = DetectSessionLogType(sessionPath)
			}
		}

		if err != nil {
			mapping.Valid = false
			mapping.Error = err.Error()
		} else {
			mapping.SessionLogPath = sessionPath
			mapping.Valid = true

			// Get last modified time
			if info, err := os.Stat(sessionPath); err == nil {
				mapping.LastModified = info.ModTime()
			}
		}

		mapping.LastVerified = time.Now()
		m.Mappings[role] = mapping
	}

	m.UpdatedAt = time.Now()
	return nil
}

// Verify checks that all mappings are still valid.
func (m *SessionMap) Verify() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for role, mapping := range m.Mappings {
		if mapping.SessionLogPath == "" {
			mapping.Valid = false
			mapping.Error = "no session log path"
			continue
		}

		err := ValidateSessionLog(mapping.SessionLogPath)
		if err != nil {
			mapping.Valid = false
			mapping.Error = err.Error()
		} else {
			mapping.Valid = true
			mapping.Error = ""

			// Update last modified time
			if info, err := os.Stat(mapping.SessionLogPath); err == nil {
				mapping.LastModified = info.ModTime()
			}
		}

		mapping.LastVerified = time.Now()
		m.Mappings[role] = mapping
	}

	m.UpdatedAt = time.Now()
}

// Save persists the session map to disk.
func (m *SessionMap) Save() error {
	if m.stateDir == "" {
		return nil
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	path := m.statePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}

	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	// Atomic write
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		return fmt.Errorf("write: %w", err)
	}

	return os.Rename(tmpPath, path)
}

// Load restores the session map from disk.
func (m *SessionMap) Load() error {
	if m.stateDir == "" {
		return nil
	}

	path := m.statePath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := json.Unmarshal(data, m); err != nil {
		return fmt.Errorf("unmarshal: %w", err)
	}

	return nil
}

func (m *SessionMap) statePath() string {
	filename := fmt.Sprintf("session-map-%s.json", m.PodName)
	return filepath.Join(m.stateDir, filename)
}

// LoadSessionMapFromFile loads a session map from a specific file path.
func LoadSessionMapFromFile(path string) (*SessionMap, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read session map: %w", err)
	}

	var m SessionMap
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("unmarshal session map: %w", err)
	}

	if m.Mappings == nil {
		m.Mappings = make(map[string]*RoleSessionMapping)
	}

	return &m, nil
}
