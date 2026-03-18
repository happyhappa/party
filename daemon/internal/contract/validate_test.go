package contract

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateNilContract(t *testing.T) {
	r := Validate(nil)
	if r.Valid {
		t.Fatal("expected invalid for nil contract")
	}
	if len(r.Errors) != 1 {
		t.Fatalf("expected 1 error, got %d", len(r.Errors))
	}
}

func TestValidateRoleToolRef(t *testing.T) {
	c := minimalContract(t)
	c.Roles = []RoleSpec{
		{Name: "oc", Tool: "nonexistent"},
	}
	r := Validate(c)
	found := findError(r, "tool_exists")
	if found == nil {
		t.Fatal("expected tool_exists error for unknown tool reference")
	}
}

func TestValidateDuplicateRole(t *testing.T) {
	c := minimalContract(t)
	c.Roles = append(c.Roles, RoleSpec{Name: "oc", Tool: "claude_code"})
	r := Validate(c)
	found := findError(r, "unique")
	if found == nil {
		t.Fatal("expected unique error for duplicate role")
	}
}

func TestValidateLayoutRefsMissingRole(t *testing.T) {
	c := minimalContract(t)
	c.Layout.Panes = []PaneLayoutSpec{
		{Role: "nonexistent", Position: "left"},
	}
	r := Validate(c)
	found := findError(r, "role_exists")
	if found == nil {
		t.Fatal("expected role_exists error for undefined layout role")
	}
}

func TestValidateUnknownStrategy(t *testing.T) {
	c := minimalContract(t)
	tool := c.Tools["claude_code"]
	tool.PaneParser.Strategy = "magic_scan"
	c.Tools["claude_code"] = tool
	r := Validate(c)
	found := findError(r, "valid_strategy")
	if found == nil {
		t.Fatal("expected valid_strategy error for unknown strategy")
	}
}

func TestValidateUnknownReadyPolicy(t *testing.T) {
	c := minimalContract(t)
	tool := c.Tools["claude_code"]
	tool.PaneParser.ReadyPolicy = "always_ready"
	c.Tools["claude_code"] = tool
	r := Validate(c)
	found := findError(r, "valid_policy")
	if found == nil {
		t.Fatal("expected valid_policy error for unknown ready policy")
	}
}

func TestValidateUnknownMutationAction(t *testing.T) {
	c := minimalContract(t)
	tool := c.Tools["claude_code"]
	tool.ConfigFiles = []ConfigFileSpec{
		{
			Name:   "test",
			Format: "json",
			Path:   "/tmp/test.json",
			Mutations: []ConfigMutationSpec{
				{Path: "foo", Action: "delete_all"},
			},
		},
	}
	c.Tools["claude_code"] = tool
	r := Validate(c)
	found := findError(r, "valid_action")
	if found == nil {
		t.Fatal("expected valid_action error for unknown mutation action")
	}
}

func TestValidateValidContract(t *testing.T) {
	c := minimalContract(t)
	r := Validate(c)
	// Filter out binary_exists errors since test env may not have claude/codex
	var realErrors []ValidationError
	for _, e := range r.Errors {
		if e.Check == "binary_exists" {
			continue
		}
		realErrors = append(realErrors, e)
	}
	if len(realErrors) > 0 {
		t.Fatalf("expected no non-binary errors, got: %v", realErrors)
	}
}

func TestValidateFormatHuman(t *testing.T) {
	r := &ValidationResult{Valid: true}
	if r.FormatHuman() != "Contract is valid." {
		t.Fatalf("unexpected output: %q", r.FormatHuman())
	}

	r = &ValidationResult{
		Valid: false,
		Errors: []ValidationError{
			{Field: "project.root_dir", Check: "dir_exists", Message: "not found"},
		},
	}
	out := r.FormatHuman()
	if out == "Contract is valid." {
		t.Fatal("expected failure message")
	}
}

func TestValidateWritableDir(t *testing.T) {
	c := minimalContract(t)
	c.Paths.StateDir = "/nonexistent/path"
	r := Validate(c)
	found := findError(r, "dir_exists")
	if found == nil {
		t.Fatal("expected dir_exists error for nonexistent state dir")
	}
}

// minimalContract creates a valid contract pointing to temp directories.
func minimalContract(t *testing.T) *Contract {
	t.Helper()
	root := t.TempDir()
	stateDir := filepath.Join(root, "state")
	logDir := filepath.Join(root, "log")
	inboxDir := filepath.Join(root, "outbox")
	mainDir := filepath.Join(root, "main")
	wtCC := filepath.Join(root, "cc-wt")
	wtCX := filepath.Join(root, "cx-wt")

	for _, d := range []string{stateDir, logDir, inboxDir, mainDir, wtCC, wtCX} {
		os.MkdirAll(d, 0o755)
	}

	return &Contract{
		Version: CurrentVersion,
		Project: ProjectSpec{
			Name:    "test",
			RootDir: root,
			MainDir: mainDir,
		},
		Paths: PathSpec{
			StateDir: stateDir,
			LogDir:   logDir,
			InboxDir: inboxDir,
		},
		Session: SessionSpec{Name: "party-test", WindowName: "main"},
		Roles: []RoleSpec{
			{Name: "oc", Tool: "claude_code", WorktreeDir: mainDir, PaneTitle: "OC", ExpectedProcs: []string{"claude"}},
			{Name: "cc", Tool: "claude_code", WorktreeDir: wtCC, PaneTitle: "CC", ExpectedProcs: []string{"claude"}},
			{Name: "cx", Tool: "codex", WorktreeDir: wtCX, PaneTitle: "CX", ExpectedProcs: []string{"codex"}},
		},
		Layout: LayoutSpec{
			SchemaVersion: 1,
			Panes: []PaneLayoutSpec{
				{Role: "oc", Position: "left"},
				{Role: "cc", Position: "right-top"},
				{Role: "cx", Position: "right-bottom"},
			},
		},
		Tools: map[string]AgentToolSpec{
			"claude_code": {
				Name:    "claude_code",
				Launch:  CommandSpec{Command: "claude"},
				PaneParser: PaneParserSpec{
					Strategy:    "separator_scan",
					ReadyPolicy: "prompt_only",
					IdlePolicy:  "prompt_only",
				},
				Injection: InjectionSpec{SlashCommandMode: "bare"},
			},
			"codex": {
				Name:    "codex",
				Launch:  CommandSpec{Command: "codex"},
				PaneParser: PaneParserSpec{
					Strategy:    "last_nonempty_skip",
					ReadyPolicy: "prompt_and_no_footer",
					IdlePolicy:  "footer_or_prompt",
				},
				Injection: InjectionSpec{SlashCommandMode: "wrapped"},
			},
		},
	}
}

func findError(r *ValidationResult, check string) *ValidationError {
	for i := range r.Errors {
		if r.Errors[i].Check == check {
			return &r.Errors[i]
		}
	}
	return nil
}
