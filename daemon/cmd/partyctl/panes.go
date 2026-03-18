package main

import "github.com/spf13/cobra"

func newPanesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "panes",
		Short: "Manage pane state",
	}
	cmd.AddCommand(newPanesWriteCmd())
	return cmd
}

func newPanesWriteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "write",
		Short: "Write the canonical pane map",
		RunE:  notImplemented("panes write"),
	}
}
