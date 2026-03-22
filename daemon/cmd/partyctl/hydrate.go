package main

import (
	"fmt"

	"github.com/norm/relay-daemon/internal/recycle"
	"github.com/spf13/cobra"
)

func newHydrateCmd() *cobra.Command {
	var contractPath string

	cmd := &cobra.Command{
		Use:   "hydrate <role>",
		Short: "Inject tier 1 hydration into a running agent",
		Long:  "Assembles hydration payload (brief, log tail, inbox) and sends it via relay to the specified role. Does not relaunch the agent.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			projectName, _ := cmd.Flags().GetString("project")
			return runHydrate(cmd, contractPath, projectName, args[0])
		},
	}
	cmd.Flags().StringVar(&contractPath, "contract-path", "", "path to contract JSON")
	return cmd
}

func runHydrate(cmd *cobra.Command, contractPath, projectName, role string) error {
	c, roleSpec, _, err := loadContractRoleAndTool(contractPath, projectName, role)
	if err != nil {
		return err
	}

	state, err := recycle.LoadState(c.Paths.StateDir, roleSpec.Name)
	if err != nil {
		return err
	}

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
	if payload == nil {
		fmt.Fprintf(cmd.OutOrStdout(), "No hydration data available for %s\n", roleSpec.Name)
		return nil
	}

	if err := sendRelayDirectFunc(roleSpec.Name, payload.FormatForInjection()); err != nil {
		return fmt.Errorf("inject hydration for %s: %w", roleSpec.Name, err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Hydrated %s\n", roleSpec.Name)
	return nil
}
