package summarywatcher

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.ChunkSizeTokens != 4000 {
		t.Errorf("expected ChunkSizeTokens=4000, got %d", cfg.ChunkSizeTokens)
	}
	if cfg.OverlapPercent != 10 {
		t.Errorf("expected OverlapPercent=10, got %d", cfg.OverlapPercent)
	}
	if cfg.ChunksPerRollup != 5 {
		t.Errorf("expected ChunksPerRollup=5, got %d", cfg.ChunksPerRollup)
	}
}

func TestFindMessageBoundary(t *testing.T) {
	// Create temp file with JSONL content
	dir := t.TempDir()
	path := filepath.Join(dir, "test.jsonl")

	content := `{"line": 1}
{"line": 2}
{"line": 3}
{"line": 4}
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	w := New(&Config{SessionLogPath: path})

	// Find boundary near middle of file
	boundary, err := w.findMessageBoundary(path, 30)
	if err != nil {
		t.Fatal(err)
	}

	// Should be at a newline boundary
	if boundary > int64(len(content)) {
		t.Errorf("boundary %d exceeds content length %d", boundary, len(content))
	}

	// Read up to boundary and verify it ends cleanly
	data, _ := os.ReadFile(path)
	portion := string(data[:boundary])
	if !strings.HasSuffix(portion, "\n") {
		t.Errorf("boundary should end at newline, got: %q", portion[len(portion)-10:])
	}
}

func TestExtractOverlapText(t *testing.T) {
	cfg := DefaultConfig()
	cfg.OverlapPercent = 20
	w := New(cfg)

	content := strings.Repeat("line of text\n", 100)
	overlap := w.extractOverlapText(content)

	// Overlap should be roughly 20% of content
	expectedMax := len(content) * 25 / 100 // Allow some variance
	if len(overlap) > expectedMax {
		t.Errorf("overlap too large: %d > %d", len(overlap), expectedMax)
	}
	if len(overlap) == 0 {
		t.Error("overlap should not be empty")
	}
}

func TestExtractFileReferences(t *testing.T) {
	content := `
Working on main.go and config.yaml
Also modified internal/handler.ts
`
	files := extractFileReferences(content)

	expected := map[string]bool{
		"main.go":             true,
		"config.yaml":         true,
		"internal/handler.ts": true,
	}

	for _, f := range files {
		if !expected[f] {
			t.Errorf("unexpected file: %s", f)
		}
		delete(expected, f)
	}

	for f := range expected {
		t.Errorf("missing file: %s", f)
	}
}

func TestExtractFunctionNames(t *testing.T) {
	content := `
func main() {}
func (s *Server) Handle() {}
function doStuff() {}
def helper():
`
	funcs := extractFunctionNames(content)

	found := make(map[string]bool)
	for _, f := range funcs {
		found[f] = true
	}

	if !found["main"] {
		t.Error("expected to find 'main'")
	}
	if !found["Handle"] {
		t.Error("expected to find 'Handle'")
	}
	if !found["doStuff"] {
		t.Error("expected to find 'doStuff'")
	}
	if !found["helper"] {
		t.Error("expected to find 'helper'")
	}
}

func TestExtractErrors(t *testing.T) {
	content := `
error: something went wrong
Failed: compilation error
FAIL test_example.go
`
	errors := extractErrors(content)

	if len(errors) == 0 {
		t.Error("expected to find errors")
	}

	hasError := false
	for _, e := range errors {
		if strings.Contains(e, "something went wrong") ||
			strings.Contains(e, "compilation") ||
			strings.Contains(e, "test_example") {
			hasError = true
			break
		}
	}
	if !hasError {
		t.Errorf("expected relevant error messages, got: %v", errors)
	}
}

func TestStatePersistence(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig()
	cfg.StateDir = dir
	cfg.Role = "test"

	w := New(cfg)
	w.lastByteOffset = 12345
	w.chunkCount = 3
	w.chunksSinceRollup = 2
	w.recentSummaries = []string{"summary1", "summary2"}

	// Save state
	if err := w.saveState(); err != nil {
		t.Fatal(err)
	}

	// Create new watcher and load state
	w2 := New(cfg)
	if err := w2.loadState(); err != nil {
		t.Fatal(err)
	}

	if w2.lastByteOffset != 12345 {
		t.Errorf("expected lastByteOffset=12345, got %d", w2.lastByteOffset)
	}
	if w2.chunkCount != 3 {
		t.Errorf("expected chunkCount=3, got %d", w2.chunkCount)
	}
	if w2.chunksSinceRollup != 2 {
		t.Errorf("expected chunksSinceRollup=2, got %d", w2.chunksSinceRollup)
	}
	if len(w2.recentSummaries) != 2 {
		t.Errorf("expected 2 recent summaries, got %d", len(w2.recentSummaries))
	}
}

func TestStatePersistenceAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig()
	cfg.StateDir = dir
	cfg.Role = "restart"

	w := New(cfg)
	w.lastByteOffset = 2048
	w.lastChunkEnd = 2048
	w.chunkCount = 2
	w.chunksSinceRollup = 1
	w.recentSummaries = []string{"summary-a"}

	if err := w.saveState(); err != nil {
		t.Fatalf("saveState: %v", err)
	}

	w2 := New(cfg)
	if err := w2.loadState(); err != nil {
		t.Fatalf("loadState: %v", err)
	}

	if w2.lastByteOffset != 2048 {
		t.Fatalf("expected lastByteOffset=2048, got %d", w2.lastByteOffset)
	}
	if w2.lastChunkEnd != 2048 {
		t.Fatalf("expected lastChunkEnd=2048, got %d", w2.lastChunkEnd)
	}
	if w2.chunkCount != 2 {
		t.Fatalf("expected chunkCount=2, got %d", w2.chunkCount)
	}
	if w2.chunksSinceRollup != 1 {
		t.Fatalf("expected chunksSinceRollup=1, got %d", w2.chunksSinceRollup)
	}
	if len(w2.recentSummaries) != 1 || w2.recentSummaries[0] != "summary-a" {
		t.Fatalf("expected recentSummaries restored, got %v", w2.recentSummaries)
	}
}

func TestHeuristicChunkSummary(t *testing.T) {
	w := New(nil)

	content := `
Working on main.go
func Initialize() { }
error: test failure
$ go build ./...
`
	summary := w.heuristicChunkSummary(content)

	if !strings.Contains(summary, "Heuristic") {
		t.Error("expected heuristic marker")
	}
	if !strings.Contains(summary, "main.go") {
		t.Error("expected file reference")
	}
	if !strings.Contains(summary, "Initialize") {
		t.Error("expected function name")
	}
}

func TestDedupe(t *testing.T) {
	input := []string{"a", "b", "a", "c", "", "b", "d"}
	result := dedupe(input)

	if len(result) != 4 {
		t.Errorf("expected 4 unique items, got %d", len(result))
	}

	expected := []string{"a", "b", "c", "d"}
	for i, v := range expected {
		if result[i] != v {
			t.Errorf("at %d: expected %q, got %q", i, v, result[i])
		}
	}
}
