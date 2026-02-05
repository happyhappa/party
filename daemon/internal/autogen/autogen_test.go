package autogen

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

func TestExtractPatterns(t *testing.T) {
	content := `
func main() {
	fmt.Println("hello")
}

def helper():
    pass

function doStuff() {
}
`
	// Test Go func extraction
	goFuncs := extractPatterns(content, []string{`func\s+(\w+)`})
	if len(goFuncs) != 1 || goFuncs[0] != "main" {
		t.Errorf("expected [main], got %v", goFuncs)
	}

	// Test Python def extraction
	pyFuncs := extractPatterns(content, []string{`def\s+(\w+)`})
	if len(pyFuncs) != 1 || pyFuncs[0] != "helper" {
		t.Errorf("expected [helper], got %v", pyFuncs)
	}

	// Test JS function extraction
	jsFuncs := extractPatterns(content, []string{`function\s+(\w+)`})
	if len(jsFuncs) != 1 || jsFuncs[0] != "doStuff" {
		t.Errorf("expected [doStuff], got %v", jsFuncs)
	}
}

func TestDedupe(t *testing.T) {
	input := []string{"a", "b", "a", "c", "b", "", "d"}
	result := dedupe(input)

	expected := []string{"a", "b", "c", "d"}
	if len(result) != len(expected) {
		t.Errorf("expected %v, got %v", expected, result)
	}
	for i, v := range expected {
		if result[i] != v {
			t.Errorf("at index %d: expected %q, got %q", i, v, result[i])
		}
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		input  string
		maxLen int
		expect string
	}{
		{"hello", 10, "hello"},
		{"hello", 5, "hello"},
		{"hello world", 8, "hello..."},
		{"hi", 2, "hi"},
		{"hello", 3, "hel"},
	}

	for _, tt := range tests {
		result := truncate(tt.input, tt.maxLen)
		if result != tt.expect {
			t.Errorf("truncate(%q, %d) = %q, want %q", tt.input, tt.maxLen, result, tt.expect)
		}
	}
}

func TestHeuristicExtract(t *testing.T) {
	g := New(nil)

	content := `
Working on autogen.go file
func Generate() for autogen
error: test failure
$ go build ./...
`

	result := g.heuristicExtract(content)

	if !strings.Contains(result, "Heuristic") {
		t.Error("expected heuristic warning in output")
	}
	if !strings.Contains(result, "autogen.go") {
		t.Error("expected file reference in output")
	}
	if !strings.Contains(result, "Generate") {
		t.Error("expected function name in output")
	}
	if !strings.Contains(result, "test failure") {
		t.Error("expected error in output")
	}
	if !strings.Contains(result, "go build") {
		t.Error("expected command in output")
	}
}

func TestResultBeadLabels(t *testing.T) {
	r := &Result{
		Role:       "cc",
		ChkID:      "chk-test",
		Source:     "autogen",
		Confidence: "low",
	}

	labels := r.BeadLabels()
	if labels["role"] != "cc" {
		t.Errorf("expected role=cc, got %s", labels["role"])
	}
	if labels["source"] != "autogen" {
		t.Errorf("expected source=autogen, got %s", labels["source"])
	}
	if labels["confidence"] != "low" {
		t.Errorf("expected confidence=low, got %s", labels["confidence"])
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.InputTokens != 4000 {
		t.Fatalf("expected InputTokens=4000, got %d", cfg.InputTokens)
	}
	if cfg.OutputTokens != 500 {
		t.Fatalf("expected OutputTokens=500, got %d", cfg.OutputTokens)
	}
	if cfg.BytesPerToken != 4 {
		t.Fatalf("expected BytesPerToken=4, got %d", cfg.BytesPerToken)
	}
}

type captureHTTPClient struct {
	body     []byte
	response []byte
}

func (c *captureHTTPClient) Do(req *http.Request) (*http.Response, error) {
	if req.Body != nil {
		b, _ := io.ReadAll(req.Body)
		c.body = b
	}
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

func TestGenerateUsesPromptAndHaiku(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "session.jsonl")
	session := "{\"type\":\"user\",\"content\":\"hello\"}\n{\"type\":\"assistant\",\"content\":\"world\"}\n"
	if err := os.WriteFile(logPath, []byte(session), 0o644); err != nil {
		t.Fatalf("write session log: %v", err)
	}

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

	gen := New(&Config{
		HaikuClient:  hc,
		InputTokens:  100,
		OutputTokens: 500,
		BytesPerToken: 4,
	})

	result, err := gen.Generate(context.Background(), "cc", "chk-123", logPath)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if result.Source != "autogen" || result.Confidence != "low" {
		t.Fatalf("expected autogen/low, got %s/%s", result.Source, result.Confidence)
	}
	if result.Content != "summary ok" {
		t.Fatalf("expected summary content, got %q", result.Content)
	}

	var req map[string]any
	if err := json.Unmarshal(stub.body, &req); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	system := req["system"].([]any)
	systemText := system[0].(map[string]any)["text"].(string)
	if systemText != SystemPrompt {
		t.Fatalf("system prompt mismatch")
	}
	messages := req["messages"].([]any)
	msg0 := messages[0].(map[string]any)
	content := msg0["content"].([]any)
	userText := content[0].(map[string]any)["text"].(string)
	if !strings.Contains(userText, "user: hello") {
		t.Fatalf("expected user content to include tail, got %q", userText)
	}
}

func TestGenerateHeuristicFallback(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "session.jsonl")
	session := "{\"type\":\"user\",\"content\":\"Working on autogen.go file\\nfunc Generate() for autogen\\nerror: test failure\\n$ go build ./...\"}\n"
	if err := os.WriteFile(logPath, []byte(session), 0o644); err != nil {
		t.Fatalf("write session log: %v", err)
	}

	gen := New(&Config{
		HaikuClient:  nil,
		InputTokens:  100,
		OutputTokens: 500,
		BytesPerToken: 4,
	})

	result, err := gen.Generate(context.Background(), "cc", "chk-456", logPath)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if result.Source != "heuristic" || result.Confidence != "very-low" {
		t.Fatalf("expected heuristic/very-low, got %s/%s", result.Source, result.Confidence)
	}
	if !strings.Contains(result.Content, "Heuristic") {
		t.Fatalf("expected heuristic warning in content")
	}
	if !strings.Contains(result.Content, "autogen.go") {
		t.Fatalf("expected file reference in content")
	}
}
