package contract

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestRegisterAndList(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)

	if err := RegisterSession(RegistryEntry{
		ProjectName:  "zeta",
		ContractPath: "/tmp/zeta/party-contract.json",
		ProjectRoot:  "/tmp/zeta",
		TmuxSession:  "party-zeta",
		UpdatedAt:    time.Unix(10, 0).UTC(),
	}); err != nil {
		t.Fatalf("RegisterSession zeta: %v", err)
	}
	if err := RegisterSession(RegistryEntry{
		ProjectName:  "alpha",
		ContractPath: "/tmp/alpha/party-contract.json",
		ProjectRoot:  "/tmp/alpha",
		TmuxSession:  "party-alpha",
		UpdatedAt:    time.Unix(20, 0).UTC(),
	}); err != nil {
		t.Fatalf("RegisterSession alpha: %v", err)
	}

	got, err := ListSessions()
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	names := []string{got[0].ProjectName, got[1].ProjectName}
	if want := []string{"alpha", "zeta"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("names = %v, want %v", names, want)
	}
}

func TestDeregisterSession(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)

	if err := RegisterSession(RegistryEntry{
		ProjectName:  "demo",
		ContractPath: "/tmp/demo/party-contract.json",
		ProjectRoot:  "/tmp/demo",
	}); err != nil {
		t.Fatalf("RegisterSession: %v", err)
	}
	if err := DeregisterSession("demo"); err != nil {
		t.Fatalf("DeregisterSession: %v", err)
	}
	got, err := ListSessions()
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("len = %d, want 0", len(got))
	}
}

func TestDeregisterMissing(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)

	if err := DeregisterSession("missing"); err != nil {
		t.Fatalf("DeregisterSession: %v", err)
	}
}

func TestFindByExplicitPath(t *testing.T) {
	got, err := FindContractPath(FindOptions{
		ExplicitPath: "/tmp/explicit/party-contract.json",
	})
	if err != nil {
		t.Fatalf("FindContractPath: %v", err)
	}
	if want := "/tmp/explicit/party-contract.json"; got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}
}

func TestFindByRelayStateDir(t *testing.T) {
	stateDir := t.TempDir()
	contractPath := filepath.Join(stateDir, "party-contract.json")
	if err := os.WriteFile(contractPath, []byte("{}"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got, err := FindContractPath(FindOptions{
		RelayStateDir: stateDir,
	})
	if err != nil {
		t.Fatalf("FindContractPath: %v", err)
	}
	if got != contractPath {
		t.Fatalf("path = %q, want %q", got, contractPath)
	}
}

func TestFindByProjectName(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	projectRoot := t.TempDir()
	contractPath := writeRegistryContractFile(t, projectRoot)

	if err := RegisterSession(RegistryEntry{
		ProjectName:  "demo",
		ContractPath: contractPath,
		ProjectRoot:  projectRoot,
	}); err != nil {
		t.Fatalf("RegisterSession: %v", err)
	}

	got, err := FindContractPath(FindOptions{
		ProjectName: "demo",
	})
	if err != nil {
		t.Fatalf("FindContractPath: %v", err)
	}
	if want := contractPath; got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}
}

func TestFindByCWD(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)

	projectRoot := filepath.Join(t.TempDir(), "demo")
	cwd := filepath.Join(projectRoot, "cx-wt", "subdir")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	contractPath := writeRegistryContractFile(t, projectRoot)
	if err := RegisterSession(RegistryEntry{
		ProjectName:  "demo",
		ContractPath: contractPath,
		ProjectRoot:  projectRoot,
	}); err != nil {
		t.Fatalf("RegisterSession: %v", err)
	}

	got, err := FindContractPath(FindOptions{CWD: cwd})
	if err != nil {
		t.Fatalf("FindContractPath: %v", err)
	}
	if want := contractPath; got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}
}

func TestFindByCWDDeepestRoot(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)

	base := t.TempDir()
	rootA := filepath.Join(base, "project-a")
	rootB := filepath.Join(rootA, "subdir")
	cwd := filepath.Join(rootB, "child")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	contractPathA := writeRegistryContractFile(t, rootA)
	contractPathB := writeRegistryContractFile(t, rootB)
	for _, entry := range []RegistryEntry{
		{ProjectName: "a", ContractPath: contractPathA, ProjectRoot: rootA},
		{ProjectName: "b", ContractPath: contractPathB, ProjectRoot: rootB},
	} {
		if err := RegisterSession(entry); err != nil {
			t.Fatalf("RegisterSession %s: %v", entry.ProjectName, err)
		}
	}

	got, err := FindContractPath(FindOptions{CWD: cwd})
	if err != nil {
		t.Fatalf("FindContractPath: %v", err)
	}
	if want := contractPathB; got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}
}

func TestFindSingleSession(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	projectRoot := t.TempDir()
	contractPath := writeRegistryContractFile(t, projectRoot)

	if err := RegisterSession(RegistryEntry{
		ProjectName:  "solo",
		ContractPath: contractPath,
		ProjectRoot:  projectRoot,
	}); err != nil {
		t.Fatalf("RegisterSession: %v", err)
	}

	got, err := FindContractPath(FindOptions{CWD: "/tmp/outside"})
	if err != nil {
		t.Fatalf("FindContractPath: %v", err)
	}
	if want := contractPath; got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}
}

