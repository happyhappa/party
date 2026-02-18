package checkpoint

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Content is the JSON payload sent by relay checkpoint.
type Content struct {
	ChkID   string            `json:"chk_id"`
	Role    string            `json:"role"`
	Content string            `json:"content"`
	Labels  map[string]string `json:"labels"`
	Title   string            `json:"title"`
}

// Parse decodes and validates checkpoint content payload.
func Parse(payload string) (*Content, error) {
	var c Content
	if err := json.Unmarshal([]byte(payload), &c); err != nil {
		return nil, fmt.Errorf("parse checkpoint content: %w", err)
	}
	if c.ChkID == "" {
		return nil, fmt.Errorf("checkpoint content: missing chk_id")
	}
	if c.Role == "" {
		return nil, fmt.Errorf("checkpoint content: missing role")
	}
	if c.Content == "" {
		return nil, fmt.Errorf("checkpoint content: missing content")
	}
	if c.Labels == nil {
		c.Labels = map[string]string{}
	}
	return &c, nil
}

// WriteBead writes checkpoint content to a bead via bd CLI.
func WriteBead(c *Content) (string, error) {
	title := c.Title
	if title == "" {
		title = fmt.Sprintf("%s checkpoint %s", c.Role, time.Now().Format("2006-01-02 15:04"))
	}

	args := []string{"create", "--type", "recovery", "--title", title}
	args = append(args, "--label", "role:"+c.Role)
	args = append(args, "--label", "chk_id:"+c.ChkID)
	args = append(args, "--label", "source:agent")
	args = append(args, "--label", "confidence:high")
	args = append(args, "--label", "writer:relay")

	for k, v := range c.Labels {
		if k == "role" || k == "chk_id" || k == "source" || k == "confidence" || k == "writer" {
			continue
		}
		args = append(args, "--label", k+":"+v)
	}

	args = append(args, "--body", c.Content)
	cmd := exec.Command("bd", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("bd create: %w: %s", err, strings.TrimSpace(string(out)))
	}

	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return "unknown", nil
	}
	return fields[0], nil
}
