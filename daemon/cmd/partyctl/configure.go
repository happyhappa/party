package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/norm/relay-daemon/internal/configadapter"
	"github.com/norm/relay-daemon/internal/contract"
	"github.com/spf13/cobra"
)

func newConfigureCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "configure",
		Short: "Manage tool configuration",
	}
	cmd.AddCommand(newConfigureApplyCmd())
	return cmd
}

func newConfigureApplyCmd() *cobra.Command {
	var contractPath string
	var dryRun bool
	var format string

	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Apply managed tool configuration",
		Long:  "Applies config mutations declared in the contract to tool config files.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConfigureApply(cmd, contractPath, dryRun, format)
		},
	}
	cmd.Flags().StringVar(&contractPath, "contract-path", "", "path to contract JSON")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would change without writing")
	cmd.Flags().StringVar(&format, "format", "human", "output format: human or json")
	return cmd
}

// fileResult describes the effect of applying mutations to one config file.
type fileResult struct {
	File      string                       `json:"file"`
	Tool      string                       `json:"tool"`
	Format    string                       `json:"format"`
	Created   bool                         `json:"created"`
	Changed   bool                         `json:"changed"`
	Mutations []configadapter.MutationResult `json:"mutations,omitempty"`
	Warning   string                       `json:"warning,omitempty"`
}

func runConfigureApply(cmd *cobra.Command, contractPath string, dryRun bool, format string) error {
	c, err := loadOrBuildContract(contractPath)
	if err != nil {
		return fmt.Errorf("load contract: %w", err)
	}

	backupDir := filepath.Join(c.Paths.StateDir, "config-backups")

	// Collect unique config files across all tools (deduplicate by path)
	type configJob struct {
		toolName string
		spec     contract.ConfigFileSpec
	}
	seen := map[string]bool{}
	var jobs []configJob
	for _, tool := range c.Tools {
		for _, cf := range tool.ConfigFiles {
			if seen[cf.Path] {
				continue
			}
			seen[cf.Path] = true
			jobs = append(jobs, configJob{toolName: tool.Name, spec: cf})
		}
	}

	var results []fileResult
	var writtenPaths []string

	for _, job := range jobs {
		result, err := applyConfigFile(job.spec, job.toolName, backupDir, dryRun)
		if err != nil {
			// Rollback: restore previously written files from backups
			for _, wp := range writtenPaths {
				if restoreErr := restoreFromBackup(wp, backupDir); restoreErr != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "rollback failed for %s: %v\n", wp, restoreErr)
				}
			}
			return fmt.Errorf("config %s (%s): %w", job.spec.Name, job.spec.Path, err)
		}
		if result.Changed && !dryRun {
			writtenPaths = append(writtenPaths, job.spec.Path)
		}
		results = append(results, result)
	}

	switch format {
	case "json":
		data, err := json.MarshalIndent(results, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal results: %w", err)
		}
		fmt.Fprintln(cmd.OutOrStdout(), string(data))
	default:
		printHumanResults(cmd, results, dryRun)
	}

	return nil
}

