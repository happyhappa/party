package pane

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

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
	captured := "› Run /review\n84% context left · ? for shortcuts\n\ngpt-5.3-codex · 16% used · ~/path · cx-wt"
	state := ParsePaneState("cx", captured)
	if state.Ready {
		t.Fatalf("expected ready=false when footer visible")
	}
	if !state.SuggestionActive {
		t.Fatalf("expected suggestion_active=true")
	}
	if state.ContextPct != 16 {
		t.Fatalf("expected context_pct=16 (used), got %d", state.ContextPct)
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
	if state.CompactedAgoS != -1 {
		t.Fatalf("expected compacted_ago_s=-1 when marker has no duration, got %d", state.CompactedAgoS)
	}
}

func TestParsePaneStateCXShortFooter(t *testing.T) {
	captured := "› Run /review\n84% left · ? for shortcuts\n\ngpt-5.3-codex · 16% used · ~/path · cx-wt"
	state := ParsePaneState("cx", captured)
	if state.ContextPct != 16 {
		t.Fatalf("expected context_pct=16 (used) from statusline, got %d", state.ContextPct)
	}
	if !state.SuggestionActive {
		t.Fatalf("expected suggestion_active=true")
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

func TestParsePaneStateCCWithStatusline(t *testing.T) {
	// New statusline layout: separator + statusline content + permission bar
	captured := "some output\n❯ \n──────────────────────────────────\n  ~/Sandbox/personal/new_party/cc-wt | cc-wt | Opus 4.6 | ctx:14%\n  ⏵⏵ bypass permissions on (shift+tab to cycle)"
	state := ParsePaneState("cc", captured)
	if !state.Ready {
		t.Fatalf("expected ready=true with statusline below prompt, got false")
	}
	if !state.Idle {
		t.Fatalf("expected idle=true with statusline below prompt, got false")
	}
}

func TestParsePaneStateCCBusyWithStatusline(t *testing.T) {
	// Busy with statusline — no prompt visible
	captured := "● Reading file.go\n\n✽ Thinking… (thought for 5s)\n──────────────────────────────────\n  ~/path | main | Opus 4.6 | ctx:34%\n  ⏵⏵ bypass permissions on (shift+tab to cycle) · esc to interrupt"
	state := ParsePaneState("oc", captured)
	if state.Ready {
		t.Fatalf("expected ready=false when busy with statusline, got true")
	}
	if state.Idle {
		t.Fatalf("expected idle=false when busy with statusline, got false")
	}
}

func TestParsePaneStateCXWithStatusline(t *testing.T) {
	// CX with both footer and statusline — statusline "N% used" is the primary source
	captured := "› Run /review\n84% context left · ? for shortcuts\n\ngpt-5.3-codex default · 16% used · ~/path · cx-wt"
	state := ParsePaneState("cx", captured)
	if state.ContextPct != 16 {
		t.Fatalf("expected context_pct=16 from statusline (used), got %d", state.ContextPct)
	}
}

func TestParsePaneStateCXStatuslineNoContextSegment(t *testing.T) {
	// CX statusline with context-used segment missing — should return -1
	captured := "some output\n›\n\ngpt-5.3-codex · ~/path · cx-wt"
	state := ParsePaneState("cx", captured)
	if state.ContextPct != -1 {
		t.Fatalf("expected context_pct=-1 when statusline has no used segment, got %d", state.ContextPct)
	}
}

func TestParsePaneStateCXFalsePositiveUsed(t *testing.T) {
	// "N% used" in assistant output should NOT match (no · before and after)
	captured := "cache 40% used\ndisk 92% used\n›\n\ngpt-5.3-codex · 16% used · ~/path · cx-wt"
	state := ParsePaneState("cx", captured)
	if state.ContextPct != 16 {
		t.Fatalf("expected context_pct=16 from statusline, not false match from output, got %d", state.ContextPct)
	}
}

func TestParsePaneStateCXStatuslineOnly(t *testing.T) {
	// CX idle with only statusline visible (no footer) — statusline is now primary source
	captured := "some output\n›\n\ngpt-5.3-codex default · 16% used · ~/path"
	state := ParsePaneState("cx", captured)
	if state.ContextPct != 16 {
		t.Fatalf("expected context_pct=16 from statusline (used), got %d", state.ContextPct)
	}
}

func writeSidecar(t *testing.T, dir, role string, td *TelemetryData) {
	t.Helper()
	data, err := json.Marshal(td)
	if err != nil {
		t.Fatalf("marshal sidecar: %v", err)
	}
	path := filepath.Join(dir, fmt.Sprintf("telemetry-%s.json", role))
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("write sidecar: %v", err)
	}
}

func TestReadTelemetrySidecar(t *testing.T) {
	dir := t.TempDir()
	td := &TelemetryData{
		Role:         "cc",
		Timestamp:    time.Now().Unix(),
		ContextPct:   14.5,
		ModelID:      "claude-opus-4-6",
		ModelDisplay: "Opus 4.6",
		SessionID:    "abc-123",
		CostUSD:      1.47,
		DurationMS:   342000,
		TokensIn:     45000,
		TokensOut:    12000,
		LinesAdded:   150,
		LinesRemoved: 42,
	}
	writeSidecar(t, dir, "cc", td)

	got, err := ReadTelemetrySidecar(dir, "cc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Role != "cc" {
		t.Fatalf("expected role=cc, got %s", got.Role)
	}
	if got.ContextPct != 14.5 {
		t.Fatalf("expected context_pct=14.5, got %f", got.ContextPct)
	}
	if got.ModelID != "claude-opus-4-6" {
		t.Fatalf("expected model_id=claude-opus-4-6, got %s", got.ModelID)
	}
	if got.SessionID != "abc-123" {
		t.Fatalf("expected session_id=abc-123, got %s", got.SessionID)
	}
	if got.CostUSD != 1.47 {
		t.Fatalf("expected cost_usd=1.47, got %f", got.CostUSD)
	}
	if got.TokensIn != 45000 {
		t.Fatalf("expected tokens_in=45000, got %d", got.TokensIn)
	}
	if got.TokensOut != 12000 {
		t.Fatalf("expected tokens_out=12000, got %d", got.TokensOut)
	}
}

func TestReadTelemetrySidecarMissing(t *testing.T) {
	dir := t.TempDir()
	got, err := ReadTelemetrySidecar(dir, "cc")
	if err == nil {
		t.Fatalf("expected error for missing sidecar, got nil")
	}
	if got != nil {
		t.Fatalf("expected nil data for missing sidecar")
	}
}

func TestReadTelemetrySidecarInvalid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "telemetry-cc.json")
	if err := os.WriteFile(path, []byte("not json"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := ReadTelemetrySidecar(dir, "cc")
	if err == nil {
		t.Fatalf("expected error for invalid JSON")
	}
	if got != nil {
		t.Fatalf("expected nil data for invalid JSON")
	}
}

func TestParsePaneStateWithTelemetryCC(t *testing.T) {
	dir := t.TempDir()
	td := &TelemetryData{
		Role:         "cc",
		Timestamp:    time.Now().Unix(),
		ContextPct:   14,
		ModelID:      "claude-opus-4-6",
		ModelDisplay: "Opus 4.6",
		SessionID:    "sess-001",
		CostUSD:      2.50,
		TokensIn:     50000,
		TokensOut:    15000,
	}
	writeSidecar(t, dir, "cc", td)

	captured := "work\n❯ \n──────────────────────────────────\n  ~/path | cc-wt | Opus 4.6 | ctx:14%\n  ⏵⏵ bypass"
	state := ParsePaneStateWithTelemetry("cc", captured, dir)

	if !state.Ready {
		t.Fatalf("expected ready=true")
	}
	if state.ContextPct != 14 {
		t.Fatalf("expected context_pct=14 from sidecar, got %d", state.ContextPct)
	}
	if state.ModelID != "claude-opus-4-6" {
		t.Fatalf("expected model_id=claude-opus-4-6, got %s", state.ModelID)
	}
	if state.SessionID != "sess-001" {
		t.Fatalf("expected session_id=sess-001, got %s", state.SessionID)
	}
	if state.CostUSD != 2.50 {
		t.Fatalf("expected cost_usd=2.50, got %f", state.CostUSD)
	}
	if state.TokensIn != 50000 {
		t.Fatalf("expected tokens_in=50000, got %d", state.TokensIn)
	}
	if state.TokensOut != 15000 {
		t.Fatalf("expected tokens_out=15000, got %d", state.TokensOut)
	}
	if state.IdentityVerified == nil || !*state.IdentityVerified {
		t.Fatalf("expected identity_verified=true")
	}
	if state.TelemetryAge < 0 || state.TelemetryAge > 2 {
		t.Fatalf("expected telemetry_age_s near 0, got %d", state.TelemetryAge)
	}
}

func TestParsePaneStateWithTelemetryStale(t *testing.T) {
	dir := t.TempDir()
	td := &TelemetryData{
		Role:       "cc",
		Timestamp:  time.Now().Unix() - 120, // 2 minutes old
		ContextPct: 14,
		ModelID:    "claude-opus-4-6",
	}
	writeSidecar(t, dir, "cc", td)

	captured := "work\n❯"
	state := ParsePaneStateWithTelemetry("cc", captured, dir)

	// Sidecar is stale — new fields should be zero
	if state.ModelID != "" {
		t.Fatalf("expected empty model_id for stale sidecar, got %s", state.ModelID)
	}
	if state.IdentityVerified != nil {
		t.Fatalf("expected nil identity_verified for stale sidecar")
	}
	// Base parsing still works
	if !state.Ready {
		t.Fatalf("expected ready=true from terminal parsing")
	}
}

func TestParsePaneStateWithTelemetryCX(t *testing.T) {
	dir := t.TempDir()
	// Even if a sidecar exists for cx, it should be ignored
	td := &TelemetryData{
		Role:       "cx",
		Timestamp:  time.Now().Unix(),
		ContextPct: 99,
		ModelID:    "gpt-5.3-codex",
	}
	writeSidecar(t, dir, "cx", td)

	captured := "some output\n›"
	state := ParsePaneStateWithTelemetry("cx", captured, dir)

	if state.ModelID != "" {
		t.Fatalf("expected empty model_id for CX (sidecar skipped), got %s", state.ModelID)
	}
	if state.IdentityVerified != nil {
		t.Fatalf("expected nil identity_verified for CX")
	}
	if !state.Ready {
		t.Fatalf("expected ready=true")
	}
}

func TestParsePaneStateWithTelemetryIdentityMismatch(t *testing.T) {
	dir := t.TempDir()
	// Sidecar says "oc" but we're querying "cc" — identity mismatch
	td := &TelemetryData{
		Role:       "oc",
		Timestamp:  time.Now().Unix(),
		ContextPct: 30,
		ModelID:    "claude-opus-4-6",
	}
	writeSidecar(t, dir, "cc", td)

	captured := "work\n❯"
	state := ParsePaneStateWithTelemetry("cc", captured, dir)

	if state.IdentityVerified == nil || *state.IdentityVerified {
		t.Fatalf("expected identity_verified=false for role mismatch")
	}
	// Data is still overlaid even on mismatch — caller decides what to do
	if state.ModelID != "claude-opus-4-6" {
		t.Fatalf("expected model_id to be set even on mismatch, got %s", state.ModelID)
	}
}
