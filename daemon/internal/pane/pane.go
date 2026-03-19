package pane

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/norm/relay-daemon/internal/contract"
)

var durationAgoRe = regexp.MustCompile(`(?i)(\d+)\s*(seconds?|secs?|s|minutes?|mins?|m|hours?|hrs?|h)\s+ago`)

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
// This compatibility wrapper preserves the role-based API for callers that
// don't have the contract available yet.
func ParsePaneState(target, capturedText string) State {
	return ParsePaneStateFromSpec(defaultSpecForTarget(target), capturedText)
}

// ParsePaneStateFromSpec parses pane state using a contract-driven parser spec.
func ParsePaneStateFromSpec(spec contract.PaneParserSpec, capturedText string) State {
	lines := strings.Split(capturedText, "\n")
	out := State{
		ContextPct:    -1,
		CompactedAgoS: -1,
	}

	footerVisible := anyLineMatches(lines, spec.FooterMatchers)
	out.ContextPct = extractContext(capturedText, spec.ContextExtractors)
	out.SuggestionActive = anyLineMatches(lines, spec.SuggestionMatchers) && footerVisible

	promptVisible := false
	switch spec.Strategy {
	case "separator_scan":
		promptVisible = promptFromSeparatorScan(lines, spec.PromptPrefixes)
	case "last_nonempty_skip":
		promptVisible = promptFromLastNonEmpty(lines, spec.PromptPrefixes, spec.SkipMatchers)
	default:
		promptVisible = promptFromLastNonEmpty(lines, spec.PromptPrefixes, spec.SkipMatchers)
	}

	switch spec.ReadyPolicy {
	case "prompt_and_no_footer":
		out.Ready = promptVisible && !footerVisible
	default:
		out.Ready = promptVisible
	}

	switch spec.IdlePolicy {
	case "footer_or_prompt":
		out.Idle = footerVisible || promptVisible
	default:
		out.Idle = promptVisible
	}

	out.Compacted, out.CompactedAgoS = compactedState(capturedText, spec.CompactedMatchers)
	return out
}

// ParsePaneStateWithTelemetry extends ParsePaneState by overlaying sidecar
// telemetry data for CC/OC roles. CX is skipped (no custom statusline script).
// If the sidecar is missing, stale (>60s), or unparseable, the extra fields
// are left at zero values and the base terminal-parsed state is returned.
func ParsePaneStateWithTelemetry(target, capturedText, stateDir string, spec ...contract.PaneParserSpec) State {
	var out State
	if len(spec) > 0 {
		out = ParsePaneStateFromSpec(spec[0], capturedText)
	} else {
		out = ParsePaneState(target, capturedText)
	}
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
		return out
	}

	if td.ContextPct >= 0 {
		out.ContextPct = int(td.ContextPct)
	}
	out.ModelID = td.ModelID
	out.SessionID = td.SessionID
	out.CostUSD = td.CostUSD
	out.TokensIn = td.TokensIn
	out.TokensOut = td.TokensOut
	out.TelemetryAge = age

	verified := td.Role == role
	out.IdentityVerified = &verified

	return out
}

// CodexFooterVisible returns true when a pane capture shows the Codex footer.
// Kept for backwards compatibility with existing callers.
func CodexFooterVisible(capturedText string) bool {
	spec := contract.DefaultContract("/tmp/project", "/tmp/main").Tools["codex"].PaneParser
	return anyLineMatches(strings.Split(capturedText, "\n"), spec.FooterMatchers)
}

func defaultSpecForTarget(target string) contract.PaneParserSpec {
	role := strings.ToLower(strings.TrimSpace(target))
	defaults := contract.DefaultContract("/tmp/project", "/tmp/main")
	if role == "cx" {
		return defaults.Tools["codex"].PaneParser
	}
	return defaults.Tools["claude_code"].PaneParser
}

func matchLine(line string, matcher contract.LineMatcherSpec) bool {
	switch matcher.MatchType {
	case "prefix":
		for _, value := range matcher.Values {
			if strings.HasPrefix(line, value) {
				return true
			}
		}
	case "contains_all":
		for _, value := range matcher.Values {
			if !strings.Contains(line, value) {
				return false
			}
		}
		return len(matcher.Values) > 0
	case "regex":
		if matcher.Regex == "" {
			return false
		}
		re, err := regexp.Compile(matcher.Regex)
		if err != nil {
			return false
		}
		return re.MatchString(line)
	}
	return false
}

func matchText(text string, matcher contract.TextMatcherSpec) bool {
	switch matcher.MatchType {
	case "contains":
		return matcher.Value != "" && strings.Contains(strings.ToLower(text), strings.ToLower(matcher.Value))
	case "regex":
		if matcher.Value == "" {
			return false
		}
		re, err := regexp.Compile(matcher.Value)
		if err != nil {
			return false
		}
		return re.MatchString(text)
	}
	return false
}

func extractContext(text string, extractors []contract.ContextExtractSpec) int {
	lastPct := -1
	for _, line := range strings.Split(text, "\n") {
		for _, extractor := range extractors {
			if !lineMatchesAll(line, extractor.RequireLineMatchers) {
				continue
			}
			re, err := regexp.Compile(extractor.Regex)
			if err != nil {
				continue
			}
			matches := re.FindStringSubmatch(line)
			if extractor.ValueGroup <= 0 || extractor.ValueGroup >= len(matches) {
				continue
			}
			pct, err := strconv.Atoi(matches[extractor.ValueGroup])
			if err != nil {
				continue
			}
			lastPct = pct
		}
	}
	return lastPct
}

func anyLineMatches(lines []string, matchers []contract.LineMatcherSpec) bool {
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		for _, matcher := range matchers {
			if matchLine(trimmed, matcher) {
				return true
			}
		}
	}
	return false
}

func lineMatchesAll(line string, matchers []contract.LineMatcherSpec) bool {
	for _, matcher := range matchers {
		if !matchLine(line, matcher) {
			return false
		}
	}
	return true
}

func promptFromSeparatorScan(lines, promptPrefixes []string) bool {
	sepIdx := -1
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "─") {
			sepIdx = i
			break
		}
	}
	limit := len(lines) - 1
	if sepIdx >= 0 {
		limit = sepIdx - 1
	}
	for i := limit; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" {
			continue
		}
		return hasAnyPrefix(trimmed, promptPrefixes)
	}
	return false
}

func promptFromLastNonEmpty(lines, promptPrefixes []string, skipMatchers []contract.LineMatcherSpec) bool {
	for i := len(lines) - 1; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" || lineMatchesAny(trimmed, skipMatchers) {
			continue
		}
		return hasAnyPrefix(trimmed, promptPrefixes)
	}
	return false
}

func lineMatchesAny(line string, matchers []contract.LineMatcherSpec) bool {
	for _, matcher := range matchers {
		if matchLine(line, matcher) {
			return true
		}
	}
	return false
}

func hasAnyPrefix(line string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}

func compactedState(text string, matchers []contract.TextMatcherSpec) (bool, int) {
	lastAgo := -1
	found := false
	for _, matcher := range matchers {
		if !matchText(text, matcher) {
			continue
		}
		found = true
		for _, line := range strings.Split(text, "\n") {
			if matchText(line, matcher) {
				if ago := extractAgoSeconds(line); ago >= 0 {
					lastAgo = ago
				}
			}
		}
	}
	return found, lastAgo
}

func extractAgoSeconds(text string) int {
	ago := durationAgoRe.FindStringSubmatch(text)
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
