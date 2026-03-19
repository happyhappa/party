package main

import (
	"fmt"
	"time"

	"github.com/norm/relay-daemon/internal/contract"
	"github.com/norm/relay-daemon/internal/recycle"
	"github.com/spf13/cobra"
)

func newStartCmd() *cobra.Command {
	var contractPath string
	var hydrate bool
	var setPanes []string

	cmd := &cobra.Command{
		Use:   "start <role>",
		Short: "Launch an agent in its tmux pane",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStart(cmd, contractPath, args[0], hydrate, setPanes, time.Now().UTC())
		},
	}
	cmd.Flags().StringVar(&contractPath, "contract-path", "", "path to contract JSON")
	cmd.Flags().BoolVar(&hydrate, "hydrate", false, "inject tier 1 hydration after launch")
	cmd.Flags().StringArrayVar(&setPanes, "set-pane", nil, "override pane ID for a role (role=paneID)")
	return cmd
}

func runStart(cmd *cobra.Command, contractPath, role string, hydrate bool, setPanes []string, now time.Time) error {
	c, roleSpec, toolSpec, err := loadContractRoleAndToolWithPanes(contractPath, role, setPanes)
	if err != nil {
		return err
	}

	lock, err := recycle.AcquireLock(c.Paths.StateDir, roleSpec.Name)
	if err != nil {
		return err
	}
	defer lock.Release()

	state, err := recycle.LoadState(c.Paths.StateDir, roleSpec.Name)
	if err != nil {
		return err
	}

	paneID, err := lookupPaneID(c, roleSpec.Name)
	if err != nil {
		return err
	}

	if err := relaunchRole(paneID, c, roleSpec, toolSpec); err != nil {
		return err
	}

	time.Sleep(1 * time.Second)
	pid := panePIDFunc(paneID)
	if pid <= 0 {
		return fmt.Errorf("failed to determine agent pid for pane %s", paneID)
	}

	state.State = recycle.StateReady
	state.EnteredAt = now
	state.AgentPID = pid
	state.Error = ""
	state.FailureCount = 0
	state.RecycleReason = ""
	if err := state.Save(c.Paths.StateDir, roleSpec.Name); err != nil {
		return fmt.Errorf("save recycle state: %w", err)
	}

	if hydrate {
		payload, err := assembleHydration(recycle.HydrationOptions{
			Role:           roleSpec.Name,
			PrevSessionID:  state.SessionID,
			TranscriptPath: state.TranscriptPath,
			BeadsDir:       c.Paths.BeadsDir,
			InboxDir:       c.Paths.InboxDir,
		})
		if err != nil {
			return fmt.Errorf("assemble hydration: %w", err)
		}
		if payload != nil {
			if err := sendRelayDirectFunc(roleSpec.Name, payload.FormatForInjection()); err != nil {
				return fmt.Errorf("inject hydration: %w", err)
			}
		}
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Started %s (pane %s, pid %d)\n", roleSpec.Name, paneID, pid)
	return nil
}

func loadContractRoleAndToolWithPanes(contractPath, role string, setPanes []string) (*contract.Contract, contract.RoleSpec, contract.AgentToolSpec, error) {
	c, err := loadOrBuildContract(contractPath)
	if err != nil {
		return nil, contract.RoleSpec{}, contract.AgentToolSpec{}, fmt.Errorf("load contract: %w", err)
	}
	if err := applyPaneOverrides(c, setPanes); err != nil {
		return nil, contract.RoleSpec{}, contract.AgentToolSpec{}, err
	}
	for _, roleSpec := range c.Roles {
		if roleSpec.Name == role {
			toolSpec, ok := c.Tools[roleSpec.Tool]
			if !ok {
				return nil, contract.RoleSpec{}, contract.AgentToolSpec{}, fmt.Errorf("role %q references unknown tool %q", role, roleSpec.Tool)
			}
			return c, roleSpec, toolSpec, nil
		}
	}
	return nil, contract.RoleSpec{}, contract.AgentToolSpec{}, fmt.Errorf("unknown role %q", role)
}
