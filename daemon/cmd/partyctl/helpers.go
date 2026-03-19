package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/norm/relay-daemon/internal/contract"
)

// exitError signals a specific exit code without printing an error message.
type exitError struct {
	code int
}

func (e *exitError) Error() string {
	return fmt.Sprintf("exit %d", e.code)
}

// loadOrBuildContract loads an existing contract from disk or builds one
// from the environment if no contract file exists.
//
// If contractPath is explicitly provided (via flag), the file MUST exist.
// If discovered from RELAY_STATE_DIR, we try the file and fall back to build.
// If no path is available at all, build from environment.
func loadOrBuildContract(contractPath string) (*contract.Contract, error) {
	explicit := contractPath != ""

	if contractPath == "" {
		stateDir := os.Getenv("RELAY_STATE_DIR")
		if stateDir != "" {
			contractPath = filepath.Join(stateDir, "party-contract.json")
		}
	}

	if contractPath != "" {
		if _, err := os.Stat(contractPath); err == nil {
			return contract.LoadContract(contractPath)
		} else if explicit {
			return nil, fmt.Errorf("contract file not found: %s", contractPath)
		}
		// Discovered path missing — fall through to build
	}

	return contract.BuildContract(contract.InitOptions{
		StateDir: os.Getenv("RELAY_STATE_DIR"),
		ShareDir: os.Getenv("RELAY_SHARE_DIR"),
		MainDir:  os.Getenv("RELAY_MAIN_DIR"),
	})
}
