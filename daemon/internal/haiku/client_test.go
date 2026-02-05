package haiku

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

type messageResponse struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Role    string `json:"role"`
	Model   string `json:"model"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	StopReason   string `json:"stop_reason"`
	StopSequence string `json:"stop_sequence"`
	Usage        struct {
		CacheCreation struct {
			Ephemeral1hInputTokens int64 `json:"ephemeral_1h_input_tokens"`
			Ephemeral5mInputTokens int64 `json:"ephemeral_5m_input_tokens"`
		} `json:"cache_creation"`
		CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
		CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
		InputTokens              int64 `json:"input_tokens"`
		OutputTokens             int64 `json:"output_tokens"`
		ServerToolUse            struct {
			WebSearchRequests int64 `json:"web_search_requests"`
		} `json:"server_tool_use"`
		ServiceTier string `json:"service_tier"`
	} `json:"usage"`
}

type stubHTTPClient struct {
	responder func(req *http.Request, call int32) *http.Response
	calls     int32
}

func (s *stubHTTPClient) Do(req *http.Request) (*http.Response, error) {
	call := atomic.AddInt32(&s.calls, 1)
	return s.responder(req, call), nil
}

func TestResolveAPIKeyEnvFallback(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "env-key")
	key, err := resolveAPIKey(&Config{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if key != "env-key" {
		t.Fatalf("expected env key, got %q", key)
	}
}

func TestResolveAPIKeyMissing(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	_, err := resolveAPIKey(&Config{})
	if err == nil {
		t.Fatalf("expected error when no API key configured")
	}
}

func TestSummarizeSuccess(t *testing.T) {
	resp := messageResponse{
		ID:           "msg_test",
		Type:         "message",
		Role:         "assistant",
		Model:        ModelHaiku3,
		StopReason:   "end_turn",
		StopSequence: "",
	}
	resp.Content = []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}{{Type: "text", Text: "ok"}}
	resp.Usage.CacheCreation.Ephemeral1hInputTokens = 0
	resp.Usage.CacheCreation.Ephemeral5mInputTokens = 0
	resp.Usage.CacheCreationInputTokens = 0
	resp.Usage.CacheReadInputTokens = 0
	resp.Usage.InputTokens = 1
	resp.Usage.OutputTokens = 1
	resp.Usage.ServerToolUse.WebSearchRequests = 0
	resp.Usage.ServiceTier = "standard"

	body, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}

	stub := &stubHTTPClient{
		responder: func(req *http.Request, call int32) *http.Response {
			if req.Method != http.MethodPost {
				return &http.Response{StatusCode: http.StatusMethodNotAllowed, Body: io.NopCloser(bytes.NewReader(nil))}
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(bytes.NewReader(body)),
			}
		},
	}

	c := &Client{
		cfg: &Config{
			Model:          ModelHaiku3,
			MaxTokens:      10,
			MaxRetries:     0,
			RetryBaseDelay: time.Millisecond,
		},
		client: anthropic.NewClient(
			option.WithAPIKey("test-key"),
			option.WithHTTPClient(stub),
		),
	}

	got, err := c.Summarize(context.Background(), "system", "user")
	if err != nil {
		t.Fatalf("summarize error: %v", err)
	}
	if got != "ok" {
		t.Fatalf("expected summary 'ok', got %q", got)
	}
}

func TestSummarizeRetriesOnServerError(t *testing.T) {
	var calls int32
	resp := messageResponse{
		ID:           "msg_test",
		Type:         "message",
		Role:         "assistant",
		Model:        ModelHaiku3,
		StopReason:   "end_turn",
		StopSequence: "",
	}
	resp.Content = []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}{{Type: "text", Text: "retry-ok"}}
	resp.Usage.CacheCreation.Ephemeral1hInputTokens = 0
	resp.Usage.CacheCreation.Ephemeral5mInputTokens = 0
	resp.Usage.CacheCreationInputTokens = 0
	resp.Usage.CacheReadInputTokens = 0
	resp.Usage.InputTokens = 1
	resp.Usage.OutputTokens = 1
	resp.Usage.ServerToolUse.WebSearchRequests = 0
	resp.Usage.ServiceTier = "standard"

	body, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}

	stub := &stubHTTPClient{
		responder: func(req *http.Request, call int32) *http.Response {
			atomic.StoreInt32(&calls, call)
			if call == 1 {
				return &http.Response{StatusCode: http.StatusInternalServerError, Body: io.NopCloser(bytes.NewReader(nil))}
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(bytes.NewReader(body)),
			}
		},
	}

	c := &Client{
		cfg: &Config{
			Model:          ModelHaiku3,
			MaxTokens:      10,
			MaxRetries:     1,
			RetryBaseDelay: time.Millisecond,
		},
		client: anthropic.NewClient(
			option.WithAPIKey("test-key"),
			option.WithHTTPClient(stub),
		),
	}

	got, err := c.Summarize(context.Background(), "system", "user")
	if err != nil {
		t.Fatalf("summarize error: %v", err)
	}
	if got != "retry-ok" {
		t.Fatalf("expected summary 'retry-ok', got %q", got)
	}
	if atomic.LoadInt32(&calls) < 2 {
		t.Fatalf("expected at least 2 calls, got %d", calls)
	}
}

func TestSummarizeRetriesOnRateLimit(t *testing.T) {
	var calls int32
	resp := messageResponse{
		ID:           "msg_test",
		Type:         "message",
		Role:         "assistant",
		Model:        ModelHaiku3,
		StopReason:   "end_turn",
		StopSequence: "",
	}
	resp.Content = []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}{{Type: "text", Text: "rate-ok"}}
	resp.Usage.CacheCreation.Ephemeral1hInputTokens = 0
	resp.Usage.CacheCreation.Ephemeral5mInputTokens = 0
	resp.Usage.CacheCreationInputTokens = 0
	resp.Usage.CacheReadInputTokens = 0
	resp.Usage.InputTokens = 1
	resp.Usage.OutputTokens = 1
	resp.Usage.ServerToolUse.WebSearchRequests = 0
	resp.Usage.ServiceTier = "standard"

	body, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}

	stub := &stubHTTPClient{
		responder: func(req *http.Request, call int32) *http.Response {
			atomic.StoreInt32(&calls, call)
			if call == 1 {
				return &http.Response{StatusCode: http.StatusTooManyRequests, Body: io.NopCloser(bytes.NewReader([]byte("rate_limit")))}
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(bytes.NewReader(body)),
			}
		},
	}

	c := &Client{
		cfg: &Config{
			Model:          ModelHaiku3,
			MaxTokens:      10,
			MaxRetries:     1,
			RetryBaseDelay: time.Millisecond,
		},
		client: anthropic.NewClient(
			option.WithAPIKey("test-key"),
			option.WithHTTPClient(stub),
		),
	}

	got, err := c.Summarize(context.Background(), "system", "user")
	if err != nil {
		t.Fatalf("summarize error: %v", err)
	}
	if got != "rate-ok" {
		t.Fatalf("expected summary 'rate-ok', got %q", got)
	}
	if atomic.LoadInt32(&calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", calls)
	}
}

func TestSummarizeMaxRetriesExceeded(t *testing.T) {
	stub := &stubHTTPClient{
		responder: func(req *http.Request, call int32) *http.Response {
			return &http.Response{StatusCode: http.StatusInternalServerError, Body: io.NopCloser(bytes.NewReader([]byte("server error")))}
		},
	}

	c := &Client{
		cfg: &Config{
			Model:          ModelHaiku3,
			MaxTokens:      10,
			MaxRetries:     1,
			RetryBaseDelay: time.Millisecond,
		},
		client: anthropic.NewClient(
			option.WithAPIKey("test-key"),
			option.WithHTTPClient(stub),
		),
	}

	_, err := c.Summarize(context.Background(), "system", "user")
	if err == nil || !strings.Contains(err.Error(), "max retries exceeded") {
		t.Fatalf("expected max retries exceeded error, got %v", err)
	}
	if atomic.LoadInt32(&stub.calls) < 2 {
		t.Fatalf("expected at least 2 calls, got %d", stub.calls)
	}
}
