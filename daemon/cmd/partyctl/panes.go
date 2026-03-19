package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/norm/relay-daemon/internal/contract"
	"github.com/spf13/cobra"
)

func newPanesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "panes",
		Short: "Manage pane state",
	}
	cmd.AddCommand(newPanesWriteCmd())
	return cmd
}

func newPanesWriteCmd() *cobra.Command {
	var contractPath string
	var setPanes []string

	cmd := &cobra.Command{
		Use:   "write",
		Short: "Write the canonical pane map",
		Long: `Writes panes.json from the contract's Roles and Layout.

Pane IDs can come from the contract or be overridden via --set-pane flags.
Produces the v2 pane map format expected by relay-daemon.

Examples:
  partyctl panes write --set-pane oc=%0 --set-pane cc=%1 --set-pane cx=%2`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPanesWrite(cmd, contractPath, setPanes)
		},
	}
	cmd.Flags().StringVar(&contractPath, "contract-path", "", "path to contract JSON")
	cmd.Flags().StringArrayVar(&setPanes, "set-pane", nil, "override pane ID for a role (role=paneID)")
	return cmd
}

// paneMapV2 matches the schema expected by relay-daemon's config.LoadPaneMap.
type paneMapV2 struct {
	Panes        map[string]string `json:"panes"`
	Version      int               `json:"version"`
	RegisteredAt string            `json:"registered_at"`
}

func runPanesWrite(cmd *cobra.Command, contractPath string, setPanes []string) error {
	c, err := loadOrBuildContract(contractPath)
	if err != nil {
		return fmt.Errorf("load contract: %w", err)
	}

	// Apply --set-pane overrides to contract roles
	if err := applyPaneOverrides(c, setPanes); err != nil {
		return err
	}

	paneMap, err := buildPaneMap(c)
	if err != nil {
		return err
	}

	outPath := c.Paths.PaneMap
	if outPath == "" {
		outPath = filepath.Join(c.Paths.StateDir, "panes.json")
	}

	data, err := json.Marshal(paneMap)
	if err != nil {
		return fmt.Errorf("marshal pane map: %w", err)
	}
	data = append(data, '\n')

	if err := atomicWriteFile(outPath, data); err != nil {
		return err
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Wrote %s (%d panes: ", outPath, len(paneMap.Panes))
	first := true
	for role, paneID := range paneMap.Panes {
		if !first {
			fmt.Fprint(cmd.OutOrStdout(), ", ")
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s=%s", role, paneID)
		first = false
	}
	fmt.Fprintln(cmd.OutOrStdout(), ")")

	return nil
}

// applyPaneOverrides parses "role=paneID" strings and sets PaneID on the
// matching contract role. This lets bin/party pass tmux-assigned pane IDs
// without modifying the contract file on disk.
func applyPaneOverrides(c *contract.Contract, setPanes []string) error {
	for _, sp := range setPanes {
		parts := strings.SplitN(sp, "=", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return fmt.Errorf("invalid --set-pane value %q: expected role=paneID", sp)
		}
		roleName, paneID := parts[0], parts[1]
		found := false
		for i := range c.Roles {
			if c.Roles[i].Name == roleName {
				c.Roles[i].PaneID = paneID
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("--set-pane: unknown role %q", roleName)
		}
	}
	return nil
}

func buildPaneMap(c *contract.Contract) (*paneMapV2, error) {
	panes := make(map[string]string, len(c.Roles))

	for _, role := range c.Roles {
		if role.PaneID == "" {
			return nil, fmt.Errorf("role %q has no pane_id — pane IDs must be set before writing the pane map", role.Name)
		}
		if _, exists := panes[role.Name]; exists {
			return nil, fmt.Errorf("duplicate role %q in pane map", role.Name)
		}
		panes[role.Name] = role.PaneID
	}

	if len(panes) == 0 {
		return nil, fmt.Errorf("no roles defined in contract")
	}

	return &paneMapV2{
		Panes:        panes,
		Version:      c.Layout.SchemaVersion,
		RegisteredAt: time.Now().UTC().Format(time.RFC3339),
	}, nil
}

func atomicWriteFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create dir for %q: %w", path, err)
	}
	tmp, err := os.CreateTemp(dir, ".partyctl-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename %q: %w", path, err)
	}
	return nil
}
