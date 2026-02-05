package summarywatcher

import (
	"bytes"
	"context"
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
	"github.com/norm/relay-daemon/internal/haiku"
)

type captureRequest struct {
	SystemPrompt string
	UserContent  string
}

type captureHTTPClient struct {
	response []byte
	requests []captureRequest
}

func (c *captureHTTPClient) Do(req *http.Request) (*http.Response, error) {
	body, _ := io.ReadAll(req.Body)
	var payload map[string]any
	_ = json.Unmarshal(body, &payload)

	system := ""
	if sys, ok := payload["system"].([]any); ok && len(sys) > 0 {
		if block, ok := sys[0].(map[string]any); ok {
			if text, ok := block["text"].(string); ok {
				system = text
			}
		}
	}
	user := ""
	if msgs, ok := payload["messages"].([]any); ok && len(msgs) > 0 {
		if msg0, ok := msgs[0].(map[string]any); ok {
			if content, ok := msg0["content"].([]any); ok && len(content) > 0 {
				if block, ok := content[0].(map[string]any); ok {
					if text, ok := block["text"].(string); ok {
						user = text
					}
				}
			}
		}
	}
	c.requests = append(c.requests, captureRequest{SystemPrompt: system, UserContent: user})

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

func buildResponse(text string) []byte {
	resp := map[string]any{
		"id":            "msg_test",
		"type":          "message",
		"role":          "assistant",
		"model":         haiku.ModelHaiku3,
		"stop_reason":   "end_turn",
		"stop_sequence": "",
		"content": []map[string]any{
			{"type": "text", "text": text},
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
	out, _ := json.Marshal(resp)
	return out
}

func TestWatcherChunkOverlapRollupAndBeads(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "session.jsonl")

	// Build two chunks of content (written in two phases)
	chunk1 := strings.Repeat("{\"type\":\"user\",\"content\":\"line-1\"}\n", 5)
	chunk2 := strings.Repeat("{\"type\":\"assistant\",\"content\":\"line-2\"}\n", 5)

	if err := os.WriteFile(logPath, []byte(chunk1), 0o644); err != nil {
		t.Fatalf("write chunk1: %v", err)
	}

	// Fake bd binary to capture bead writes
	bdArgsPath := filepath.Join(tmpDir, "bd_args.txt")
	bdPath := filepath.Join(tmpDir, "bd")
	bdScript := "#!/bin/sh\nprintf \"%s\\n\" \"$@\" >> \"" + bdArgsPath + "\"\necho bead-123\n"
	if err := os.WriteFile(bdPath, []byte(bdScript), 0o755); err != nil {
		t.Fatalf("write bd script: %v", err)
	}
	t.Setenv("PATH", tmpDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	// Haiku client stub
	stub := &captureHTTPClient{response: buildResponse("chunk ok")}
	hc, err := haiku.New(&haiku.Config{
		APIKey:         "test-key",
		MaxRetries:     0,
		RetryBaseDelay: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("new haiku client: %v", err)
	}
	setHaikuClientHTTP(t, hc, stub)

	cfg := &Config{
		SessionLogPath: logPath,
		Role:           "cc",
		ChunkSizeTokens: 50,
		BytesPerToken:   1,
		OverlapPercent:  20,
		MinChunkSizeBytes: 1,
		ChunksPerRollup: 1,
		StateDir:        tmpDir,
		HaikuClient:     hc,
	}
	w := New(cfg)

	ctx := context.Background()

	// Process first chunk
	w.checkForNewContent(ctx)

	// Append second chunk and process again
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open log for append: %v", err)
	}
	if _, err := f.WriteString(chunk2); err != nil {
		_ = f.Close()
		t.Fatalf("append chunk2: %v", err)
	}
	_ = f.Close()

	w.checkForNewContent(ctx)

	// Verify Haiku called for chunk summaries and rollups
	var chunkCalls []captureRequest
	var rollupCalls []captureRequest
	for _, req := range stub.requests {
		switch req.SystemPrompt {
		case ChunkSummaryPrompt:
			chunkCalls = append(chunkCalls, req)
		case RollupPrompt:
			rollupCalls = append(rollupCalls, req)
		}
	}
	if len(chunkCalls) < 2 {
		t.Fatalf("expected at least 2 chunk summary calls, got %d", len(chunkCalls))
	}
	if len(rollupCalls) < 2 {
		t.Fatalf("expected at least 2 rollup calls, got %d", len(rollupCalls))
	}

	// Second chunk should include overlap marker
	if !strings.Contains(chunkCalls[1].UserContent, "--- Previous context ---") {
		t.Fatalf("expected overlap marker in second chunk content")
	}

	// Rollup should include chunk markers
	if !strings.Contains(rollupCalls[0].UserContent, "=== Chunk 1 Summary ===") {
		t.Fatalf("expected rollup content to include chunk markers")
	}

	// Verify beads were written for chunk_summary and state_rollup
	argsBytes, err := os.ReadFile(bdArgsPath)
	if err != nil {
		t.Fatalf("expected bd args to be written: %v", err)
	}
	args := string(argsBytes)
	if !strings.Contains(args, "--type") || !strings.Contains(args, "chunk_summary") {
		t.Fatalf("expected chunk_summary bead write, got args: %s", args)
	}
	if !strings.Contains(args, "state_rollup") {
		t.Fatalf("expected state_rollup bead write, got args: %s", args)
	}
}
