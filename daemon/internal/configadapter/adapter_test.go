package configadapter

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/norm/relay-daemon/internal/contract"
)

func TestReplaceForJSONAndTOML(t *testing.T) {
	for _, format := range []string{"json", "toml"} {
		t.Run(format, func(t *testing.T) {
			path := writeFixture(t, format, map[string]interface{}{
				"tui": map[string]interface{}{
					"enabled": false,
				},
			})

			adapter := mustLoadAdapter(t, format, path)
			value := true
			if err := adapter.Apply([]contract.ConfigMutationSpec{{
				Path:      "tui.enabled",
				Action:    "replace",
				ValueType: "bool",
				BoolValue: &value,
			}}); err != nil {
				t.Fatalf("Apply: %v", err)
			}
			if err := adapter.Write(path); err != nil {
				t.Fatalf("Write: %v", err)
			}

			reloaded := mustLoadAdapter(t, format, path)
			got, err := reloaded.Get("tui.enabled")
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if got != true {
				t.Fatalf("enabled = %v, want true", got)
			}
		})
	}
}

func TestEnsureExistsForJSONAndTOML(t *testing.T) {
	for _, format := range []string{"json", "toml"} {
		t.Run(format, func(t *testing.T) {
			path := writeFixture(t, format, map[string]interface{}{})
			adapter := mustLoadAdapter(t, format, path)

			if err := adapter.Apply([]contract.ConfigMutationSpec{{
				Path:          "telemetry.identity_key",
				Action:        "ensure_exists",
				ValueType:     "string",
				StringValue:   "relay",
				CreateParents: true,
			}}); err != nil {
				t.Fatalf("Apply create: %v", err)
			}
			if err := adapter.Apply([]contract.ConfigMutationSpec{{
				Path:          "telemetry.identity_key",
				Action:        "ensure_exists",
				ValueType:     "string",
				StringValue:   "ignored",
				CreateParents: true,
			}}); err != nil {
				t.Fatalf("Apply preserve: %v", err)
			}

			got, err := adapter.Get("telemetry.identity_key")
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if got != "relay" {
				t.Fatalf("identity_key = %v, want relay", got)
			}
		})
	}
}

func TestMergeUnionOrderingAndDedupForJSONAndTOML(t *testing.T) {
	for _, format := range []string{"json", "toml"} {
		t.Run(format, func(t *testing.T) {
			path := writeFixture(t, format, map[string]interface{}{
				"shell_environment_policy": map[string]interface{}{
					"include_only": []string{"PATH", "HOME", "PATH"},
				},
			})
			adapter := mustLoadAdapter(t, format, path)

			if err := adapter.Apply([]contract.ConfigMutationSpec{{
				Path:          "shell_environment_policy.include_only",
				Action:        "merge_union",
				ValueType:     "string_array",
				ArrayValue:    []string{"HOME", "CODEX_HOME", "PATH", "BEADS_DIR", "CODEX_HOME"},
				CreateParents: true,
			}}); err != nil {
				t.Fatalf("Apply: %v", err)
			}

			got, err := adapter.Get("shell_environment_policy.include_only")
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			want := []string{"PATH", "HOME", "CODEX_HOME", "BEADS_DIR"}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("include_only = %#v, want %#v", got, want)
			}
		})
	}
}

func TestEnsureTableCreateParentsForJSONAndTOML(t *testing.T) {
	for _, format := range []string{"json", "toml"} {
		t.Run(format, func(t *testing.T) {
			path := writeFixture(t, format, map[string]interface{}{})
			adapter := mustLoadAdapter(t, format, path)

			if err := adapter.Apply([]contract.ConfigMutationSpec{{
				Path:          "services.api.auth",
				Action:        "ensure_table",
				ValueType:     "object",
				CreateParents: true,
			}}); err != nil {
				t.Fatalf("Apply: %v", err)
			}

			got, err := adapter.Get("services.api.auth")
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			table, ok := got.(map[string]interface{})
			if !ok {
				t.Fatalf("auth table type = %T", got)
			}
			if len(table) != 0 {
				t.Fatalf("auth table = %#v, want empty table", table)
			}
		})
	}
}

