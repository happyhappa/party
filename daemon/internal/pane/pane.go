package pane

import (
	"regexp"
	"strconv"
	"strings"
)

var (
	contextPctRe    = regexp.MustCompile(`(\d+)%\s*(?:context\s+)?left`)
	durationAgoRe   = regexp.MustCompile(`(?i)(\d+)\s*(seconds?|secs?|s|minutes?|mins?|m|hours?|hrs?|h)\s+ago`)
	cxCompactRe     = regexp.MustCompile(`(?i)context compacted(?:[^0-9]+(\d+\s*(?:seconds?|secs?|s|minutes?|mins?|m|hours?|hrs?|h)\s+ago))?`)
	claudeCompactRe = regexp.MustCompile(`(?i)✻\s*conversation compacted(?:[^0-9]+(\d+\s*(?:seconds?|secs?|s|minutes?|mins?|m|hours?|hrs?|h)\s+ago))?`)
)

// State is parsed pane state data emitted by relay-daemon --pane-status.
type State struct {
	Ready            bool   `json:"ready"`
	ContextPct       int    `json:"context_pct"`
	SuggestionActive bool   `json:"suggestion_active"`
	Compacted        bool   `json:"compacted"`
	CompactedAgoS    int    `json:"compacted_ago_s"`
	Idle             bool   `json:"idle"`
	ProcessName      string `json:"process_name,omitempty"`
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
		out.Ready = strings.HasPrefix(trimmedLast, "›") && !footer
		out.Idle = footer
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

// CodexFooterVisible returns true when Codex footer metadata is visible.
func CodexFooterVisible(capturedText string) bool {
	return strings.Contains(capturedText, "% left ·") || strings.Contains(capturedText, "% context left")
}

// extractContextPct extracts context percentage from Codex footer lines only.
// Skips statusline lines (which contain "· N% left ·" with middle-dots before
// the percentage) to avoid false matches.
func extractContextPct(capturedText string) int {
	lastPct := -1
	for _, line := range strings.Split(capturedText, "\n") {
		m := contextPctRe.FindStringSubmatch(line)
		if len(m) < 2 {
			continue
		}
		// Statusline format: "model · N% left · path" — has · before the pct.
		// Footer format: "N% context left · ?" or "N% left · ?" — pct is at/near line start.
		// Skip lines where a · appears before the percentage match.
		idx := strings.Index(line, m[0])
		prefix := line[:idx]
		if strings.Contains(prefix, "·") {
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
