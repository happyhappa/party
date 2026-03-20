package main

import (
	"fmt"
	"time"

	"github.com/norm/relay-daemon/internal/recycle"
	"github.com/spf13/cobra"
)

func newStopCmd() *cobra.Command {
	var contractPath string
	var force bool

	cmd := &cobra.Command{
		Use:   "stop <role>",
		Short: "Stop an agent gracefully or forcefully",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStop(cmd, contractPath, args[0], force, time.Now().UTC())
		},
	}
	cmd.Flags().StringVar(&contractPath, "contract-path", "", "path to contract JSON")
	cmd.Flags().BoolVar(&force, "force", false, "skip graceful exit and go straight to SIGKILL")
	return cmd
}

func runStop(cmd *cobra.Command, contractPath, role string, force bool, now time.Time) error {
	c, roleSpec, toolSpec, err := loadContractRoleAndTool(contractPath, role)
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

	if state.AgentPID > 0 {
		// Preserve pane on process exit so pane ID survives for later start
		_ = tmuxSetOptionFunc(paneID, "remain-on-exit", "on")

		if !force {
			if err := sendExitCommand(paneID, toolSpec.Recycle.ExitCommand); err != nil {
				return fmt.Errorf("send exit command: %w", err)
			}
			if err := gracefulKillFunc(state.AgentPID, toolSpec.Recycle.GracePeriod.Duration); err != nil {
				return err
			}
		} else {
			if err := forceKillPIDFunc(state.AgentPID); err != nil {
				return err
			}
		}
	}

	state.State = recycle.StateReady
	state.EnteredAt = now
	state.AgentPID = 0
	state.Error = ""
	state.FailureCount = 0
	state.RecycleReason = ""
	if err := state.Save(c.Paths.StateDir, roleSpec.Name); err != nil {
		return fmt.Errorf("save recycle state: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Stopped %s\n", roleSpec.Name)
	return nil
}
