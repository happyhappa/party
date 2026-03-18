package main

import "github.com/spf13/cobra"

func newConfigureCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "configure",
		Short: "Manage tool configuration",
	}
	cmd.AddCommand(newConfigureApplyCmd())
	return cmd
}

func newConfigureApplyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "apply",
		Short: "Apply managed tool configuration",
		RunE:  notImplemented("configure apply"),
	}
}
