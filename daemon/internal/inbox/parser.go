package inbox

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/norm/relay-daemon/pkg/envelope"
)

// Defaults supplies auto-filled values for incoming messages.
type Defaults struct {
	From      string
	ProjectID string
}

var errMissingSeparator = errors.New("rmf: missing header separator")
var errMissingTo = errors.New("rmf: missing TO header")

// ParseMessage parses an RMF v2 message into an Envelope.
func ParseMessage(message []byte) (*envelope.Envelope, error) {
	return ParseMessageWithDefaults(message, Defaults{})
}

// ParseMessageWithDefaults parses an RMF v2 message and fills missing fields with defaults.
func ParseMessageWithDefaults(message []byte, defaults Defaults) (*envelope.Envelope, error) {
	if strings.TrimSpace(string(message)) == "" {
		return nil, nil
	}

	lines := strings.Split(strings.ReplaceAll(string(message), "\r\n", "\n"), "\n")
	separator := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == "---" {
			separator = i
			break
		}
	}
	if separator == -1 {
		return nil, errMissingSeparator
	}

	headers := lines[:separator]
	bodyLines := []string{}
	if separator+1 < len(lines) {
		bodyLines = lines[separator+1:]
	}
	body := strings.Join(bodyLines, "\n")

	env := envelope.Envelope{
		Payload: body,
	}

	for _, line := range headers {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		colon := strings.Index(line, ":")
		if colon == -1 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(line[:colon]))
		value := strings.TrimSpace(line[colon+1:])
		switch key {
		case "to":
			env.To = value
		case "from":
			env.From = value
		case "project":
			env.ProjectID = value
		case "project_id":
			env.ProjectID = value
		case "kind":
			env.Kind = value
		case "thread":
			env.ThreadID = value
		case "thread_id":
			env.ThreadID = value
		case "msg_id":
			env.MsgID = value
		case "ts":
			env.Timestamp = value
		case "priority":
			if value == "" {
				continue
			}
			priority, err := strconv.Atoi(value)
			if err != nil {
				return nil, fmt.Errorf("rmf: invalid priority %q: %w", value, err)
			}
			env.Priority = priority
		case "ephemeral":
			if value == "" {
				continue
			}
			ephemeral, err := strconv.ParseBool(value)
			if err != nil {
				return nil, fmt.Errorf("rmf: invalid ephemeral %q: %w", value, err)
			}
			env.Ephemeral = ephemeral
		}
	}

	if defaults.ProjectID != "" && env.ProjectID == "" {
		env.ProjectID = defaults.ProjectID
	}
	if defaults.From != "" {
		env.From = defaults.From
	}
	if env.To == "" {
		return nil, errMissingTo
	}
	if env.MsgID == "" {
		env.MsgID = envelope.GenerateMsgID()
	}
	if env.Timestamp == "" {
		env.Timestamp = time.Now().UTC().Format(time.RFC3339)
	}
	if env.Priority == 0 {
		env.Priority = 1
	}

	return &env, nil
}

// ParseFile reads a single RMF v2 message from a file.
func ParseFile(path string) ([]*envelope.Envelope, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	var b strings.Builder
	for scanner.Scan() {
		b.WriteString(scanner.Text())
		b.WriteByte('\n')
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	env, err := ParseMessage([]byte(b.String()))
	if err != nil {
		return nil, err
	}
	if env == nil {
		return nil, nil
	}
	return []*envelope.Envelope{env}, nil
}
