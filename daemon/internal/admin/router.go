package admin

import (
	"github.com/norm/relay-daemon/pkg/envelope"
)

// MessageRouter routes messages to appropriate handlers.
// Messages to "admin" are handled internally, others are forwarded.
type MessageRouter struct {
	admin    *Admin
	forward  func(*envelope.Envelope) error
}

// NewMessageRouter creates a new message router.
// The forward function is called for messages not handled by admin.
func NewMessageRouter(admin *Admin, forward func(*envelope.Envelope) error) *MessageRouter {
	return &MessageRouter{
		admin:   admin,
		forward: forward,
	}
}

// Route processes a message, either handling it locally or forwarding.
// Returns true if the message was handled locally (no forwarding needed).
func (r *MessageRouter) Route(env *envelope.Envelope) (bool, error) {
	if env == nil {
		return false, nil
	}

	// Handle admin-destined messages
	if env.To == "admin" {
		r.handleAdminMessage(env)
		return true, nil
	}

	// Forward to injector
	if r.forward != nil {
		return false, r.forward(env)
	}

	return false, nil
}

// handleAdminMessage processes messages sent to admin.
func (r *MessageRouter) handleAdminMessage(env *envelope.Envelope) {
	if r.admin == nil {
		return
	}

	switch env.Kind {
	case CheckpointContentKind:
		// Agent sending checkpoint content for single-writer enforcement
		r.admin.HandleCheckpointContent(env.From, env.Payload)

	case "checkpoint_ack":
		// Legacy ACK handling (backward compatibility)
		// Parse chk_id and status from payload
		chkID, status := parseCheckpointACK(env.Payload)
		r.admin.HandleCheckpointACK(env.From, chkID, status)

	default:
		// Unknown kind - log but don't fail
		r.admin.logEvent("admin_unknown_kind", env.From, "admin", "", "kind="+env.Kind)
	}

	// Record activity for checkpoint trigger timing
	r.admin.RecordRelayActivity()
}

// parseCheckpointACK extracts chk_id and status from an ACK payload.
// Expected format: "chk_id=xxx status=yyy" or just "chk_id=xxx"
func parseCheckpointACK(payload string) (chkID, status string) {
	// Simple parsing - look for chk_id= and status=
	fields := make(map[string]string)
	for _, part := range splitFields(payload) {
		if idx := indexOf(part, '='); idx > 0 {
			key := part[:idx]
			val := part[idx+1:]
			fields[key] = val
		}
	}

	return fields["chk_id"], fields["status"]
}

// splitFields splits on whitespace.
func splitFields(s string) []string {
	var result []string
	var current []byte
	for i := 0; i < len(s); i++ {
		if s[i] == ' ' || s[i] == '\t' || s[i] == '\n' {
			if len(current) > 0 {
				result = append(result, string(current))
				current = current[:0]
			}
		} else {
			current = append(current, s[i])
		}
	}
	if len(current) > 0 {
		result = append(result, string(current))
	}
	return result
}

// indexOf returns the index of c in s, or -1 if not found.
func indexOf(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}
