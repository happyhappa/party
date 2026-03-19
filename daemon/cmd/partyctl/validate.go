package main

import (
	"encoding/json"
	"fmt"

	"github.com/norm/relay-daemon/internal/contract"
	"github.com/spf13/cobra"
)

func newValidateCmd() *cobra.Command {
	var contractPath string
	var format string

	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Validate a party contract and environment",
		Long:  "Loads a contract and validates binaries, paths, configs, and internal consistency.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runValidate(cmd, contractPath, format)
		},
	}
	cmd.Flags().StringVar(&contractPath, "contract-path", "", "path to contract JSON (default: $RELAY_STATE_DIR/party-contract.json)")
	cmd.Flags().StringVar(&format, "format", "human", "output format: human or json")
	return cmd
}

func runValidate(cmd *cobra.Command, contractPath, format string) error {
	c, err := loadOrBuildContract(contractPath)
	if err != nil {
		if format == "json" {
			data, _ := json.MarshalIndent(map[string]any{
				"valid":  false,
				"errors": []map[string]string{{"field": "contract", "check": "load", "message": err.Error()}},
			}, "", "  ")
			fmt.Fprintln(cmd.OutOrStdout(), string(data))
		} else {
			fmt.Fprintf(cmd.ErrOrStderr(), "Failed to load contract: %v\n", err)
		}
		return &exitError{code: 2}
	}

	result := contract.Validate(c)

	switch format {
	case "json":
		data, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal result: %w", err)
		}
		fmt.Fprintln(cmd.OutOrStdout(), string(data))
	default:
		fmt.Fprint(cmd.OutOrStdout(), result.FormatHuman())
	}

	if !result.Valid {
		return &exitError{code: 1}
	}
	return nil
}
