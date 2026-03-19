package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/norm/relay-daemon/internal/contract"
	"github.com/norm/relay-daemon/internal/recycle"
)

func TestRunBriefJSON(t *testing.T) {
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeExecutable(t, filepath.Join(binDir, "party-jsonl-filter"), "#!/bin/sh\ncat\n")
	writeExecutable(t, filepath.Join(binDir, "codex"), "#!/bin/sh\nprintf 'brief output\\n'\n")
	writeExecutable(t, filepath.Join(binDir, "bd"), "#!/bin/sh\nprintf 'bead-123\\n'\n")

	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+oldPath)

	promptPath := filepath.Join(dir, "daemon", "scripts", "party-brief-prompt.txt")
	if err := os.MkdirAll(filepath.Dir(promptPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(promptPath, []byte("summarize"), 0o644); err != nil {
		t.Fatal(err)
	}

	transcriptPath := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(transcriptPath, []byte(strings.Repeat("x", 12000)), 0o644); err != nil {
		t.Fatal(err)
	}

	state := &recycle.RecycleState{
		State:          recycle.StateReady,
		EnteredAt:      time.Now().UTC(),
		SessionID:      "sess-1",
		TranscriptPath: transcriptPath,
	}
	if err := state.Save(dir, "oc"); err != nil {
		t.Fatal(err)
	}

	contractPath := filepath.Join(dir, "contract.json")
	c := &contract.Contract{
		Version: 1,
		Project: contract.ProjectSpec{Name: "demo", RootDir: dir},
		Paths: contract.PathSpec{
			StateDir: dir,
			BeadsDir: filepath.Join(dir, ".beads"),
		},
		Session: contract.SessionSpec{Name: "party-demo"},
		Roles:   []contract.RoleSpec{{Name: "oc", Tool: "claude_code"}},
		Tools: map[string]contract.AgentToolSpec{
			"claude_code": {
				Name: "claude_code",
				Recycle: contract.RecycleSpec{
					BriefMinDelta: 10240,
				},
			},
		},
	}
	writeContract(t, contractPath, c)

	oldWD, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(oldWD)

	cmd := newRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"brief", "oc", "--contract-path", contractPath, "--format", "json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v\n%s", err, out.String())
	}

	var got briefOutput
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("json output: %v\n%s", err, out.String())
	}
	if got.Role != "oc" || got.BeadID != "bead-123" || got.Offset == 0 {
		t.Fatalf("unexpected output: %+v", got)
	}

	updated, err := recycle.LoadState(dir, "oc")
	if err != nil {
		t.Fatal(err)
	}
	if updated.LastBriefOffset == 0 {
		t.Fatal("LastBriefOffset not updated")
	}
}

func writeExecutable(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeContract(t *testing.T, path string, c *contract.Contract) {
	t.Helper()
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}
