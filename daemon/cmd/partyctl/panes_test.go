package main

import (
	"testing"

	"github.com/norm/relay-daemon/internal/contract"
)

func testContract() *contract.Contract {
	return &contract.Contract{
		Version: 1,
		Roles: []contract.RoleSpec{
			{Name: "oc", Tool: "claude_code"},
			{Name: "cc", Tool: "claude_code"},
			{Name: "cx", Tool: "codex"},
		},
		Layout: contract.LayoutSpec{SchemaVersion: 1},
	}
}

func TestApplyPaneOverrides(t *testing.T) {
	t.Run("empty overrides is no-op", func(t *testing.T) {
		c := testContract()
		if err := applyPaneOverrides(c, nil); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		for _, r := range c.Roles {
			if r.PaneID != "" {
				t.Errorf("role %q should have empty PaneID, got %q", r.Name, r.PaneID)
			}
		}
	})

	t.Run("single override", func(t *testing.T) {
		c := testContract()
		if err := applyPaneOverrides(c, []string{"oc=%0"}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if c.Roles[0].PaneID != "%0" {
			t.Errorf("oc PaneID = %q, want %%0", c.Roles[0].PaneID)
		}
		if c.Roles[1].PaneID != "" {
			t.Errorf("cc PaneID should be empty, got %q", c.Roles[1].PaneID)
		}
	})

	t.Run("multiple overrides", func(t *testing.T) {
		c := testContract()
		err := applyPaneOverrides(c, []string{"oc=%0", "cc=%1", "cx=%2"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := map[string]string{"oc": "%0", "cc": "%1", "cx": "%2"}
		for _, r := range c.Roles {
			if r.PaneID != want[r.Name] {
				t.Errorf("role %q PaneID = %q, want %q", r.Name, r.PaneID, want[r.Name])
			}
		}
	})

	t.Run("unknown role", func(t *testing.T) {
		c := testContract()
		err := applyPaneOverrides(c, []string{"admin=%5"})
		if err == nil {
			t.Fatal("expected error for unknown role")
		}
	})

	t.Run("malformed value no equals", func(t *testing.T) {
		c := testContract()
		err := applyPaneOverrides(c, []string{"oc"})
		if err == nil {
			t.Fatal("expected error for malformed value")
		}
	})

	t.Run("malformed value empty role", func(t *testing.T) {
		c := testContract()
		err := applyPaneOverrides(c, []string{"=%0"})
		if err == nil {
			t.Fatal("expected error for empty role")
		}
	})

	t.Run("malformed value empty pane id", func(t *testing.T) {
		c := testContract()
		err := applyPaneOverrides(c, []string{"oc="})
		if err == nil {
			t.Fatal("expected error for empty pane id")
		}
	})
}

func TestBuildPaneMap(t *testing.T) {
	t.Run("valid contract", func(t *testing.T) {
		c := testContract()
		c.Roles[0].PaneID = "%0"
		c.Roles[1].PaneID = "%1"
		c.Roles[2].PaneID = "%2"

		pm, err := buildPaneMap(c)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(pm.Panes) != 3 {
			t.Errorf("expected 3 panes, got %d", len(pm.Panes))
		}
		if pm.Panes["oc"] != "%0" {
			t.Errorf("oc pane = %q, want %%0", pm.Panes["oc"])
		}
		if pm.Version != 1 {
			t.Errorf("version = %d, want 1", pm.Version)
		}
		if pm.RegisteredAt == "" {
			t.Error("registered_at should not be empty")
		}
	})

	t.Run("missing pane id", func(t *testing.T) {
		c := testContract()
		c.Roles[0].PaneID = "%0"
		// cc and cx have no pane IDs
		_, err := buildPaneMap(c)
		if err == nil {
			t.Fatal("expected error for missing pane id")
		}
	})

	t.Run("duplicate role names", func(t *testing.T) {
		c := &contract.Contract{
			Roles: []contract.RoleSpec{
				{Name: "oc", PaneID: "%0"},
				{Name: "oc", PaneID: "%1"},
			},
			Layout: contract.LayoutSpec{SchemaVersion: 1},
		}
		_, err := buildPaneMap(c)
		if err == nil {
			t.Fatal("expected error for duplicate role names")
		}
	})

	t.Run("no roles", func(t *testing.T) {
		c := &contract.Contract{
			Roles:  []contract.RoleSpec{},
			Layout: contract.LayoutSpec{SchemaVersion: 1},
		}
		_, err := buildPaneMap(c)
		if err == nil {
			t.Fatal("expected error for no roles")
		}
	})
}