func TestFindMultipleNoMatch(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)

	alphaRoot := t.TempDir()
	betaRoot := t.TempDir()
	for _, entry := range []RegistryEntry{
		{ProjectName: "alpha", ContractPath: writeRegistryContractFile(t, alphaRoot), ProjectRoot: alphaRoot},
		{ProjectName: "beta", ContractPath: writeRegistryContractFile(t, betaRoot), ProjectRoot: betaRoot},
	} {
		if err := RegisterSession(entry); err != nil {
			t.Fatalf("RegisterSession %s: %v", entry.ProjectName, err)
		}
	}

	_, err := FindContractPath(FindOptions{CWD: "/tmp/outside"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "alpha") || !strings.Contains(err.Error(), "beta") {
		t.Fatalf("error = %q, want listed projects", err.Error())
	}
}

func TestFindWalkUpWithMultipleRegisteredProjects(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)

	for _, entry := range []RegistryEntry{
		{ProjectName: "alpha", ContractPath: "/tmp/alpha/party-contract.json", ProjectRoot: "/tmp/alpha"},
		{ProjectName: "beta", ContractPath: "/tmp/beta/party-contract.json", ProjectRoot: "/tmp/beta"},
	} {
		if err := RegisterSession(entry); err != nil {
			t.Fatalf("RegisterSession %s: %v", entry.ProjectName, err)
		}
	}

	projectRoot := t.TempDir()
	stateDir := filepath.Join(projectRoot, ".relay", "state")
	cwd := filepath.Join(projectRoot, "cx-wt", "subdir")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("MkdirAll stateDir: %v", err)
	}
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatalf("MkdirAll cwd: %v", err)
	}
	contractPath := filepath.Join(stateDir, "party-contract.json")
	if err := os.WriteFile(contractPath, []byte("{}"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got, err := FindContractPath(FindOptions{CWD: cwd})
	if err != nil {
		t.Fatalf("FindContractPath: %v", err)
	}
	if got != contractPath {
		t.Fatalf("path = %q, want %q", got, contractPath)
	}
}

func TestFindSkipsStaleRegistryEntry(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)

	projectRoot := t.TempDir()
	stateDir := filepath.Join(projectRoot, ".relay", "state")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("MkdirAll stateDir: %v", err)
	}
	liveContractPath := filepath.Join(stateDir, "party-contract.json")
	if err := os.WriteFile(liveContractPath, []byte("{}"), 0o644); err != nil {
		t.Fatalf("WriteFile live contract: %v", err)
	}

	if err := RegisterSession(RegistryEntry{
		ProjectName:  "stale",
		ContractPath: filepath.Join(t.TempDir(), "missing", "party-contract.json"),
		ProjectRoot:  projectRoot,
	}); err != nil {
		t.Fatalf("RegisterSession: %v", err)
	}

	got, err := FindContractPath(FindOptions{
		ProjectName: "stale",
		CWD:         projectRoot,
	})
	if err != nil {
		t.Fatalf("FindContractPath: %v", err)
	}
	if got != liveContractPath {
		t.Fatalf("path = %q, want %q", got, liveContractPath)
	}
}

func writeRegistryContractFile(t *testing.T, projectRoot string) string {
	t.Helper()
	stateDir := filepath.Join(projectRoot, ".relay", "state")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("MkdirAll stateDir: %v", err)
	}
	contractPath := filepath.Join(stateDir, "party-contract.json")
	if err := os.WriteFile(contractPath, []byte("{}"), 0o644); err != nil {
		t.Fatalf("WriteFile contract: %v", err)
	}
	return contractPath
}

func TestCwdWithinRoot(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "foo")
	cwd := filepath.Join(root, "bar")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatalf("MkdirAll cwd: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(base, "foo-bar"), 0o755); err != nil {
		t.Fatalf("MkdirAll foo-bar: %v", err)
	}
	linkRoot := filepath.Join(base, "foo-link")
	if err := os.Symlink(root, linkRoot); err != nil {
		t.Fatalf("Symlink root: %v", err)
	}
	linkCWD := filepath.Join(base, "bar-link")
	if err := os.Symlink(cwd, linkCWD); err != nil {
		t.Fatalf("Symlink cwd: %v", err)
	}

	cases := []struct {
		name string
		root string
		cwd  string
		want bool
	}{
		{name: "same dir", root: root, cwd: root, want: true},
		{name: "child dir", root: root, cwd: cwd, want: true},
		{name: "prefix only", root: root, cwd: filepath.Join(base, "foo-bar"), want: false},
		{name: "outside root", root: root, cwd: base, want: false},
		{name: "symlinked root", root: linkRoot, cwd: cwd, want: true},
		{name: "symlinked cwd", root: root, cwd: linkCWD, want: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := cwdWithinRoot(tc.root, tc.cwd); got != tc.want {
				t.Fatalf("cwdWithinRoot(%q, %q) = %v, want %v", tc.root, tc.cwd, got, tc.want)
			}
		})
	}
}
