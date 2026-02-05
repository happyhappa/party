package summarywatcher

import (
	"regexp"
	"strings"
)

// extractFileReferences extracts file paths from content.
func extractFileReferences(content string) []string {
	// Match common file extensions with paths
	pattern := regexp.MustCompile(`[a-zA-Z0-9_\-./]+\.(?:go|ts|js|py|md|yaml|json|sh|tsx|jsx|css|html|sql|proto|toml)`)
	matches := pattern.FindAllString(content, -1)
	return dedupe(matches)
}

// extractFunctionNames extracts function/method names from content.
func extractFunctionNames(content string) []string {
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`func\s+(\w+)`),         // Go
		regexp.MustCompile(`func\s+\([^)]+\)\s+(\w+)`), // Go methods
		regexp.MustCompile(`function\s+(\w+)`),     // JS
		regexp.MustCompile(`def\s+(\w+)`),          // Python
		regexp.MustCompile(`async\s+function\s+(\w+)`), // JS async
		regexp.MustCompile(`const\s+(\w+)\s*=\s*(?:async\s*)?\(`), // JS arrow
	}

	var results []string
	for _, re := range patterns {
		matches := re.FindAllStringSubmatch(content, -1)
		for _, m := range matches {
			if len(m) > 1 {
				results = append(results, strings.TrimSpace(m[1]))
			}
		}
	}
	return dedupe(results)
}

// extractErrors extracts error messages from content.
func extractErrors(content string) []string {
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`(?i)error:?\s*([^\n]+)`),
		regexp.MustCompile(`(?i)failed:?\s*([^\n]+)`),
		regexp.MustCompile(`(?i)panic:?\s*([^\n]+)`),
		regexp.MustCompile(`(?i)exception:?\s*([^\n]+)`),
		regexp.MustCompile(`FAIL\s+([^\n]+)`),
	}

	var results []string
	for _, re := range patterns {
		matches := re.FindAllStringSubmatch(content, -1)
		for _, m := range matches {
			if len(m) > 1 {
				results = append(results, strings.TrimSpace(m[1]))
			} else if len(m) > 0 {
				results = append(results, strings.TrimSpace(m[0]))
			}
		}
	}
	return dedupe(results)
}

// extractCommands extracts shell commands from content.
func extractCommands(content string) []string {
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`\$\s*([^\n]+)`),
		regexp.MustCompile(`>\s*([^\n]+)`),
		regexp.MustCompile(`(?m)^(?:go|npm|yarn|pip|cargo|make|docker|git|kubectl)\s+[^\n]+`),
	}

	var results []string
	for _, re := range patterns {
		matches := re.FindAllStringSubmatch(content, -1)
		for _, m := range matches {
			if len(m) > 1 {
				results = append(results, strings.TrimSpace(m[1]))
			} else if len(m) > 0 {
				results = append(results, strings.TrimSpace(m[0]))
			}
		}
	}
	return dedupe(results)
}

// dedupe removes duplicate strings while preserving order.
func dedupe(items []string) []string {
	seen := make(map[string]bool)
	var result []string
	for _, item := range items {
		item = strings.TrimSpace(item)
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
