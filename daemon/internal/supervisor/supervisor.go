package supervisor

import (
	"context"
	"log"
	"time"

	"github.com/norm/relay-daemon/internal/state"
)

// Supervisor coordinates background checks.
type Supervisor struct {
	agents   *state.AgentTracker
	attacks  *state.AttackWatcher
	nagger   *Nagger
	recovery *RecoveryHandler
	interval time.Duration
}

func NewSupervisor(agents *state.AgentTracker, attacks *state.AttackWatcher, nagger *Nagger, recovery *RecoveryHandler, interval time.Duration) *Supervisor {
	return &Supervisor{
		agents:   agents,
		attacks:  attacks,
		nagger:   nagger,
		recovery: recovery,
		interval: interval,
	}
}

func (s *Supervisor) Start(ctx context.Context) error {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := s.attacks.Scan(); err != nil {
				log.Printf("supervisor scan error: %v", err)
			}
			if err := s.nagger.Check(); err != nil {
				log.Printf("supervisor nagger error: %v", err)
			}
			_ = s.agents // reserved for 3b
			_ = s.recovery
		}
	}
}
