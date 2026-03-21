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

// loadContractRoleAndTool loads a contract and resolves the role and tool specs.
func loadContractRoleAndTool(contractPath, projectName, role string) (*contract.Contract, contract.RoleSpec, contract.AgentToolSpec, error) {
	c, err := loadOrBuildContract(contractPath, projectName)
	if err != nil {
		return nil, contract.RoleSpec{}, contract.AgentToolSpec{}, fmt.Errorf("load contract: %w", err)
	}
	for _, roleSpec := range c.Roles {
		if roleSpec.Name == role {
			toolSpec, ok := c.Tools[roleSpec.Tool]
			if !ok {
				return nil, contract.RoleSpec{}, contract.AgentToolSpec{}, fmt.Errorf("role %q references unknown tool %q", role, roleSpec.Tool)
			}
			return c, roleSpec, toolSpec, nil
		}
	}
	return nil, contract.RoleSpec{}, contract.AgentToolSpec{}, fmt.Errorf("unknown role %q", role)
}

// loadContractRoleAndToolWithPanes loads a contract, applies pane overrides, and resolves role/tool.
func loadContractRoleAndToolWithPanes(contractPath, projectName, role string, setPanes []string) (*contract.Contract, contract.RoleSpec, contract.AgentToolSpec, error) {
	c, err := loadOrBuildContract(contractPath, projectName)
	if err != nil {
		return nil, contract.RoleSpec{}, contract.AgentToolSpec{}, fmt.Errorf("load contract: %w", err)
	}
	if err := applyPaneOverrides(c, setPanes); err != nil {
		return nil, contract.RoleSpec{}, contract.AgentToolSpec{}, err
	}
	for _, roleSpec := range c.Roles {
		if roleSpec.Name == role {
			toolSpec, ok := c.Tools[roleSpec.Tool]
			if !ok {
				return nil, contract.RoleSpec{}, contract.AgentToolSpec{}, fmt.Errorf("role %q references unknown tool %q", role, roleSpec.Tool)
			}
			return c, roleSpec, toolSpec, nil
		}
	}
	return nil, contract.RoleSpec{}, contract.AgentToolSpec{}, fmt.Errorf("unknown role %q", role)
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
