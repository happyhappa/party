package pane

import (
	"testing"

	"github.com/norm/relay-daemon/internal/contract"
)

// Sample pane captures for integration testing.
var (
	sampleClaudeReady = `Some previous output here.

Working on the task...

❯ `

	sampleClaudeCompacted = `Some output.

✻ Conversation compacted 30 seconds ago

❯ `

	sampleClaudeBusy = `Reading files and analyzing code...

Let me check the implementation.`

	sampleCXReady = `Previous codex output.

o4-mini · 25% used · ~/project · main

› `

	sampleCXFooter = `Here is the result of the task.

o4-mini · 40% used · ~/project · main

84% context left · ? for shortcuts`

	sampleCXCompacted = `Some output.
context compacted

o4-mini · 10% used · ~/project · main

› `
)

// TestParseContractPaneSpecs verifies that ParsePaneStateFromSpec using the
// default contract's PaneParserSpec produces results matching ParsePaneState
// for the same inputs.
func TestParseContractPaneSpecs(t *testing.T) {
	dc := contract.DefaultContract("/tmp/test-project", "/tmp/test-project/main")

	// Verify contract has PaneParser specs for both tools
	for _, toolName := range []string{"claude_code", "codex"} {
		tool, ok := dc.Tools[toolName]
		if !ok {
			t.Fatalf("missing tool %q in default contract", toolName)
		}
		if tool.PaneParser.Strategy == "" {
			t.Errorf("%s: PaneParser.Strategy is empty", toolName)
		}
		if len(tool.PaneParser.PromptPrefixes) == 0 {
			t.Errorf("%s: PaneParser.PromptPrefixes is empty", toolName)
		}
	}

	tests := []struct {
		name       string
		role       string
		tool       string
		input      string
		wantReady  bool
		wantIdle   bool
		wantPct    int
		wantCompacted bool
	}{
		{
			name:      "claude_ready",
			role:      "oc",
			tool:      "claude_code",
			input:     sampleClaudeReady,
			wantReady: true,
			wantIdle:  true,
			wantPct:   -1,
		},
		{
			name:          "claude_compacted",
			role:          "cc",
			tool:          "claude_code",
			input:         sampleClaudeCompacted,
			wantReady:     true,
			wantIdle:      true,
			wantPct:       -1,
			wantCompacted: true,
		},
		{
			name:      "claude_busy",
			role:      "oc",
			tool:      "claude_code",
			input:     sampleClaudeBusy,
			wantReady: false,
			wantIdle:  false,
			wantPct:   -1,
		},
		{
			name:      "cx_ready",
			role:      "cx",
			tool:      "codex",
			input:     sampleCXReady,
			wantReady: true,
			wantIdle:  true,
			wantPct:   25,
		},
		{
			name:      "cx_footer",
			role:      "cx",
			tool:      "codex",
			input:     sampleCXFooter,
			wantReady: false,
			wantIdle:  true,
			wantPct:   40,
		},
		{
			name:          "cx_compacted",
			role:          "cx",
			tool:          "codex",
			input:         sampleCXCompacted,
			wantReady:     true,
			wantIdle:      true,
			wantCompacted: true,
			wantPct:       10,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			toolSpec, ok := dc.Tools[tt.tool]
			if !ok {
				t.Fatalf("tool %q not in contract", tt.tool)
			}

			// Run both parsers
			baseline := ParsePaneState(tt.role, tt.input)
			fromSpec := ParsePaneStateFromSpec(toolSpec.PaneParser, tt.input)

			// Verify expected values against both parsers
			for label, got := range map[string]State{"baseline": baseline, "fromSpec": fromSpec} {
				if got.Ready != tt.wantReady {
					t.Errorf("%s: Ready = %v, want %v", label, got.Ready, tt.wantReady)
				}
				if got.Idle != tt.wantIdle {
					t.Errorf("%s: Idle = %v, want %v", label, got.Idle, tt.wantIdle)
				}
				if tt.wantPct >= 0 && got.ContextPct != tt.wantPct {
					t.Errorf("%s: ContextPct = %d, want %d", label, got.ContextPct, tt.wantPct)
				}
				if got.Compacted != tt.wantCompacted {
					t.Errorf("%s: Compacted = %v, want %v", label, got.Compacted, tt.wantCompacted)
				}
			}

			// Cross-check: both parsers should agree
			if baseline.Ready != fromSpec.Ready {
				t.Errorf("Ready mismatch: baseline=%v fromSpec=%v", baseline.Ready, fromSpec.Ready)
			}
			if baseline.Idle != fromSpec.Idle {
				t.Errorf("Idle mismatch: baseline=%v fromSpec=%v", baseline.Idle, fromSpec.Idle)
			}
			if baseline.ContextPct != fromSpec.ContextPct {
				t.Errorf("ContextPct mismatch: baseline=%d fromSpec=%d", baseline.ContextPct, fromSpec.ContextPct)
			}
			if baseline.Compacted != fromSpec.Compacted {
				t.Errorf("Compacted mismatch: baseline=%v fromSpec=%v", baseline.Compacted, fromSpec.Compacted)
			}
		})
	}
}

// TestContractSpecFieldCoverage verifies that the default contract PaneParserSpec
// covers all the patterns that the hardcoded parser uses.
func TestContractSpecFieldCoverage(t *testing.T) {
	dc := contract.DefaultContract("/tmp/test", "/tmp/test/main")

	// Claude Code (separator_scan strategy)
	cc := dc.Tools["claude_code"].PaneParser
	if cc.Strategy != "separator_scan" {
		t.Errorf("claude_code strategy = %q, want separator_scan", cc.Strategy)
	}
	if len(cc.PromptPrefixes) == 0 || cc.PromptPrefixes[0] != "❯" {
		t.Errorf("claude_code prompt prefix = %v, want [❯]", cc.PromptPrefixes)
	}
	if len(cc.CompactedMatchers) == 0 {
		t.Error("claude_code: no compacted matchers")
	}
	if cc.ReadyPolicy != "prompt_only" {
		t.Errorf("claude_code ReadyPolicy = %q, want prompt_only", cc.ReadyPolicy)
	}

	// Codex (last_nonempty_skip strategy)
	cx := dc.Tools["codex"].PaneParser
	if cx.Strategy != "last_nonempty_skip" {
		t.Errorf("codex strategy = %q, want last_nonempty_skip", cx.Strategy)
	}
	if len(cx.PromptPrefixes) == 0 || cx.PromptPrefixes[0] != "›" {
		t.Errorf("codex prompt prefix = %v, want [›]", cx.PromptPrefixes)
	}
	if len(cx.FooterMatchers) == 0 {
		t.Error("codex: no footer matchers")
	}
	if len(cx.StatuslineMatchers) == 0 {
		t.Error("codex: no statusline matchers")
	}
	if len(cx.ContextExtractors) == 0 {
		t.Error("codex: no context extractors")
	}
	if cx.ReadyPolicy != "prompt_and_no_footer" {
		t.Errorf("codex ReadyPolicy = %q, want prompt_and_no_footer", cx.ReadyPolicy)
	}
}
