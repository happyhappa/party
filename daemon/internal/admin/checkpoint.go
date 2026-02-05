// Package admin implements the RFC-002 admin daemon for checkpoint coordination.
// This file handles checkpoint content messages and single-writer bead creation.
package admin

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	logpkg "github.com/norm/relay-daemon/internal/log"
)

// CheckpointContent represents checkpoint content sent from an agent to admin.
// Agents must send this instead of writing beads directly (single-writer enforcement).
type CheckpointContent struct {
	ChkID   string            `json:"chk_id"`   // Correlation ID from CHECKPOINT_REQUEST
	Role    string            `json:"role"`     // Agent role (oc, cc, cx)
	Content string            `json:"content"`  // Checkpoint markdown content
	Labels  map[string]string `json:"labels"`   // Additional labels for the bead
	Title   string            `json:"title"`    // Optional custom title
}

// CheckpointContentKind is the relay message kind for checkpoint content.
const CheckpointContentKind = "checkpoint_content"

// ParseCheckpointContent parses a checkpoint content message from JSON payload.
func ParseCheckpointContent(payload string) (*CheckpointContent, error) {
	var cc CheckpointContent
	if err := json.Unmarshal([]byte(payload), &cc); err != nil {
		return nil, fmt.Errorf("parse checkpoint content: %w", err)
	}

	if cc.ChkID == "" {
		return nil, fmt.Errorf("checkpoint content: missing chk_id")
	}
	if cc.Role == "" {
		return nil, fmt.Errorf("checkpoint content: missing role")
	}
	if cc.Content == "" {
		return nil, fmt.Errorf("checkpoint content: missing content")
	}

	return &cc, nil
}

// HandleCheckpointContent processes checkpoint content from an agent and writes the bead.
// This is the single-writer enforcement point - only admin writes beads.
func (a *Admin) HandleCheckpointContent(from string, payload string) {
	cc, err := ParseCheckpointContent(payload)
	if err != nil {
		a.logEvent("checkpoint_content_error", from, "admin", "", err.Error())
		return
	}

	// Verify the sender matches the role in the content
	if cc.Role != from {
		a.logEvent("checkpoint_content_error", from, "admin", cc.ChkID,
			fmt.Sprintf("role mismatch: from=%s content.role=%s", from, cc.Role))
		return
	}

	// Check if we have a pending request for this role
	a.mu.Lock()
	pending, hasPending := a.pendingRequests[cc.Role]
	a.mu.Unlock()

	// Reject unsolicited checkpoint content (no pending request)
	if !hasPending {
		a.logEvent("checkpoint_content_rejected", from, "admin", cc.ChkID,
			"no pending request for role")
		return
	}

	// Reject stale checkpoint content (wrong chk_id)
	if pending.ChkID != cc.ChkID {
		a.logEvent("checkpoint_content_rejected", from, "admin", cc.ChkID,
			fmt.Sprintf("chk_id mismatch: expected=%s got=%s", pending.ChkID, cc.ChkID))
		return
	}

	// Write the bead on behalf of the agent
	beadID, err := a.writeBeadForAgent(cc)
	if err != nil {
		a.logEvent("checkpoint_bead_error", from, "admin", cc.ChkID, err.Error())
		return
	}

	// Clear pending request and update state
	a.mu.Lock()
	delete(a.pendingRequests, cc.Role)
	a.lastCheckpointTime[cc.Role] = time.Now()
	a.cooldownUntil[cc.Role] = time.Now().Add(a.cfg.CooldownAfterCheckpoint)
	a.mu.Unlock()

	// Log success
	a.logEventWithChkID(logpkg.EventTypeCheckpointAck, cc.Role, "admin", cc.ChkID, "written:"+beadID, "")
}

// writeBeadForAgent writes a checkpoint bead on behalf of an agent.
func (a *Admin) writeBeadForAgent(cc *CheckpointContent) (string, error) {
	title := cc.Title
	if title == "" {
		title = fmt.Sprintf("%s checkpoint %s", cc.Role, time.Now().Format("2006-01-02 15:04"))
	}

	args := []string{
		"create",
		"--type", "recovery",
		"--title", title,
	}

	// Add standard labels
	args = append(args, "--label", "role:"+cc.Role)
	args = append(args, "--label", "chk_id:"+cc.ChkID)
	args = append(args, "--label", "source:agent")
	args = append(args, "--label", "confidence:high")
	args = append(args, "--label", "writer:admin")

	// Add custom labels from agent
	for k, v := range cc.Labels {
		// Skip labels we already set
		if k == "role" || k == "chk_id" || k == "source" || k == "confidence" || k == "writer" {
			continue
		}
		args = append(args, "--label", k+":"+v)
	}

	args = append(args, "--body", cc.Content)

	cmd := exec.Command("bd", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("bd create: %w: %s", err, string(output))
	}

	// Extract bead ID from output
	beadID := strings.Fields(string(output))
	if len(beadID) > 0 {
		return beadID[0], nil
	}

	return "unknown", nil
}

// FormatCheckpointContent creates a JSON payload for checkpoint content.
// This helper can be used by agents to format their checkpoint messages.
func FormatCheckpointContent(chkID, role, content, title string, labels map[string]string) (string, error) {
	cc := CheckpointContent{
		ChkID:   chkID,
		Role:    role,
		Content: content,
		Title:   title,
		Labels:  labels,
	}
	data, err := json.Marshal(cc)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
