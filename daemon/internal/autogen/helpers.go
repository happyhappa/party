package autogen

import (
	"regexp"
	"strings"
)

// extractPatterns extracts all matches for given regex patterns from content.
func extractPatterns(content string, patterns []string) []string {
	var results []string
	for _, pattern := range patterns {
		re, err := regexp.Compile(pattern)
		if err != nil {
			continue
		}
		matches := re.FindAllStringSubmatch(content, -1)
		for _, m := range matches {
			if len(m) > 1 {
				// Use first capture group
				results = append(results, strings.TrimSpace(m[1]))
			} else if len(m) > 0 {
				results = append(results, strings.TrimSpace(m[0]))
			}
		}
	}
	return results
}

// dedupe removes duplicate strings while preserving order.
func dedupe(items []string) []string {
	seen := make(map[string]bool)
	var result []string
	for _, item := range items {
		if item == "" {
			continue
		}
		if !seen[item] {
			seen[item] = true
			result = append(result, item)
		}
	}
	return result
}

// truncate shortens a string to maxLen, adding "..." if truncated.
func truncate(s string, maxLen int) string {
	s = strings.TrimSpace(s)
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}

// min returns the smaller of two ints.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
