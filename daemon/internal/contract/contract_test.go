package contract

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultContractRoundTrip(t *testing.T) {
	c := DefaultContract("/tmp/project", "/tmp/project/main")
	c.GeneratedAt = time.Unix(123, 0).UTC()
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var roundTrip Contract
	if err := json.Unmarshal(data, &roundTrip); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if roundTrip.Version != CurrentVersion {
		t.Fatalf("version = %d, want %d", roundTrip.Version, CurrentVersion)
	}
	if len(roundTrip.Roles) != 3 {
		t.Fatalf("roles = %d, want 3", len(roundTrip.Roles))
	}
	if len(roundTrip.Tools) != 2 {
		t.Fatalf("tools = %d, want 2", len(roundTrip.Tools))
	}
}

func TestExpandPathsUnknownVariable(t *testing.T) {
	c := &Contract{
		Version: 1,
		Paths: PathSpec{
			ShareDir: "{{.unknown_value}}/relay",
		},
		Session: SessionSpec{Name: "party"},
		Project: ProjectSpec{Name: "p", RootDir: "/tmp/p"},
	}
	err := ExpandPaths(c, map[string]string{"home": "/tmp"})
	if err == nil {
		t.Fatal("expected error for unknown variable")
	}
}

func TestDurationRoundTrip(t *testing.T) {
	type sample struct {
		Value Duration `json:"value"`
	}
	in := sample{Value: Duration{Duration: 5*time.Minute + 500*time.Millisecond}}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out sample
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Value.Duration != in.Value.Duration {
		t.Fatalf("duration = %v, want %v", out.Value.Duration, in.Value.Duration)
	}
}

func TestBuildContractExpandsTemplates(t *testing.T) {
	projectRoot := t.TempDir()
	c, err := BuildContract(InitOptions{
		ProjectRoot: projectRoot,
		MainDir:     filepath.Join(projectRoot, "main"),
	})
	if err != nil {
		t.Fatalf("BuildContract: %v", err)
	}
	if c.Paths.ContractPath == "" {
		t.Fatal("expected contract path")
	}
	if c.Session.Name == "" || c.Session.Name == "party-{{.project_name}}" {
		t.Fatalf("session name not expanded: %q", c.Session.Name)
	}
}
