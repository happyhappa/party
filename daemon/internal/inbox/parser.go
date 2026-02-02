package inbox

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/norm/relay-daemon/pkg/envelope"
)

// sanitizeForJSON escapes control characters, invalid UTF-8, and fixes invalid escape sequences.
// This allows LLMs to send messages containing escape sequences without manual escaping.
func sanitizeForJSON(s string) string {
	var b strings.Builder
	b.Grow(len(s) + len(s)/10) // Allow for some expansion

	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])

		if r == utf8.RuneError && size == 1 {
			// Invalid UTF-8 byte - escape as unicode
			b.WriteString(fmt.Sprintf("\\u%04x", s[i]))
			i++
			continue
		}

		// Escape control characters (except tab, newline, carriage return which JSON allows)
		if r < 0x20 && r != '\t' && r != '\n' && r != '\r' {
			b.WriteString(fmt.Sprintf("\\u%04x", r))
			i += size
			continue
		}

		// Handle backslashes - check if followed by valid JSON escape char
		if r == '\\' && i+size < len(s) {
			next := s[i+size]
			// Valid JSON escapes: " \ / b f n r t u
			if next == '"' || next == '\\' || next == '/' ||
				next == 'b' || next == 'f' || next == 'n' ||
				next == 'r' || next == 't' || next == 'u' {
				// Valid escape sequence - keep as is
				b.WriteByte('\\')
				i += size
				continue
			}
			// Invalid escape - double the backslash to escape it
			b.WriteString("\\\\")
			i += size
			continue
		}

		b.WriteRune(r)
		i += size
	}
	return b.String()
}

// Defaults supplies auto-filled values for incoming messages.
type Defaults struct {
	From      string
	ProjectID string
}

type partialEnvelope struct {
	MsgID     string `json:"msg_id"`
	Timestamp string `json:"ts"`
	ProjectID string `json:"project_id"`
	From      string `json:"from"`
	To        string `json:"to"`
	Kind      string `json:"kind"`
	Priority  *int   `json:"priority"`
	ThreadID  string `json:"thread_id"`
	Payload   string `json:"payload"`
	Ephemeral *bool  `json:"ephemeral"`
}

// ParseLine parses a single JSONL line into an Envelope.
func ParseLine(line []byte) (*envelope.Envelope, error) {
	return ParseLineWithDefaults(line, Defaults{})
}

// ParseLineWithDefaults parses a line and fills missing fields with defaults.
func ParseLineWithDefaults(line []byte, defaults Defaults) (*envelope.Envelope, error) {
	trimmed := strings.TrimSpace(string(line))
	if trimmed == "" {
		return nil, nil
	}

	// Sanitize control characters that break JSON parsing (e.g., escape sequences from LLMs)
	sanitized := sanitizeForJSON(trimmed)

	var partial partialEnvelope
	if err := json.Unmarshal([]byte(sanitized), &partial); err != nil {
		return nil, err
	}

	env := envelope.Envelope{
		MsgID:     partial.MsgID,
		Timestamp: partial.Timestamp,
		ProjectID: partial.ProjectID,
		From:      partial.From,
		To:        partial.To,
		Kind:      partial.Kind,
		ThreadID:  partial.ThreadID,
		Payload:   partial.Payload,
	}

	if defaults.ProjectID != "" && env.ProjectID == "" {
		env.ProjectID = defaults.ProjectID
	}
	if defaults.From != "" {
		env.From = defaults.From
	}
	if env.MsgID == "" {
		env.MsgID = envelope.GenerateMsgID()
	}
	if env.Timestamp == "" {
		env.Timestamp = time.Now().UTC().Format(time.RFC3339)
	}
	if partial.Priority != nil {
		env.Priority = *partial.Priority
	} else {
		env.Priority = 1
	}
	if partial.Ephemeral != nil {
		env.Ephemeral = *partial.Ephemeral
	}

	return &env, nil
}

// ParseFile reads all envelopes from a JSONL file.
func ParseFile(path string) ([]*envelope.Envelope, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	var out []*envelope.Envelope
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		env, err := ParseLine(scanner.Bytes())
		if err != nil {
			return nil, fmt.Errorf("parse %s line %d: %w", path, lineNum, err)
		}
		if env != nil {
			out = append(out, env)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
