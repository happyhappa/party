package main

import (
	"fmt"
	"os"

	"github.com/norm/relay-daemon/internal/contract"
	"github.com/spf13/cobra"
)

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
		RunE:  runContractInit,
	}
}

func newContractShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Show a resolved party contract",
		RunE:  runContractShow,
	}
}

func runContractInit(cmd *cobra.Command, _ []string) error {
	stateDir := os.Getenv("RELAY_STATE_DIR")
	if stateDir == "" {
		return fmt.Errorf("RELAY_STATE_DIR is required for contract init")
	}
	c, err := contract.BuildContract(contract.InitOptions{
		StateDir: stateDir,
		ShareDir: os.Getenv("RELAY_SHARE_DIR"),
		MainDir:  os.Getenv("RELAY_MAIN_DIR"),
	})
	if err != nil {
		return err
	}
	if err := contract.WriteContract(c, c.Paths.ContractPath); err != nil {
		return err
	}
	cmd.Println(c.Paths.ContractPath)
	return nil
}

func runContractShow(cmd *cobra.Command, _ []string) error {
	c, err := contract.BuildContract(contract.InitOptions{
		StateDir: os.Getenv("RELAY_STATE_DIR"),
		ShareDir: os.Getenv("RELAY_SHARE_DIR"),
		MainDir:  os.Getenv("RELAY_MAIN_DIR"),
	})
	if err != nil {
		return err
	}
	data, err := contract.ShowContract(c)
	if err != nil {
		return err
	}
	fmt.Fprintln(cmd.OutOrStdout(), string(data))
	return nil
}
