package contextcapture

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
)

const maxPayloadBytes = 10 * 1024

// Message represents a parsed session log message suitable for tail rendering.
type Message struct {
	Role      string
	Content   string
	Timestamp string
	RawType   string
}

// ParseMessages parses Claude session log JSONL entries from a reader.
func ParseMessages(r io.Reader) ([]Message, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)

	var messages []Message
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}

		msg, skip, err := parseJSONLLine(line)
		if err != nil {
			continue
		}
		if skip {
			continue
		}
		messages = append(messages, msg)
	}

	if err := scanner.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			return messages, nil
		}
		return messages, err
	}

	return messages, nil
}

// ParseMessagesFromOffset reads from a byte offset and parses messages.
func ParseMessagesFromOffset(path string, offset int64) ([]Message, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	if offset > 0 {
		if _, err := file.Seek(offset, io.SeekStart); err != nil {
			return nil, err
		}
		skipPartialLine(file)
	}

	return ParseMessages(file)
}

func skipPartialLine(r io.Reader) {
	reader := bufio.NewReader(r)
	_, _ = reader.ReadString('\n')
}

func parseJSONLLine(line []byte) (Message, bool, error) {
	var env map[string]any
	if err := json.Unmarshal(line, &env); err != nil {
		return Message{}, false, err
	}

	typ, _ := env["type"].(string)
	role := firstString(env, "role", "sender", "from")
	ts := firstString(env, "timestamp", "created_at", "time")

	content := extractContent(env)
	if len(content) > maxPayloadBytes {
		return Message{}, true, nil
	}

	content = strings.TrimSpace(content)
	if content == "" {
		return Message{}, true, nil
	}

	if role == "" {
		if msg, ok := env["message"].(map[string]any); ok {
			role = firstString(msg, "role")
		}
		if role == "" {
			if payload, ok := env["payload"].(map[string]any); ok {
				role = firstString(payload, "role")
				if role == "" {
					if msg, ok := payload["message"].(map[string]any); ok {
						role = firstString(msg, "role")
					}
				}
			}
		}
		if role == "" {
			role = roleFromType(typ)
		}
	}

	return Message{
		Role:      role,
		Content:   content,
		Timestamp: ts,
		RawType:   typ,
	}, false, nil
}

func extractContent(env map[string]any) string {
	if payload, ok := env["payload"].(map[string]any); ok {
		if content := extractContentFromPayload(payload); content != "" {
			return content
		}
		if msg, ok := payload["message"].(map[string]any); ok {
			if content := extractContentFromPayload(msg); content != "" {
				return content
			}
		}
	}
	if content := extractString(env["content"]); content != "" {
		return content
	}
	if msg, ok := env["message"].(map[string]any); ok {
		if content := extractContentFromPayload(msg); content != "" {
			return content
		}
	}
	return ""
}

func extractContentFromPayload(payload map[string]any) string {
	if content := extractString(payload["content"]); content != "" {
		return content
	}
	if text := extractString(payload["text"]); text != "" {
		return text
	}
	if msg, ok := payload["message"].(map[string]any); ok {
		if content := extractContentFromPayload(msg); content != "" {
			return content
		}
	}
	if parts, ok := payload["content"].([]any); ok {
		return concatTextParts(parts)
	}
	return ""
}

func concatTextParts(parts []any) string {
	var b strings.Builder
	for _, part := range parts {
		switch val := part.(type) {
		case string:
			if val != "" {
				b.WriteString(val)
				b.WriteString("\n")
			}
		case map[string]any:
			if val["type"] == "text" {
				if text := extractString(val["text"]); text != "" {
					b.WriteString(text)
					b.WriteString("\n")
				}
			}
		}
	}
	return strings.TrimSpace(b.String())
}

func extractString(v any) string {
	if v == nil {
		return ""
	}
	switch val := v.(type) {
	case string:
		return val
	case []byte:
		return string(val)
	default:
		return ""
	}
}

func firstString(env map[string]any, keys ...string) string {
	for _, key := range keys {
		if val, ok := env[key]; ok {
			if str := extractString(val); str != "" {
				return str
			}
		}
	}
	return ""
}

func roleFromType(typ string) string {
	switch typ {
	case "assistant", "assistant_message":
		return "assistant"
	case "user", "user_message":
		return "user"
	default:
		return ""
	}
}
