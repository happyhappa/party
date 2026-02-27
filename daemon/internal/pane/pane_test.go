package pane

import "testing"

func TestParsePaneStateCXReady(t *testing.T) {
	captured := "some output\n›"
	state := ParsePaneState("cx", captured)
	if !state.Ready {
		t.Fatalf("expected ready=true, got false")
	}
	if state.SuggestionActive {
		t.Fatalf("expected suggestion_active=false")
	}
	if state.ContextPct != -1 {
		t.Fatalf("expected context_pct=-1, got %d", state.ContextPct)
	}
}

func TestParsePaneStateCXSuggestionActive(t *testing.T) {
	captured := "› Run /review\n84% context left · ? for shortcuts"
	state := ParsePaneState("cx", captured)
	if state.Ready {
		t.Fatalf("expected ready=false when footer visible")
	}
	if !state.SuggestionActive {
		t.Fatalf("expected suggestion_active=true")
	}
	if state.ContextPct != 84 {
		t.Fatalf("expected context_pct=84, got %d", state.ContextPct)
	}
	if !state.Idle {
		t.Fatalf("expected idle=true")
	}
}

func TestParsePaneStateCXCompacted(t *testing.T) {
	captured := "Context compacted 12 seconds ago\n›"
	state := ParsePaneState("cx", captured)
	if !state.Compacted {
		t.Fatalf("expected compacted=true")
	}
	if state.CompactedAgoS != 12 {
		t.Fatalf("expected compacted_ago_s=12, got %d", state.CompactedAgoS)
	}
}

func TestParsePaneStateCC(t *testing.T) {
	captured := "work\n✻ Conversation compacted\n❯"
	state := ParsePaneState("cc", captured)
	if !state.Ready || !state.Idle {
		t.Fatalf("expected cc ready/idle true")
	}
	if !state.Compacted {
		t.Fatalf("expected compacted=true")
	}
	if state.ContextPct != -1 {
		t.Fatalf("expected context_pct=-1, got %d", state.ContextPct)
	}
	if state.SuggestionActive {
		t.Fatalf("expected suggestion_active=false")
	}
	if state.CompactedAgoS != 0 {
		t.Fatalf("expected compacted_ago_s=0 when marker has no duration, got %d", state.CompactedAgoS)
	}
}

func TestParsePaneStateCCWithStatusBar(t *testing.T) {
	// Claude Code has a status bar below the prompt — parser must skip it
	captured := "some output\n❯ \n──────────────────────────────────\n  ⏵⏵ bypass permissions on · 1 bash"
	state := ParsePaneState("cc", captured)
	if !state.Ready {
		t.Fatalf("expected ready=true with status bar below prompt, got false")
	}
	if !state.Idle {
		t.Fatalf("expected idle=true with status bar below prompt, got false")
	}
}

func TestParsePaneStateCCBusy(t *testing.T) {
	// When Claude is actively working, no ❯ prompt visible
	captured := "● Reading file.go\n\n* Thinking… (thought for 5s)\n──────────────────────────────────\n  ⏵⏵ bypass permissions on (shift+tab to cycle) · esc to interrupt"
	state := ParsePaneState("oc", captured)
	if state.Ready {
		t.Fatalf("expected ready=false when Claude is busy, got true")
	}
	if state.Idle {
		t.Fatalf("expected idle=false when Claude is busy, got false")
	}
}

func TestParsePaneStateCCCompactedWithStatusBar(t *testing.T) {
	captured := "work\n✻ Conversation compacted\n❯\n──────────────────────────────────\n  ⏵⏵ bypass permissions on · 1 bash"
	state := ParsePaneState("cc", captured)
	if !state.Ready {
		t.Fatalf("expected ready=true")
	}
	if !state.Compacted {
		t.Fatalf("expected compacted=true")
	}
}
