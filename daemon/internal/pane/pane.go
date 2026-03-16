package pane

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	contextPctRe    = regexp.MustCompile(`(\d+)%\s*used`)
	durationAgoRe   = regexp.MustCompile(`(?i)(\d+)\s*(seconds?|secs?|s|minutes?|mins?|m|hours?|hrs?|h)\s+ago`)
	cxCompactRe     = regexp.MustCompile(`(?i)context compacted(?:[^0-9]+(\d+\s*(?:seconds?|secs?|s|minutes?|mins?|m|hours?|hrs?|h)\s+ago))?`)
	claudeCompactRe = regexp.MustCompile(`(?i)✻\s*conversation compacted(?:[^0-9]+(\d+\s*(?:seconds?|secs?|s|minutes?|mins?|m|hours?|hrs?|h)\s+ago))?`)
)

// TelemetryData is the sidecar JSON written by the statusline script.
type TelemetryData struct {
	Role         string  `json:"role"`
	Timestamp    int64   `json:"timestamp"`
	ContextPct   float64 `json:"context_pct"`
	ModelID      string  `json:"model_id"`
	ModelDisplay string  `json:"model_display"`
	SessionID    string  `json:"session_id"`
	CostUSD      float64 `json:"cost_usd"`
	DurationMS   int64   `json:"duration_ms"`
	TokensIn     int64   `json:"tokens_in"`
	TokensOut    int64   `json:"tokens_out"`
	LinesAdded   int64   `json:"lines_added"`
	LinesRemoved int64   `json:"lines_removed"`
}

