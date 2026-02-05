package admin

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/norm/relay-daemon/internal/autogen"
	"github.com/norm/relay-daemon/internal/haiku"
	"github.com/norm/relay-daemon/internal/tmux"
)

type captureHTTPClient struct {
	response []byte
}

func (c *captureHTTPClient) Do(req *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(c.response)),
	}, nil
}

func setHaikuClientHTTP(t *testing.T, hc *haiku.Client, httpClient option.HTTPClient) {
	t.Helper()
	anth := anthropic.NewClient(
		option.WithAPIKey("test-key"),
		option.WithHTTPClient(httpClient),
	)
	val := reflect.ValueOf(hc).Elem().FieldByName("client")
	ptr := unsafe.Pointer(val.UnsafeAddr())
	reflect.NewAt(val.Type(), ptr).Elem().Set(reflect.ValueOf(anth))
}

func TestAdminTimeoutTriggersAutogenBead(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "session.jsonl")
	session := "{\"type\":\"user\",\"content\":\"hello\"}\n"
	if err := os.WriteFile(logPath, []byte(session), 0o644); err != nil {
		t.Fatalf("write session log: %v", err)
	}

	// Fake bd binary to capture labels
	bdArgsPath := filepath.Join(tmpDir, "bd_args.txt")
	bdPath := filepath.Join(tmpDir, "bd")
	bdScript := "#!/bin/sh\nprintf \"%s\\n\" \"$@\" > \"" + bdArgsPath + "\"\necho bead-123\n"
	if err := os.WriteFile(bdPath, []byte(bdScript), 0o755); err != nil {
		t.Fatalf("write bd script: %v", err)
	}
	t.Setenv("PATH", tmpDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	resp := map[string]any{
		"id":            "msg_test",
		"type":          "message",
		"role":          "assistant",
		"model":         haiku.ModelHaiku3,
		"stop_reason":   "end_turn",
		"stop_sequence": "",
		"content": []map[string]any{
			{"type": "text", "text": "summary ok"},
		},
		"usage": map[string]any{
			"cache_creation": map[string]any{
				"ephemeral_1h_input_tokens": 0,
				"ephemeral_5m_input_tokens": 0,
			},
			"cache_creation_input_tokens": 0,
			"cache_read_input_tokens":     0,
			"input_tokens":                1,
			"output_tokens":               1,
			"server_tool_use": map[string]any{
				"web_search_requests": 0,
			},
			"service_tier": "standard",
		},
	}
	respBytes, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}

	stub := &captureHTTPClient{response: respBytes}
	hc, err := haiku.New(&haiku.Config{
		APIKey:         "test-key",
		MaxRetries:     0,
		RetryBaseDelay: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("new haiku client: %v", err)
	}
	setHaikuClientHTTP(t, hc, stub)

	gen := autogen.New(&autogen.Config{
		HaikuClient:  hc,
		InputTokens:  100,
		OutputTokens: 500,
		BytesPerToken: 4,
	})

	cfg := DefaultConfig()
	cfg.ACKTimeout = 1 * time.Millisecond
	cfg.SessionLogPaths = map[string]string{"cc": logPath}
	admin := New(cfg, nil, nil, tmux.NewInjector(nil, map[string]string{"cc": "%1"}), gen)

	admin.pendingRequests["cc"] = &PendingCheckpoint{
		ChkID:       "chk-timeout",
		Role:        "cc",
		RequestedAt: time.Now().Add(-2 * time.Second),
	}

	admin.checkTimeouts()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(bdArgsPath); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	argsBytes, err := os.ReadFile(bdArgsPath)
	if err != nil {
		t.Fatalf("expected bd args to be written: %v", err)
	}
	args := string(argsBytes)
	if !strings.Contains(args, "source:autogen") {
		t.Fatalf("expected source:autogen label, got %s", args)
	}
	if !strings.Contains(args, "confidence:low") {
		t.Fatalf("expected confidence:low label, got %s", args)
	}
	if !strings.Contains(args, "--type") || !strings.Contains(args, "recovery") {
		t.Fatalf("expected recovery type in bd args, got %s", args)
	}
}
