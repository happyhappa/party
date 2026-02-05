package supervisor

import (
	"fmt"
	"sync"
	"time"

	logpkg "github.com/norm/relay-daemon/internal/log"
	"github.com/norm/relay-daemon/internal/state"
	tmuxpkg "github.com/norm/relay-daemon/internal/tmux"
	"github.com/norm/relay-daemon/pkg/envelope"
)

// Nagger checks for stale attacks and sends reminders.
type Nagger struct {
	attacks        *state.AttackWatcher
	injector       *tmuxpkg.Injector
	logger         *logpkg.EventLog
	stuckThreshold time.Duration
	nagInterval    time.Duration
	maxNagDuration time.Duration

	mu           sync.Mutex
	nagStartTime map[string]time.Time
	lastNagTime  map[string]time.Time
}

func NewNagger(attacks *state.AttackWatcher, injector *tmuxpkg.Injector, logger *logpkg.EventLog, stuckThreshold, nagInterval, maxNagDuration time.Duration) *Nagger {
	return &Nagger{
		attacks:        attacks,
		injector:       injector,
		logger:         logger,
		stuckThreshold: stuckThreshold,
		nagInterval:    nagInterval,
		maxNagDuration: maxNagDuration,
		nagStartTime:   make(map[string]time.Time),
		lastNagTime:    make(map[string]time.Time),
	}
}

func (n *Nagger) Check() error {
	attacks := n.attacks.OpenAttacks()
	now := time.Now().UTC()

	for _, attack := range attacks {
		if attack == nil {
			continue
		}

		if isClosedStatus(attack.Status) {
			n.clearNagState(attack.AttackID)
			continue
		}

		if attack.Status != "open" {
			// Only nag open attacks in 3a.
			continue
		}

		if !n.attacks.IsStale(attack, n.stuckThreshold) {
			n.clearNagState(attack.AttackID)
			continue
		}

		start := n.ensureNagStart(attack.AttackID, now)
		if now.Sub(start) >= n.maxNagDuration {
			n.clearNagState(attack.AttackID)
			_ = n.logger.Log(logpkg.NewEvent("nag_giveup", "relay", "oc").WithMsgID(attack.AttackID))
			_ = n.attacks.AppendEvent(attack.AttackID, state.StateEvent{
				Kind:    "nag_giveup",
				Actor:   "relay",
				Message: "nagging stopped after max duration",
			})
			continue
		}

		lastNag := n.lastNag(attack.AttackID)
		if !lastNag.IsZero() && now.Sub(lastNag) < n.nagInterval {
			continue
		}

		minutes := int(now.Sub(attack.LastUpdated).Minutes())
		message := fmt.Sprintf("[RELAY] Attack %s appears stalled. Last update %d min ago.", attack.AttackID, minutes)
		env := envelope.NewEnvelope("relay", "oc", "nag", message)
		env.Priority = 0
		env.ThreadID = attack.AttackID
		env.Ephemeral = true

		if err := n.injector.Inject(env); err != nil {
			_ = n.logger.Log(logpkg.NewEvent("error", env.From, env.To).WithMsgID(env.MsgID).WithError(err.Error()))
			continue
		}

		n.recordNag(attack.AttackID, now)
		_ = n.logger.Log(logpkg.NewEvent("nag", env.From, env.To).WithMsgID(env.MsgID))
		_ = n.attacks.AppendEvent(attack.AttackID, state.StateEvent{
			Kind:    "nag_sent",
			Actor:   "relay",
			Message: message,
		})
	}

	return nil
}

func (n *Nagger) ensureNagStart(attackID string, now time.Time) time.Time {
	n.mu.Lock()
	defer n.mu.Unlock()

	start, ok := n.nagStartTime[attackID]
	if !ok {
		start = now
		n.nagStartTime[attackID] = start
	}
	return start
}

func (n *Nagger) lastNag(attackID string) time.Time {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.lastNagTime[attackID]
}

func (n *Nagger) recordNag(attackID string, now time.Time) {
	n.mu.Lock()
	n.lastNagTime[attackID] = now
	n.mu.Unlock()
}

func (n *Nagger) clearNagState(attackID string) {
	n.mu.Lock()
	delete(n.nagStartTime, attackID)
	delete(n.lastNagTime, attackID)
	n.mu.Unlock()
}

func isClosedStatus(status string) bool {
	switch status {
	case "complete", "aborted", "closed", "done", "stopped":
		return true
	default:
		return false
	}
}
