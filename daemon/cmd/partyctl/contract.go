package main

import (
	"fmt"
	"os"
	"text/tabwriter"
	"time"

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
	cmd.AddCommand(newContractRegisterCmd())
	cmd.AddCommand(newContractDeregisterCmd())
	cmd.AddCommand(newContractSessionsCmd())
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

func newContractRegisterCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "register",
		Short: "Register the current contract in the session registry",
		RunE:  runContractRegister,
	}
}

func newContractDeregisterCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "deregister [project-name]",
		Short: "Remove a project from the session registry",
		Args:  cobra.MaximumNArgs(1),
		RunE:  runContractDeregister,
	}
}

func newContractSessionsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "sessions",
		Short: "List all registered sessions",
		RunE:  runContractSessions,
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

	// Auto-register in session registry
	if err := contract.RegisterSession(contract.RegistryEntry{
		ProjectName:  c.Project.Name,
		ContractPath: c.Paths.ContractPath,
		ProjectRoot:  c.Project.RootDir,
		TmuxSession:  c.Session.Name,
	}); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: failed to register session: %v\n", err)
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

func runContractRegister(cmd *cobra.Command, _ []string) error {
	projectName, _ := cmd.Flags().GetString("project")
	contractPath, _ := cmd.Flags().GetString("contract-path")
	c, err := loadOrBuildContract(contractPath, projectName)
	if err != nil {
		return fmt.Errorf("load contract: %w", err)
	}

	entry := contract.RegistryEntry{
		ProjectName:  c.Project.Name,
		ContractPath: c.Paths.ContractPath,
		ProjectRoot:  c.Project.RootDir,
		TmuxSession:  c.Session.Name,
	}
	if err := contract.RegisterSession(entry); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Registered session %q (%s)\n", entry.ProjectName, entry.ContractPath)
	return nil
}

func runContractDeregister(cmd *cobra.Command, args []string) error {
	var projectName string
	if len(args) > 0 {
		projectName = args[0]
	} else {
		// Derive from contract
		flagProject, _ := cmd.Flags().GetString("project")
		contractPath, _ := cmd.Flags().GetString("contract-path")
		c, err := loadOrBuildContract(contractPath, flagProject)
		if err != nil {
			return fmt.Errorf("load contract to determine project name: %w (pass project name as argument instead)", err)
		}
		projectName = c.Project.Name
	}

	if err := contract.DeregisterSession(projectName); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Deregistered session %q\n", projectName)
	return nil
}

func runContractSessions(cmd *cobra.Command, _ []string) error {
	sessions, err := contract.ListSessions()
	if err != nil {
		return err
	}
	if len(sessions) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No registered sessions.")
		return nil
	}

	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "PROJECT\tSESSION\tCONTRACT\tUPDATED")
	for _, s := range sessions {
		updated := s.UpdatedAt.Format(time.RFC3339)
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", s.ProjectName, s.TmuxSession, s.ContractPath, updated)
	}
	return w.Flush()
}
