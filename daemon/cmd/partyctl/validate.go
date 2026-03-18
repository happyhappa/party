package main

import "github.com/spf13/cobra"

func newValidateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "validate",
		Short: "Validate a party contract and environment",
		RunE:  notImplemented("validate"),
	}
}
