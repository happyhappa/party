package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

	cmd := &cobra.Command{
		Use:   "write",
		Short: "Write the canonical pane map",
		Long: `Writes panes.json from the contract's Roles and Layout.

Each role must have a PaneID set (assigned by tmux at startup).
Produces the v2 pane map format expected by relay-daemon.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPanesWrite(cmd, contractPath)
		},
	}
	cmd.Flags().StringVar(&contractPath, "contract-path", "", "path to contract JSON")
	return cmd
}

// paneMapV2 matches the schema expected by relay-daemon's config.LoadPaneMap.
type paneMapV2 struct {
	Panes        map[string]string `json:"panes"`
	Version      int               `json:"version"`
	RegisteredAt string            `json:"registered_at"`
}

func runPanesWrite(cmd *cobra.Command, contractPath string) error {
	c, err := loadOrBuildContract(contractPath)
	if err != nil {
		return fmt.Errorf("load contract: %w", err)
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
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create dir for %q: %w", path, err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename %q: %w", path, err)
	}
	return nil
}
