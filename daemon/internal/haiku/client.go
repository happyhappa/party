// Package haiku provides a client for Claude Haiku summarization.
package haiku

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// ModelHaiku3 is the Claude Haiku 3 model ID.
const ModelHaiku3 = "claude-3-haiku-20240307"

// Config holds Haiku client configuration.
type Config struct {
	// Model to use (defaults to Haiku 3)
	Model string

	// Max tokens for output
	MaxTokens int

	// Retry settings
	MaxRetries     int
	RetryBaseDelay time.Duration

	// API key source (if empty, uses BWS or ANTHROPIC_API_KEY env)
	APIKey string

	// BWS secret ID for API key (optional)
	BWSSecretID string
}

// DefaultConfig returns sensible defaults.
func DefaultConfig() *Config {
	return &Config{
		Model:          ModelHaiku3,
		MaxTokens:      500,
		MaxRetries:     3,
		RetryBaseDelay: time.Second,
	}
}

// Client wraps the Anthropic SDK for Haiku summarization.
type Client struct {
	cfg    *Config
	client anthropic.Client
}

// New creates a new Haiku client.
func New(cfg *Config) (*Client, error) {
	if cfg == nil {
		cfg = DefaultConfig()
	}

	apiKey, err := resolveAPIKey(cfg)
	if err != nil {
		return nil, fmt.Errorf("haiku: %w", err)
	}

	client := anthropic.NewClient(option.WithAPIKey(apiKey))

	return &Client{
		cfg:    cfg,
		client: client,
	}, nil
}

// Summarize sends a prompt to Haiku and returns the response.
// Includes retry logic with exponential backoff.
func (c *Client) Summarize(ctx context.Context, systemPrompt, userContent string) (string, error) {
	var lastErr error

	for attempt := 0; attempt <= c.cfg.MaxRetries; attempt++ {
		if attempt > 0 {
			// Exponential backoff
			delay := c.cfg.RetryBaseDelay * time.Duration(math.Pow(2, float64(attempt-1)))
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(delay):
			}
		}

		result, err := c.doRequest(ctx, systemPrompt, userContent)
		if err == nil {
			return result, nil
		}

		lastErr = err

		// Check if error is retryable
		if !isRetryable(err) {
			return "", err
		}
	}

	return "", fmt.Errorf("haiku: max retries exceeded: %w", lastErr)
}

// doRequest performs a single API request.
func (c *Client) doRequest(ctx context.Context, systemPrompt, userContent string) (string, error) {
	model := c.cfg.Model
	if model == "" {
		model = ModelHaiku3
	}

	maxTokens := c.cfg.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 500
	}

	resp, err := c.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.Model(model),
		MaxTokens: int64(maxTokens),
		System: []anthropic.TextBlockParam{
			{Text: systemPrompt},
		},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(userContent)),
		},
	})
	if err != nil {
		return "", fmt.Errorf("haiku request: %w", err)
	}

	// Extract text from response
	var result strings.Builder
	for _, block := range resp.Content {
		if block.Type == "text" {
			result.WriteString(block.Text)
		}
	}

	return result.String(), nil
}

// resolveAPIKey gets the API key from config, BWS, or environment.
func resolveAPIKey(cfg *Config) (string, error) {
	// 1. Direct config
	if cfg.APIKey != "" {
		return cfg.APIKey, nil
	}

	// 2. BWS secret
	if cfg.BWSSecretID != "" {
		key, err := getBWSSecret(cfg.BWSSecretID)
		if err == nil && key != "" {
			return key, nil
		}
		// Fall through to env var
	}

	// 3. Environment variable
	if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
		return key, nil
	}

	return "", errors.New("no API key: set ANTHROPIC_API_KEY or configure BWS")
}

// getBWSSecret retrieves a secret from Bitwarden Secrets Manager.
func getBWSSecret(secretID string) (string, error) {
	cmd := exec.Command("bws", "secret", "get", secretID, "--output", "json")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("bws get secret: %w", err)
	}

	// Parse JSON output - bws returns {"id":"...","value":"..."}
	// Simple extraction without full JSON parsing
	value := extractJSONValue(string(output), "value")
	if value == "" {
		return "", errors.New("bws: empty secret value")
	}

	return value, nil
}

// extractJSONValue extracts a string value from simple JSON.
func extractJSONValue(json, key string) string {
	// Look for "key":"value"
	search := fmt.Sprintf(`"%s":"`, key)
	idx := strings.Index(json, search)
	if idx == -1 {
		return ""
	}
	start := idx + len(search)
	end := strings.Index(json[start:], `"`)
	if end == -1 {
		return ""
	}
	return json[start : start+end]
}

// isRetryable checks if an error should be retried.
func isRetryable(err error) bool {
	if err == nil {
		return false
	}

	errStr := err.Error()

	// Retry on rate limits
	if strings.Contains(errStr, "rate_limit") || strings.Contains(errStr, "429") {
		return true
	}

	// Retry on server errors
	if strings.Contains(errStr, "500") || strings.Contains(errStr, "502") ||
		strings.Contains(errStr, "503") || strings.Contains(errStr, "504") {
		return true
	}

	// Retry on timeouts
	if strings.Contains(errStr, "timeout") || strings.Contains(errStr, "deadline") {
		return true
	}

	return false
}
