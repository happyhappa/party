package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "partyctl",
		Short: "Party control plane CLI",
	}
	cmd.AddCommand(newContractCmd())
	cmd.AddCommand(newValidateCmd())
	cmd.AddCommand(newConfigureCmd())
	cmd.AddCommand(newPanesCmd())
	return cmd
}
