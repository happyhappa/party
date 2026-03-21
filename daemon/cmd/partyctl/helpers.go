package main

import (
	"fmt"
	"os"

	"github.com/norm/relay-daemon/internal/contract"
)

// exitError signals a specific exit code without printing an error message.
type exitError struct {
	code int
}

func (e *exitError) Error() string {
	return fmt.Sprintf("exit %d", e.code)
}

// loadOrBuildContract resolves a contract using the discovery chain:
//  1. --contract-path flag (explicit, must exist)
//  2. RELAY_STATE_DIR env → $RELAY_STATE_DIR/party-contract.json
//  3. Session registry (cwd match / --project / single-session auto-select)
//  4. Walk up from cwd for .relay/state/party-contract.json
//  5. BuildContract from env (last resort)
func loadOrBuildContract(contractPath, projectName string) (*contract.Contract, error) {
	cwd, _ := os.Getwd()
	found, err := contract.FindContractPath(contract.FindOptions{
		ExplicitPath:  contractPath,
		RelayStateDir: os.Getenv("RELAY_STATE_DIR"),
		ProjectName:   projectName,
		CWD:           cwd,
	})
	if err != nil {
		return nil, err
	}
	if found != "" {
		return contract.LoadContract(found)
	}

	// Final fallback: build from env
	return contract.BuildContract(contract.InitOptions{
		StateDir: os.Getenv("RELAY_STATE_DIR"),
		ShareDir: os.Getenv("RELAY_SHARE_DIR"),
		MainDir:  os.Getenv("RELAY_MAIN_DIR"),
	})
}