func applyConfigFile(spec contract.ConfigFileSpec, toolName, backupDir string, dryRun bool) (fileResult, error) {
	result := fileResult{
		File:   spec.Path,
		Tool:   toolName,
		Format: spec.Format,
	}

	// Check for TOML comment loss warning
	if spec.Format == "toml" {
		if hasComments(spec.Path) {
			result.Warning = "TOML file has comments that may be lost during rewrite"
		}
	}

	adapter, err := configadapter.LoadFile(spec)
	if err != nil {
		return result, err
	}

	// Track if this is a new file
	if _, statErr := os.Stat(spec.Path); statErr != nil {
		result.Created = true
	}

	if dryRun {
		mutations, err := adapter.DryRun(spec.Mutations)
		if err != nil {
			return result, fmt.Errorf("dry run: %w", err)
		}
		result.Mutations = mutations
		for _, m := range mutations {
			if m.Changed {
				result.Changed = true
				break
			}
		}
		return result, nil
	}

	// Check if any mutations would actually change the file
	mutations, err := adapter.DryRun(spec.Mutations)
	if err != nil {
		return result, fmt.Errorf("dry run: %w", err)
	}
	result.Mutations = mutations

	anyChanged := false
	for _, m := range mutations {
		if m.Changed {
			anyChanged = true
			break
		}
	}

	if !anyChanged && !result.Created {
		return result, nil
	}

	// Backup before mutation (to state dir, not adjacent)
	if !result.Created {
		if err := backupToStateDir(spec.Path, backupDir); err != nil {
			return result, fmt.Errorf("backup: %w", err)
		}
	}

	// Re-load to get a fresh adapter (DryRun may have mutated internal state)
	adapter, err = configadapter.LoadFile(spec)
	if err != nil {
		return result, fmt.Errorf("reload for apply: %w", err)
	}

	if err := adapter.Apply(spec.Mutations); err != nil {
		return result, fmt.Errorf("apply: %w", err)
	}

	if err := adapter.Write(spec.Path); err != nil {
		return result, fmt.Errorf("write: %w", err)
	}

	result.Changed = true
	return result, nil
}

// backupName returns the backup path for a config file, using the full path
// with slashes replaced to avoid collisions between files with the same basename
// in different directories (e.g., ~/.claude/settings.json vs ~/.codex/settings.json).
func backupName(filePath, backupDir string) string {
	// Use full absolute path with separators replaced: /home/user/.claude/settings.json -> home-user-.claude-settings.json.bak
	clean := filepath.Clean(filePath)
	clean = strings.TrimPrefix(clean, string(filepath.Separator))
	safe := strings.ReplaceAll(clean, string(filepath.Separator), "-")
	return filepath.Join(backupDir, safe+".bak")
}

// backupToStateDir copies a config file to $RELAY_STATE_DIR/config-backups/
// if a backup doesn't already exist.
func backupToStateDir(filePath, backupDir string) error {
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return fmt.Errorf("create backup dir: %w", err)
	}

	backupPath := backupName(filePath, backupDir)

	// Skip if backup already exists (idempotent)
	if _, err := os.Stat(backupPath); err == nil {
		return nil
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}

	tmp := backupPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, backupPath)
}

// restoreFromBackup copies a backup file back to the original path.
// Used for rollback when a later config file fails during apply.
func restoreFromBackup(filePath, backupDir string) error {
	backupPath := backupName(filePath, backupDir)
	data, err := os.ReadFile(backupPath)
	if err != nil {
		return fmt.Errorf("read backup %q: %w", backupPath, err)
	}
	if err := os.WriteFile(filePath, data, 0o644); err != nil {
		return fmt.Errorf("restore %q: %w", filePath, err)
	}
	return nil
}

// hasComments checks if a file contains lines starting with # (TOML comments).
func hasComments(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		trimmed := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(trimmed, "#") {
			return true
		}
	}
	return false
}

func printHumanResults(cmd *cobra.Command, results []fileResult, dryRun bool) {
	prefix := ""
	if dryRun {
		prefix = "[dry-run] "
	}

	anyChanged := false
	for _, r := range results {
		if r.Changed || r.Created {
			anyChanged = true
		}
		status := "unchanged"
		if r.Created {
			status = "created"
		} else if r.Changed {
			status = "changed"
		}

		fmt.Fprintf(cmd.OutOrStdout(), "%s%s (%s/%s): %s\n", prefix, r.File, r.Tool, r.Format, status)

		if r.Warning != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "  ⚠ %s\n", r.Warning)
		}

		if dryRun && r.Mutations != nil {
			for _, m := range r.Mutations {
				marker := " "
				if m.Changed {
					marker = "~"
				}
				fmt.Fprintf(cmd.OutOrStdout(), "  %s %s [%s]\n", marker, m.Path, m.Action)
			}
		}
	}

	if !anyChanged {
		fmt.Fprintln(cmd.OutOrStdout(), "No changes needed.")
	}
}
