package main

import "github.com/spf13/cobra"

func newContractCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "contract",
		Short: "Manage party contracts",
	}
	cmd.AddCommand(newContractInitCmd())
	cmd.AddCommand(newContractShowCmd())
	return cmd
}

func newContractInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Initialize a party contract",
		RunE:  notImplemented("contract init"),
	}
}

func newContractShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Show a resolved party contract",
		RunE:  notImplemented("contract show"),
	}
}