// ReadTelemetrySidecar reads the telemetry sidecar file for a given role.
// Returns nil and an error if the file is missing or unparseable.
func ReadTelemetrySidecar(stateDir, role string) (*TelemetryData, error) {
	path := filepath.Join(stateDir, "telemetry-"+role+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var td TelemetryData
	if err := json.Unmarshal(data, &td); err != nil {
		return nil, err
	}
	return &td, nil
}

// State is parsed pane state data emitted by relay-daemon --pane-status.
type State struct {
	Ready            bool   `json:"ready"`
	ContextPct       int    `json:"context_pct"`
	SuggestionActive bool   `json:"suggestion_active"`
	Compacted        bool   `json:"compacted"`
	CompactedAgoS    int    `json:"compacted_ago_s"`
	Idle             bool   `json:"idle"`
	ProcessName      string `json:"process_name,omitempty"`
	// Sidecar telemetry fields (CC/OC only, populated by ParsePaneStateWithTelemetry)
	ModelID          string  `json:"model_id,omitempty"`
	SessionID        string  `json:"session_id,omitempty"`
	CostUSD          float64 `json:"cost_usd,omitempty"`
	TokensIn         int64   `json:"tokens_in,omitempty"`
	TokensOut        int64   `json:"tokens_out,omitempty"`
	TelemetryAge     int     `json:"telemetry_age_s,omitempty"`
	IdentityVerified *bool   `json:"identity_verified,omitempty"`
}

// ParsePaneState parses pane capture text into normalized pane state.
func ParsePaneState(target, capturedText string) State {
	role := strings.ToLower(strings.TrimSpace(target))
	out := State{
		ContextPct:    -1,
		CompactedAgoS: -1,
	}

	last := lastNonEmptyLine(capturedText)
	trimmedLast := strings.TrimSpace(last)

	switch role {
	case "cx":
		footer := CodexFooterVisible(capturedText)
		out.ContextPct = extractContextPct(capturedText)
		out.SuggestionActive = hasSuggestionLine(capturedText) && footer
		// The CX statusline ("model · N% used · path · branch") may be the
		// last non-empty line. Skip it to find the real prompt line.
		effectiveLast := trimmedLast
		if strings.Contains(trimmedLast, "·") && !strings.HasPrefix(trimmedLast, "›") {
			effectiveLast = lastNonEmptyLineSkipping(capturedText, func(line string) bool {
				return strings.Contains(line, "·")
			})
		}
		out.Ready = strings.HasPrefix(effectiveLast, "›") && !footer
		out.Idle = footer || (out.Ready && out.ContextPct >= 0)
		if strings.Contains(strings.ToLower(capturedText), "context compacted") {
			out.Compacted = true
			out.CompactedAgoS = extractCompactedAgoSeconds(capturedText, cxCompactRe)
		}
	default:
		prompt := hasClaudePrompt(capturedText)
		out.Ready = prompt
		out.Idle = prompt
		if strings.Contains(strings.ToLower(capturedText), "conversation compacted") {
			out.Compacted = true
			out.CompactedAgoS = extractCompactedAgoSeconds(capturedText, claudeCompactRe)
		}
	}

	return out
}

// ParsePaneStateWithTelemetry extends ParsePaneState by overlaying sidecar
// telemetry data for CC/OC roles. CX is skipped (no custom statusline script).
// If the sidecar is missing, stale (>60s), or unparseable, the extra fields
// are left at zero values and the base terminal-parsed state is returned.
func ParsePaneStateWithTelemetry(target, capturedText, stateDir string) State {
	out := ParsePaneState(target, capturedText)
	role := strings.ToLower(strings.TrimSpace(target))

	// CX has no sidecar — skip
	if role == "cx" || stateDir == "" {
		return out
	}

	td, err := ReadTelemetrySidecar(stateDir, role)
	if err != nil || td == nil {
		return out
	}

	age := int(time.Now().Unix() - td.Timestamp)
	if age > 60 || age < 0 {
		// Stale or future timestamp — don't trust it
		return out
	}

	// Overlay sidecar data
	if td.ContextPct >= 0 {
		out.ContextPct = int(td.ContextPct)
	}
	out.ModelID = td.ModelID
	out.SessionID = td.SessionID
	out.CostUSD = td.CostUSD
	out.TokensIn = td.TokensIn
	out.TokensOut = td.TokensOut
	out.TelemetryAge = age

	// Identity verification: sidecar role should match expected role
	verified := td.Role == role
	out.IdentityVerified = &verified

	return out
}

// CodexFooterVisible returns true when Codex footer metadata is visible.
// Checks both old "% left" format and new "% used" statusline format.
func CodexFooterVisible(capturedText string) bool {
	return strings.Contains(capturedText, "% left ·") || strings.Contains(capturedText, "% context left") || strings.Contains(capturedText, "% used ·")
}

// extractContextPct extracts context-used percentage from the CX statusline.
// The statusline format is "model · N% used · ~/path · branch".
// Only matches lines where · appears both before and after "N% used" to avoid
// false positives from arbitrary output (e.g. "disk 40% used").
// Returns the value directly as used-percentage (same direction as CC/OC sidecar).
func extractContextPct(capturedText string) int {
	lastPct := -1
	for _, line := range strings.Split(capturedText, "\n") {
		m := contextPctRe.FindStringSubmatch(line)
		if len(m) < 2 {
			continue
		}
		// Require · both before and after the match (statusline shape).
		idx := strings.Index(line, m[0])
		if !strings.Contains(line[:idx], "·") || !strings.Contains(line[idx+len(m[0]):], "·") {
			continue
		}
		pct, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		lastPct = pct
	}
	return lastPct
}

func extractCompactedAgoSeconds(capturedText string, marker *regexp.Regexp) int {
	if marker == nil {
		return -1
	}
	matches := marker.FindAllString(capturedText, -1)
	if len(matches) == 0 {
		return -1
	}
	last := matches[len(matches)-1]
	ago := durationAgoRe.FindStringSubmatch(last)
	if len(ago) < 3 {
		return -1
	}
	value, err := strconv.Atoi(ago[1])
	if err != nil {
		return -1
	}
	unit := strings.ToLower(ago[2])
	switch {
	case strings.HasPrefix(unit, "h"):
		return value * 3600
	case strings.HasPrefix(unit, "m"):
		return value * 60
	default:
		return value
	}
}

// hasSuggestionLine returns true if any line starts with the › prompt prefix.
// More precise than strings.Contains — avoids matching › in arbitrary output content.
func hasSuggestionLine(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "›") {
			return true
		}
	}
	return false
}

// hasClaudePrompt scans backwards through captured text looking for the ❯
// prompt. Skips the entire "footer zone" below the last ─── separator, which
// may contain the statusline content line and the ⏵⏵ permission bar.
// If no separator is found, falls back to skipping ⏵ lines only.
func hasClaudePrompt(capturedText string) bool {
	lines := strings.Split(capturedText, "\n")
	// Find the last separator line (scanning backwards)
	sepIdx := -1
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "─") {
			sepIdx = i
			break
		}
	}
	if sepIdx >= 0 {
		// Skip entire footer zone; scan above separator
		for i := sepIdx - 1; i >= 0; i-- {
			trimmed := strings.TrimSpace(lines[i])
			if trimmed == "" {
				continue
			}
			return strings.HasPrefix(trimmed, "❯")
		}
		return false
	}
	// No separator found — fall back to skipping ⏵ lines
	for i := len(lines) - 1; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" || strings.HasPrefix(trimmed, "⏵") {
			continue
		}
		return strings.HasPrefix(trimmed, "❯")
	}
	return false
}

func lastNonEmptyLine(out string) string {
	lines := strings.Split(out, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line != "" {
			return line
		}
	}
	return ""
}

// lastNonEmptyLineSkipping returns the last non-empty line that does not
// match the skip predicate. Used to skip CX statusline when finding prompt.
func lastNonEmptyLineSkipping(out string, skip func(string) bool) string {
	lines := strings.Split(out, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" || skip(line) {
			continue
		}
		return line
	}
	return ""
}
