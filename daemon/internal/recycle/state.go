package recycle

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// State represents a role's position in the recycle lifecycle.
type State string

const (
	StateReady       State = "ready"
	StateExiting     State = "exiting"
	StateConfirming  State = "confirming"
	StateRelaunching State = "relaunching"
	StateHydrating   State = "hydrating"
	StateDegraded    State = "degraded"
	StateFailed      State = "failed"
)

// RecycleState is the durable per-role state file at $RELAY_STATE_DIR/recycle-{role}.json.
type RecycleState struct {
	State           State     `json:"state"`
	EnteredAt       time.Time `json:"entered_at"`
	AgentPID        int       `json:"agent_pid"`
	SessionID       string    `json:"session_id"`
	TranscriptPath  string    `json:"transcript_path"`
	LastBriefOffset int64     `json:"last_brief_offset"`
	LastBriefAt     time.Time `json:"last_brief_at,omitempty"`
	RecycleReason   string    `json:"recycle_reason,omitempty"`
	Error           string    `json:"error,omitempty"`
	RelayAvailable  bool      `json:"relay_available"`
	FailureCount    int       `json:"failure_count"`
}

// validTransitions defines the allowed state transitions.
var validTransitions = map[State][]State{
	StateReady:       {StateExiting},
	StateExiting:     {StateConfirming, StateDegraded, StateFailed},
	StateConfirming:  {StateRelaunching, StateDegraded, StateFailed},
	StateRelaunching: {StateHydrating, StateDegraded, StateFailed},
	StateHydrating:   {StateReady, StateDegraded, StateFailed},
	StateDegraded:    {StateRelaunching, StateFailed},
	StateFailed:      {StateRelaunching}, // manual reset via --force
}

// StatePath returns the filesystem path for a role's recycle state file.
func StatePath(stateDir, role string) string {
	return filepath.Join(stateDir, fmt.Sprintf("recycle-%s.json", role))
}

// LockPath returns the filesystem path for a role's recycle lock file.
func LockPath(stateDir, role string) string {
	return filepath.Join(stateDir, fmt.Sprintf("recycle-%s.lock", role))
}

// LoadState reads a role's recycle state from disk.
// If the file does not exist, returns a default "ready" state.
func LoadState(stateDir, role string) (*RecycleState, error) {
	path := StatePath(stateDir, role)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &RecycleState{
				State:     StateReady,
				EnteredAt: time.Now().UTC(),
			}, nil
		}
		return nil, fmt.Errorf("read recycle state: %w", err)
	}
	var s RecycleState
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("decode recycle state: %w", err)
	}
	return &s, nil
}

// Save writes the recycle state to disk atomically.
func (s *RecycleState) Save(stateDir, role string) error {
	path := StatePath(stateDir, role)
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal recycle state: %w", err)
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(stateDir, ".recycle-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp state file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp state: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp state: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename state file: %w", err)
	}
	return nil
}

// Transition moves the state to a new value, validating the transition is legal.
func (s *RecycleState) Transition(to State) error {
	allowed, ok := validTransitions[s.State]
	if !ok {
		return fmt.Errorf("no transitions defined from state %q", s.State)
	}
	for _, a := range allowed {
		if a == to {
			s.State = to
			s.EnteredAt = time.Now().UTC()
			if to != StateDegraded && to != StateFailed {
				s.Error = ""
			}
			if to == StateReady {
				s.FailureCount = 0
				s.RecycleReason = ""
			}
			return nil
		}
	}
	return fmt.Errorf("invalid transition: %s → %s", s.State, to)
}

// TransitionDegraded moves to degraded state, recording the error.
func (s *RecycleState) TransitionDegraded(reason string) error {
	if err := s.Transition(StateDegraded); err != nil {
		return err
	}
	s.FailureCount++
	s.Error = reason
	return nil
}

// TransitionFailed moves to failed state when retry budget is exhausted.
func (s *RecycleState) TransitionFailed(reason string) error {
	if err := s.Transition(StateFailed); err != nil {
		return err
	}
	s.Error = reason
	return nil
}

// StateLock provides exclusive access to a role's recycle state via flock.
type StateLock struct {
	file *os.File
}

// AcquireLock takes an exclusive flock on the role's lock file.
// Returns ErrLockBusy if another process holds the lock.
func AcquireLock(stateDir, role string) (*StateLock, error) {
	path := LockPath(stateDir, role)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("acquire lock for %s: %w (another process holds it)", role, err)
	}
	return &StateLock{file: f}, nil
}

// Release releases the flock and closes the file.
func (l *StateLock) Release() error {
	if l.file == nil {
		return nil
	}
	syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	return l.file.Close()
}
