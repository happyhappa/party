package adminpane

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/norm/relay-daemon/internal/config"
)

// mockCall records a single tmux method call.
type mockCall struct {
	Method string
	Args   []string
}

// mockTmux records Run/SendToPane calls and returns configurable responses.
type mockTmux struct {
	mu    sync.Mutex
	calls []mockCall

	// captureOutput is returned for capture-pane Run() calls
	captureOutput string
	// captureErr is returned as error for capture-pane calls
	captureErr error
	// promptOutput is returned for prompt-detection capture-pane calls (-S -5)
	// Each call pops from front; when empty, returns "$" (prompt found)
	promptResponses []string
	promptIdx       int
	// sendToPaneErr is returned by SendToPane
	sendToPaneErr error
}

func newMockTmux() *mockTmux {
	return &mockTmux{
		captureOutput: "some admin output\nline 2\nline 3",
	}
}

func (m *mockTmux) Run(args ...string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, mockCall{Method: "Run", Args: args})

	// Dispatch based on first arg
	if len(args) > 0 && args[0] == "capture-pane" {
		// Check if this is a prompt-detection call (-S -5) or tail capture (-S -200)
		for _, a := range args {
			if a == "-200" {
				return m.captureOutput, m.captureErr
			}
			if a == "-5" {
				// Prompt detection
				if m.promptIdx < len(m.promptResponses) {
					resp := m.promptResponses[m.promptIdx]
					m.promptIdx++
					return resp, nil
				}
				// Default: prompt visible
				return "$ ", nil
			}
		}
	}
	return "", nil
}

func (m *mockTmux) SendToPane(pane, message string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, mockCall{Method: "SendToPane", Args: []string{pane, message}})
	return m.sendToPaneErr
}

func (m *mockTmux) getCalls() []mockCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]mockCall, len(m.calls))
	copy(cp, m.calls)
	return cp
}

func (m *mockTmux) callsWithMethod(method string) []mockCall {
	var result []mockCall
	for _, c := range m.getCalls() {
		if c.Method == method {
			result = append(result, c)
		}
	}
	return result
}

// --- Integration Tests ---

func TestRecycleFullSequence(t *testing.T) {
	mock := newMockTmux()
	// Prompt not ready for 2 polls, then ready
	mock.promptResponses = []string{
		"processing something...",
		"still working...",
	}

	adminDir := t.TempDir()
	cfg := config.Default()
	cfg.AdminRelaunchCmd = "claude --dangerously-skip-permissions"

	r := NewRecycler(mock, cfg, nil, "%2", adminDir)

	ctx := context.Background()
	err := r.Recycle(ctx)
	if err != nil {
		t.Fatalf("Recycle failed: %v", err)
	}

	calls := mock.getCalls()

	// Verify sequence: capture-pane -S -200, SendToPane /exit, capture-pane -S -5 (polls), SendToPane relaunch
	// 1. First Run call should be capture-pane with -S -200
	runCalls := mock.callsWithMethod("Run")
	if len(runCalls) < 1 {
		t.Fatal("expected at least 1 Run call for capture-pane")
	}
	firstRun := runCalls[0]
	foundS200 := false
	for _, a := range firstRun.Args {
		if a == "-200" {
			foundS200 = true
		}
	}
	if !foundS200 {
		t.Errorf("first Run call should be capture-pane with -S -200, got args: %v", firstRun.Args)
	}

	// 2. Verify last-life.txt was written
	lastLifePath := filepath.Join(adminDir, "state", "last-life.txt")
	data, err := os.ReadFile(lastLifePath)
	if err != nil {
		t.Fatalf("last-life.txt not written: %v", err)
	}
	if string(data) != "some admin output\nline 2\nline 3" {
		t.Errorf("last-life.txt content = %q, want capture output", string(data))
	}

	// 3. SendToPane calls: /exit first, then relaunch
	sendCalls := mock.callsWithMethod("SendToPane")
	if len(sendCalls) < 2 {
		t.Fatalf("expected at least 2 SendToPane calls, got %d", len(sendCalls))
	}
	if sendCalls[0].Args[0] != "%2" || sendCalls[0].Args[1] != "/exit" {
		t.Errorf("first SendToPane should be /exit to %%2, got: %v", sendCalls[0].Args)
	}
	if sendCalls[1].Args[0] != "%2" || sendCalls[1].Args[1] != "claude --dangerously-skip-permissions" {
		t.Errorf("second SendToPane should be relaunch cmd, got: %v", sendCalls[1].Args)
	}

	// 4. Prompt polling: should have 3 prompt-check calls (2 not ready + 1 ready)
	promptPolls := 0
	for _, c := range runCalls {
		for _, a := range c.Args {
			if a == "-5" {
				promptPolls++
			}
		}
	}
	if promptPolls != 3 {
		t.Errorf("expected 3 prompt polls (2 not ready + 1 ready), got %d", promptPolls)
	}

	// Verify overall call order
	var methodSeq []string
	for _, c := range calls {
		methodSeq = append(methodSeq, c.Method)
	}
	// Should be: Run(capture), SendToPane(/exit), Run(poll)..., SendToPane(relaunch)
	if len(methodSeq) < 4 {
		t.Errorf("expected at least 4 calls, got %d: %v", len(methodSeq), methodSeq)
	}
}

