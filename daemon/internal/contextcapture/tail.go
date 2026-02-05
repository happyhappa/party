package contextcapture

import (
	"fmt"
	"os"
	"strings"
)

const defaultMaxLineLen = 400

// TailExtract extracts a readable tail from a session log path.
func TailExtract(path string, tailTokens int, bytesPerToken int) (string, error) {
	if tailTokens <= 0 || bytesPerToken <= 0 {
		return "", fmt.Errorf("invalid tail parameters")
	}

	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}

	bytesToRead := int64(tailTokens * bytesPerToken)
	size := info.Size()
	start := int64(0)
	if size > bytesToRead {
		start = size - bytesToRead
	}

	messages, err := ParseMessagesFromOffset(path, start)
	if err != nil {
		return "", err
	}

	return formatMessages(messages), nil
}

// TailExtractFromConfig discovers the session log and extracts tail using config defaults.
func TailExtractFromConfig(cfg *Config) (string, error) {
	path, err := DiscoverSessionLog(cfg)
	if err != nil {
		return "", err
	}
	if cfg == nil {
		cfg = DefaultConfig()
	}
	return TailExtract(path, cfg.Recovery.TailTokens, cfg.Recovery.TailBytesPerToken)
}

// TailExtractFromOffset extracts tail starting from a specific offset.
// This is used to skip content already covered by chunk summaries (overlap skip).
// If minStartOffset is provided, extraction starts from max(calculated_start, minStartOffset).
func TailExtractFromOffset(path string, tailTokens int, bytesPerToken int, minStartOffset int64) (string, error) {
	if tailTokens <= 0 || bytesPerToken <= 0 {
		return "", fmt.Errorf("invalid tail parameters")
	}

	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}

	bytesToRead := int64(tailTokens * bytesPerToken)
	size := info.Size()

	// Calculate start position
	start := int64(0)
	if size > bytesToRead {
		start = size - bytesToRead
	}

	// Apply overlap skip: start from minStartOffset if it's greater
	if minStartOffset > 0 && minStartOffset > start && minStartOffset < size {
		start = minStartOffset
	}

	messages, err := ParseMessagesFromOffset(path, start)
	if err != nil {
		return "", err
	}

	return formatMessages(messages), nil
}

func formatMessages(messages []Message) string {
	var b strings.Builder
	for _, msg := range messages {
		content := abbreviate(msg.Content, defaultMaxLineLen)
		if content == "" {
			continue
		}
		role := msg.Role
		if role == "" {
			role = "unknown"
		}
		if msg.Timestamp != "" {
			b.WriteString("[")
			b.WriteString(msg.Timestamp)
			b.WriteString("] ")
		}
		b.WriteString(role)
		b.WriteString(": ")
		b.WriteString(content)
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

func abbreviate(content string, maxLen int) string {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return ""
	}
	if maxLen <= 0 || len(trimmed) <= maxLen {
		return trimmed
	}
	return strings.TrimSpace(trimmed[:maxLen]) + "…"
}
