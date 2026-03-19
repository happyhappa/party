package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		var ee *exitError
		if errors.As(err, &ee) {
			os.Exit(ee.code)
		}
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
	cmd.AddCommand(newHealthCmd())
	return cmd
}