func TestRecycleCaptureFailureNonFatal(t *testing.T) {
	mock := newMockTmux()
	mock.captureErr = fmt.Errorf("pane not found")

	adminDir := t.TempDir()
	cfg := config.Default()
	cfg.AdminRelaunchCmd = "claude --dangerously-skip-permissions"

	r := NewRecycler(mock, cfg, nil, "%2", adminDir)

	err := r.Recycle(context.Background())
	if err != nil {
		t.Fatalf("Recycle should succeed even when capture fails, got: %v", err)
	}

	// last-life.txt should NOT exist (capture failed)
	lastLifePath := filepath.Join(adminDir, "state", "last-life.txt")
	if _, err := os.Stat(lastLifePath); err == nil {
		t.Error("last-life.txt should not exist when capture fails")
	}

	// /exit and relaunch should still have been called
	sendCalls := mock.callsWithMethod("SendToPane")
	if len(sendCalls) < 2 {
		t.Fatalf("expected 2 SendToPane calls despite capture failure, got %d", len(sendCalls))
	}
}

func TestRecyclePromptTimeout(t *testing.T) {
	mock := newMockTmux()
	// Never return a prompt — all responses are non-prompt output
	responses := make([]string, 60)
	for i := range responses {
		responses[i] = "still processing..."
	}
	mock.promptResponses = responses

	adminDir := t.TempDir()
	cfg := config.Default()
	cfg.AdminRelaunchCmd = "claude --dangerously-skip-permissions"

	r := NewRecycler(mock, cfg, nil, "%2", adminDir)

	// Use a short timeout context to avoid waiting 30s in tests
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	err := r.Recycle(ctx)
	if err == nil {
		t.Fatal("Recycle should fail when prompt never appears")
	}
	if !strings.Contains(err.Error(), "wait for prompt") {
		t.Errorf("error should mention prompt wait, got: %v", err)
	}
}

func TestRecycleExitInjectionFailure(t *testing.T) {
	mock := newMockTmux()
	mock.sendToPaneErr = fmt.Errorf("tmux send failed")

	adminDir := t.TempDir()
	cfg := config.Default()

	r := NewRecycler(mock, cfg, nil, "%2", adminDir)

	err := r.Recycle(context.Background())
	if err == nil {
		t.Fatal("Recycle should fail when /exit injection fails")
	}
	if !strings.Contains(err.Error(), "inject /exit") {
		t.Errorf("error should mention /exit injection, got: %v", err)
	}
}

func TestNeedsRecycleThenRecycleResetsCounters(t *testing.T) {
	mock := newMockTmux()
	adminDir := t.TempDir()

	cfg := config.Default()
	cfg.AdminRecycleCycles = 3
	cfg.AdminMaxUptime = 1 * time.Hour
	cfg.AdminRelaunchCmd = "claude --dangerously-skip-permissions"

	r := NewRecycler(mock, cfg, nil, "%2", adminDir)

	// Not yet at threshold
	if r.NeedsRecycle(2, time.Now()) {
		t.Error("2 cycles should not trigger recycle (threshold=3)")
	}

	// At threshold
	if !r.NeedsRecycle(3, time.Now()) {
		t.Error("3 cycles should trigger recycle (threshold=3)")
	}

	// Simulate the timer's counter reset flow
	timer := NewAdminTimer(noopInjector(), cfg, nil)
	timer.SetRecycler(r)

	// Manually simulate what happens after recycle
	err := r.Recycle(context.Background())
	if err != nil {
		t.Fatalf("Recycle failed: %v", err)
	}

	// Timer would reset counters
	timer.mu.Lock()
	timer.checkpointCycles = 0
	timer.startTime = time.Now()
	timer.mu.Unlock()

	// After reset, NeedsRecycle should be false
	if r.NeedsRecycle(timer.CheckpointCycles(), timer.StartTime()) {
		t.Error("after counter reset, NeedsRecycle should be false")
	}
}