func TestDryRunDoesNotModifyFileForJSONAndTOML(t *testing.T) {
	for _, format := range []string{"json", "toml"} {
		t.Run(format, func(t *testing.T) {
			path := writeFixture(t, format, map[string]interface{}{
				"telemetry": map[string]interface{}{
					"context_key": "old",
				},
			})
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("ReadFile before: %v", err)
			}

			adapter := mustLoadAdapter(t, format, path)
			results, err := adapter.DryRun([]contract.ConfigMutationSpec{{
				Path:          "telemetry.context_key",
				Action:        "replace",
				ValueType:     "string",
				StringValue:   "new",
				CreateParents: true,
			}})
			if err != nil {
				t.Fatalf("DryRun: %v", err)
			}
			if len(results) != 1 {
				t.Fatalf("results = %d, want 1", len(results))
			}
			if !results[0].Changed || results[0].OldValue != "old" || results[0].NewValue != "new" {
				t.Fatalf("unexpected result: %#v", results[0])
			}

			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("ReadFile after: %v", err)
			}
			if string(after) != string(before) {
				t.Fatal("dry run modified file contents")
			}

			got, err := adapter.Get("telemetry.context_key")
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if got != "old" {
				t.Fatalf("adapter state = %v, want old", got)
			}
		})
	}
}

func TestLoadFileMissingOptionalCreatesManagedFields(t *testing.T) {
	for _, format := range []string{"json", "toml"} {
		t.Run(format, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config."+format)
			spec := contract.ConfigFileSpec{
				Format:   format,
				Path:     path,
				Required: false,
			}

			adapter, err := LoadFile(spec)
			if err != nil {
				t.Fatalf("LoadFile optional: %v", err)
			}
			if err := adapter.Apply([]contract.ConfigMutationSpec{
				{
					Path:          "tui",
					Action:        "ensure_table",
					ValueType:     "object",
					CreateParents: true,
				},
				{
					Path:          "tui.status_line",
					Action:        "replace",
					ValueType:     "string_array",
					ArrayValue:    []string{"model-with-reasoning", "context-used"},
					CreateParents: true,
				},
			}); err != nil {
				t.Fatalf("Apply: %v", err)
			}
			if err := adapter.Write(path); err != nil {
				t.Fatalf("Write: %v", err)
			}

			reloaded := mustLoadAdapter(t, format, path)
			got, err := reloaded.Get("tui.status_line")
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			want := []string{"model-with-reasoning", "context-used"}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("status_line = %#v, want %#v", got, want)
			}
			if _, err := reloaded.Get("shell_environment_policy"); err == nil {
				t.Fatal("unexpected unmanaged field")
			}
		})
	}
}

func TestLoadFileMissingRequiredReturnsError(t *testing.T) {
	for _, format := range []string{"json", "toml"} {
		t.Run(format, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "missing."+format)
			_, err := LoadFile(contract.ConfigFileSpec{
				Format:   format,
				Path:     path,
				Required: true,
			})
			if err == nil {
				t.Fatal("expected error for missing required file")
			}
		})
	}
}

func TestTOMLApplyCreatesBackupBeforeMutation(t *testing.T) {
	path := writeFixture(t, "toml", map[string]interface{}{
		"telemetry": map[string]interface{}{
			"context_key": "old",
		},
	})
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile original: %v", err)
	}

	adapter := mustLoadAdapter(t, "toml", path)
	if err := adapter.Apply([]contract.ConfigMutationSpec{{
		Path:          "telemetry.context_key",
		Action:        "replace",
		ValueType:     "string",
		StringValue:   "new",
		CreateParents: true,
	}}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	backup, err := os.ReadFile(path + ".bak")
	if err != nil {
		t.Fatalf("ReadFile backup: %v", err)
	}
	if string(backup) != string(original) {
		t.Fatal("backup contents differ from original")
	}
}

func mustLoadAdapter(t *testing.T, format, path string) ConfigAdapter {
	t.Helper()
	adapter, err := NewAdapter(format)
	if err != nil {
		t.Fatalf("NewAdapter(%q): %v", format, err)
	}
	err = adapter.Load(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Load(%q): %v", path, err)
	}
	return adapter
}

func writeFixture(t *testing.T, format string, doc map[string]interface{}) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config."+format)
	adapter, err := NewAdapter(format)
	if err != nil {
		t.Fatalf("NewAdapter(%q): %v", format, err)
	}
	if err := adapter.Load(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Load(%q): %v", path, err)
	}
	base := adapter.(*baseAdapter)
	base.doc = doc
	if err := adapter.Write(path); err != nil {
		t.Fatalf("Write(%q): %v", path, err)
	}
	return path
}
