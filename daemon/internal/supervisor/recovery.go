package supervisor

import (
	logpkg "github.com/norm/relay-daemon/internal/log"
	tmuxpkg "github.com/norm/relay-daemon/internal/tmux"
	"github.com/norm/relay-daemon/pkg/envelope"
)

// RecoveryHandler injects post-compact recovery messages.
type RecoveryHandler struct {
	injector *tmuxpkg.Injector
	logger   *logpkg.EventLog
}

func NewRecoveryHandler(injector *tmuxpkg.Injector, logger *logpkg.EventLog) *RecoveryHandler {
	return &RecoveryHandler{injector: injector, logger: logger}
}

// HandleWake injects a recovery message to the target.
func (r *RecoveryHandler) HandleWake(target string) error {
	message := "[RELAY] You just compacted. Run /rec to restore context."
	env := envelope.NewEnvelope("relay", target, "event", message)
	env.Ephemeral = true
	if err := r.injector.Inject(env); err != nil {
		_ = r.logger.Log(logpkg.Event{Kind: "error", MsgID: env.MsgID, Target: env.To, Error: err.Error()})
		return err
	}
	_ = r.logger.Log(logpkg.Event{Kind: "recovery", MsgID: env.MsgID, Target: env.To})
	return nil
}
