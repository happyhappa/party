package recycle

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	// DefaultLogTailBytes is the default number of bytes to read from the tail of the JSONL log.
	DefaultLogTailBytes int64 = 8 * 1024
)

// HydrationOptions configures Tier 1 hydration assembly.
type HydrationOptions struct {
	Role           string
	PrevSessionID  string
	TranscriptPath string
	BeadsDir       string
	LogTailBytes   int64  // bytes from end of log to include (default 8KB)
	InboxDir       string // check for pending messages
}

// HydrationPayload is the assembled data for injecting into a fresh agent.
type HydrationPayload struct {
	Role           string `json:"role"`
	PrevSessionID  string `json:"prev_session_id"`
	TranscriptPath string `json:"transcript_path"`
	LogTail        string `json:"log_tail"`
	Brief          string `json:"brief,omitempty"`
	InboxNotice    string `json:"inbox_notice"`
}

// AssembleHydration builds a Tier 1 hydration payload from disk artifacts.
// This works without model generation — the brief is an enhancement if available.
func AssembleHydration(opts HydrationOptions) (*HydrationPayload, error) {
	payload := &HydrationPayload{
		Role:           opts.Role,
		PrevSessionID:  opts.PrevSessionID,
		TranscriptPath: opts.TranscriptPath,
	}

	// Read log tail (minimum viable hydration)
	tailBytes := opts.LogTailBytes
	if tailBytes <= 0 {
		tailBytes = DefaultLogTailBytes
	}
	tail, err := readTail(opts.TranscriptPath, tailBytes)
	if err != nil {
		// Log tail failure is non-fatal — we can hydrate with just metadata
		payload.LogTail = fmt.Sprintf("(log tail unavailable: %v)", err)
	} else {
		payload.LogTail = tail
	}

	// Look for latest continuous brief (enhancement)
	brief, err := findLatestBrief(opts.BeadsDir, opts.Role, opts.PrevSessionID)
	if err == nil && brief != "" {
		payload.Brief = brief
	}

	// Check for pending inbox messages
	payload.InboxNotice = checkInbox(opts.InboxDir, opts.Role)

	return payload, nil
}

// readTail reads the last n bytes from a file.
func readTail(path string, n int64) (string, error) {
	if path == "" {
		return "", fmt.Errorf("no transcript path")
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return "", err
	}

	start := info.Size() - n
	if start < 0 {
		start = 0
	}

	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return "", err
	}

	data := make([]byte, info.Size()-start)
	nRead, err := io.ReadFull(f, data)
	if err != nil && err != io.ErrUnexpectedEOF {
		return "", err
	}

	return string(data[:nRead]), nil
}

// findLatestBrief scans beads for the most recent continuous brief matching
// the role and session ID.
func findLatestBrief(beadsDir, role, sessionID string) (string, error) {
	if beadsDir == "" {
		return "", fmt.Errorf("no beads dir")
	}

	pattern := filepath.Join(beadsDir, "*.json")
	files, err := filepath.Glob(pattern)
	if err != nil {
		return "", err
	}

	type candidate struct {
		path    string
		modTime int64
	}
	var matches []candidate

	roleLabel := fmt.Sprintf("role:%s", role)
	sessionLabel := fmt.Sprintf("session_id:%s", sessionID)

	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		content := string(data)
		if !strings.Contains(content, "kind:session_brief") {
			continue
		}
		if !strings.Contains(content, roleLabel) {
			continue
		}
		// Validate session_id if provided
		if sessionID != "" && !strings.Contains(content, sessionLabel) {
			continue
		}
		info, err := os.Stat(f)
		if err != nil {
			continue
		}
		matches = append(matches, candidate{path: f, modTime: info.ModTime().UnixNano()})
	}

	if len(matches) == 0 {
		return "", fmt.Errorf("no briefs found for role %s", role)
	}

	// Sort newest first
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].modTime > matches[j].modTime
	})

	data, err := os.ReadFile(matches[0].path)
	if err != nil {
		return "", err
	}

	// Extract brief content from bead (body section)
	return extractBeadBody(string(data)), nil
}

// extractBeadBody extracts the body content from a bead JSON file.
// Beads store content in a "body" or "description" field.
func extractBeadBody(content string) string {
	// Simple extraction: look for body content between markers
	// Bead format varies; return the full content as fallback
	return content
}

// checkInbox reports whether there are pending messages in the role's inbox.
func checkInbox(inboxDir, role string) string {
	if inboxDir == "" {
		return ""
	}
	roleInbox := filepath.Join(inboxDir, role)
	entries, err := os.ReadDir(roleInbox)
	if err != nil {
		return ""
	}
	count := 0
	for _, e := range entries {
		if !e.IsDir() {
			count++
		}
	}
	if count == 0 {
		return ""
	}
	return fmt.Sprintf("You have %d message(s) in your inbox. Check %s for messages received during recycle.", count, roleInbox)
}

// FormatForInjection formats the hydration payload as text suitable for
// injection into an agent's prompt.
func (p *HydrationPayload) FormatForInjection() string {
	var b strings.Builder

	fmt.Fprintf(&b, "## Recovery Context (Recycle)\n\n")
	fmt.Fprintf(&b, "**Role:** %s\n", p.Role)
	fmt.Fprintf(&b, "**Previous Session:** %s\n", p.PrevSessionID)
	fmt.Fprintf(&b, "**Transcript:** %s\n\n", p.TranscriptPath)

	if p.Brief != "" {
		fmt.Fprintf(&b, "### Session Brief\n%s\n\n", p.Brief)
	}

	if p.LogTail != "" {
		fmt.Fprintf(&b, "### Recent Activity (log tail)\n```\n%s\n```\n\n", p.LogTail)
	}

	if p.InboxNotice != "" {
		fmt.Fprintf(&b, "### Inbox\n%s\n", p.InboxNotice)
	}

	return b.String()
}
