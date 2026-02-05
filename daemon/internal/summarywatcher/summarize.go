package summarywatcher

import (
	"context"
	"fmt"
	"strings"
)

// ChunkSummaryPrompt is the system prompt for chunk summarization.
const ChunkSummaryPrompt = `You are a session log summarizer. Analyze the following session log chunk and produce a concise summary.

Focus on:
- What task/goal is being worked on
- Key actions taken (files modified, commands run)
- Important decisions or discoveries
- Current state/progress
- Any errors or blockers encountered

Format as structured markdown with clear sections. Keep the summary to 300-400 tokens.
Do not include raw file contents or command outputs - summarize their meaning instead.`

// RollupPrompt is the system prompt for state rollup generation.
const RollupPrompt = `You are a session state synthesizer. Given the following chunk summaries from a coding session, produce a consolidated state rollup.

Synthesize into:
## Current Goal
[What the agent is ultimately trying to accomplish]

## Progress Summary
[What has been completed so far]

## Active Context
[Files, functions, or components currently being worked on]

## Key Decisions
[Important architectural or implementation decisions made]

## Open Issues
[Any unresolved problems or blockers]

## Next Steps
[Immediate priorities going forward]

Be concise but comprehensive. Target 400-500 tokens. Focus on current state, not history.`

// SummaryResult holds a summary and its source.
type SummaryResult struct {
	Content string
	Source  string // "haiku" or "heuristic"
}

// summarizeChunk generates a summary for a session log chunk.
// Returns the summary content and source (haiku or heuristic).
func (w *Watcher) summarizeChunk(ctx context.Context, content string) (SummaryResult, error) {
	if w.cfg.HaikuClient == nil {
		// Fallback to heuristic summary
		return SummaryResult{
			Content: w.heuristicChunkSummary(content),
			Source:  "heuristic",
		}, nil
	}

	summary, err := w.cfg.HaikuClient.Summarize(ctx, ChunkSummaryPrompt, content)
	if err != nil {
		// Fallback to heuristic on error
		return SummaryResult{
			Content: w.heuristicChunkSummary(content),
			Source:  "heuristic",
		}, nil
	}

	return SummaryResult{
		Content: summary,
		Source:  "haiku",
	}, nil
}

// summarizeForRollup generates a state rollup from chunk summaries.
// Returns the rollup content and source (haiku or heuristic).
func (w *Watcher) summarizeForRollup(ctx context.Context, summaries []string) (SummaryResult, error) {
	if len(summaries) == 0 {
		return SummaryResult{}, fmt.Errorf("no summaries to roll up")
	}

	// Combine summaries with chunk markers
	var combined strings.Builder
	for i, s := range summaries {
		combined.WriteString(fmt.Sprintf("=== Chunk %d Summary ===\n", i+1))
		combined.WriteString(s)
		combined.WriteString("\n\n")
	}

	if w.cfg.HaikuClient == nil {
		// Fallback to simple concatenation
		return SummaryResult{
			Content: w.heuristicRollupSummary(summaries),
			Source:  "heuristic",
		}, nil
	}

	rollup, err := w.cfg.HaikuClient.Summarize(ctx, RollupPrompt, combined.String())
	if err != nil {
		return SummaryResult{
			Content: w.heuristicRollupSummary(summaries),
			Source:  "heuristic",
		}, nil
	}

	return SummaryResult{
		Content: rollup,
		Source:  "haiku",
	}, nil
}

// heuristicChunkSummary generates a basic summary without LLM.
func (w *Watcher) heuristicChunkSummary(content string) string {
	var b strings.Builder
	b.WriteString("## Chunk Summary (Heuristic)\n\n")

	// Extract file references
	files := extractFileReferences(content)
	if len(files) > 0 {
		b.WriteString("### Files Referenced\n")
		for _, f := range files[:min(10, len(files))] {
			b.WriteString("- " + f + "\n")
		}
		b.WriteString("\n")
	}

	// Extract function names
	funcs := extractFunctionNames(content)
	if len(funcs) > 0 {
		b.WriteString("### Functions/Methods\n")
		for _, f := range funcs[:min(10, len(funcs))] {
			b.WriteString("- " + f + "\n")
		}
		b.WriteString("\n")
	}

	// Extract errors
	errors := extractErrors(content)
	if len(errors) > 0 {
		b.WriteString("### Errors\n")
		for _, e := range errors[:min(5, len(errors))] {
			b.WriteString("- " + truncateString(e, 100) + "\n")
		}
		b.WriteString("\n")
	}

	// Content stats
	lines := strings.Count(content, "\n")
	b.WriteString(fmt.Sprintf("### Stats\n- Lines: %d\n- Size: %d bytes\n", lines, len(content)))

	return b.String()
}

// heuristicRollupSummary generates a basic rollup without LLM.
func (w *Watcher) heuristicRollupSummary(summaries []string) string {
	var b strings.Builder
	b.WriteString("## State Rollup (Heuristic)\n\n")
	b.WriteString("**Note:** Generated via heuristic extraction (Haiku unavailable).\n\n")

	b.WriteString("### Recent Chunk Summaries\n\n")
	for i, s := range summaries {
		b.WriteString(fmt.Sprintf("#### Chunk %d\n", i+1))
		// Truncate each summary
		if len(s) > 500 {
			s = s[:500] + "..."
		}
		b.WriteString(s + "\n\n")
	}

	return b.String()
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}
