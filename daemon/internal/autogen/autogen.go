// Package autogen implements RFC-002 Tier 3 autogen checkpoint generation.
// Autogen is triggered when checkpoint ACK times out OR session log is inaccessible.
package autogen

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/norm/relay-daemon/internal/contextcapture"
	"github.com/norm/relay-daemon/internal/haiku"
)

// SystemPrompt is the prompt template for autogen checkpoint extraction.
const SystemPrompt = `You are a recovery assistant. Summarize the following agent session log into a recovery checkpoint.

Extract and format as follows:

## Current Goal
[1-2 sentences: What the agent is trying to accomplish]

## Key Decisions
[Bullet list: Important decisions made and their rationale]

## Blockers
[Bullet list: What's preventing progress, or "(none)" if clear]

## Next Steps
[Numbered list: Immediate actions in priority order]

Be concise. Output 400-500 tokens maximum. Focus on actionable state, not history.`

// Config holds autogen configuration.
type Config struct {
	// Haiku client for LLM summarization
	HaikuClient *haiku.Client

	// Token budget for session log extraction
	InputTokens  int // ~4000 tokens
	OutputTokens int // ~500 tokens

	// Bytes per token estimate
	BytesPerToken int
}

// DefaultConfig returns sensible defaults per RFC-002.
func DefaultConfig() *Config {
	return &Config{
		InputTokens:   4000,
		OutputTokens:  500,
		BytesPerToken: 4,
	}
}

// Generator creates autogen checkpoints from session logs.
type Generator struct {
	cfg *Config
}

// New creates a new autogen generator.
func New(cfg *Config) *Generator {
	if cfg == nil {
		cfg = DefaultConfig()
	}
	return &Generator{cfg: cfg}
}

// Result holds the autogen checkpoint result.
type Result struct {
	Content    string    // Checkpoint content (markdown)
	Source     string    // "autogen" or "heuristic"
	Confidence string    // "low" or "very-low"
	Role       string    // Agent role
	ChkID      string    // Checkpoint correlation ID
	CreatedAt  time.Time // Generation timestamp
}

// Generate creates an autogen checkpoint from a session log.
// Returns Result with Source:"autogen" if Haiku succeeds, or Source:"heuristic" as fallback.
func (g *Generator) Generate(ctx context.Context, role, chkID, sessionLogPath string) (*Result, error) {
	// Extract session log slice
	content, err := g.extractSessionSlice(sessionLogPath)
	if err != nil {
		return nil, fmt.Errorf("autogen: extract session: %w", err)
	}

	if content == "" {
		return nil, fmt.Errorf("autogen: empty session content")
	}

	result := &Result{
		Role:      role,
		ChkID:     chkID,
		CreatedAt: time.Now(),
	}

	// Try Haiku summarization
	if g.cfg.HaikuClient != nil {
		summary, err := g.cfg.HaikuClient.Summarize(ctx, SystemPrompt, content)
		if err == nil && summary != "" {
			result.Content = summary
			result.Source = "autogen"
			result.Confidence = "low"
			return result, nil
		}
		// Fall through to heuristic
	}

	// Heuristic fallback
	heuristic := g.heuristicExtract(content)
	result.Content = heuristic
	result.Source = "heuristic"
	result.Confidence = "very-low"

	return result, nil
}

// extractSessionSlice extracts ~4k tokens from the session log.
func (g *Generator) extractSessionSlice(path string) (string, error) {
	tokens := g.cfg.InputTokens
	if tokens <= 0 {
		tokens = 4000
	}
	bytesPerToken := g.cfg.BytesPerToken
	if bytesPerToken <= 0 {
		bytesPerToken = 4
	}

	// Use contextcapture for extraction
	content, err := contextcapture.TailExtract(path, tokens, bytesPerToken)
	if err != nil {
		return "", err
	}

	return content, nil
}

// heuristicExtract performs regex-based extraction when Haiku is unavailable.
func (g *Generator) heuristicExtract(content string) string {
	var b strings.Builder
	b.WriteString("# Autogen Checkpoint (Heuristic)\n\n")
	b.WriteString("**Warning:** Generated via heuristic extraction (Haiku unavailable).\n\n")

	// Extract file paths mentioned (non-capturing group for extension)
	files := extractPatterns(content, []string{
		`[a-zA-Z0-9_\-./]+\.(?:go|ts|js|py|md|yaml|json|sh)`,
	})
	if len(files) > 0 {
		b.WriteString("## Files Referenced\n")
		for _, f := range dedupe(files)[:min(10, len(files))] {
			b.WriteString("- " + f + "\n")
		}
		b.WriteString("\n")
	}

	// Extract function/method names
	funcs := extractPatterns(content, []string{
		`func\s+(\w+)`,
		`function\s+(\w+)`,
		`def\s+(\w+)`,
	})
	if len(funcs) > 0 {
		b.WriteString("## Functions/Methods\n")
		for _, f := range dedupe(funcs)[:min(10, len(files))] {
			b.WriteString("- " + f + "\n")
		}
		b.WriteString("\n")
	}

	// Extract error messages
	errors := extractPatterns(content, []string{
		`error:?\s*[^\n]+`,
		`Error:?\s*[^\n]+`,
		`failed:?\s*[^\n]+`,
	})
	if len(errors) > 0 {
		b.WriteString("## Errors Encountered\n")
		for _, e := range dedupe(errors)[:min(5, len(errors))] {
			b.WriteString("- " + truncate(e, 100) + "\n")
		}
		b.WriteString("\n")
	}

	// Extract commands run
	commands := extractPatterns(content, []string{
		`\$\s*([^\n]+)`,
		`>\s*([^\n]+)`,
	})
	if len(commands) > 0 {
		b.WriteString("## Commands Run\n")
		for _, c := range dedupe(commands)[:min(5, len(commands))] {
			b.WriteString("- `" + truncate(c, 80) + "`\n")
		}
		b.WriteString("\n")
	}

	b.WriteString("## Next Steps\n")
	b.WriteString("1. Review session log for context\n")
	b.WriteString("2. Run `/restore` for full recovery\n")

	return b.String()
}

// BeadLabels returns the labels for an autogen bead.
func (r *Result) BeadLabels() map[string]string {
	return map[string]string{
		"role":       r.Role,
		"chk_id":     r.ChkID,
		"source":     r.Source,
		"confidence": r.Confidence,
	}
}

// BeadTitle returns the title for an autogen bead.
func (r *Result) BeadTitle() string {
	return fmt.Sprintf("%s autogen checkpoint %s", r.Role, r.CreatedAt.Format("2006-01-02 15:04"))
}

// WriteBead writes the autogen result to beads via bd CLI.
func (r *Result) WriteBead() (string, error) {
	args := []string{
		"create",
		"--type", "recovery",
		"--title", r.BeadTitle(),
	}

	for k, v := range r.BeadLabels() {
		args = append(args, "--label", k+":"+v)
	}

	args = append(args, "--body", r.Content)

	bdPath := os.ExpandEnv("$HOME/go/bin/bd")
	cmd := exec.Command(bdPath, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("bd create: %w: %s", err, string(output))
	}

	// Extract bead ID from output (first word typically)
	beadID := strings.Fields(string(output))
	if len(beadID) > 0 {
		return beadID[0], nil
	}

	return "", nil
}
