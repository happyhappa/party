package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/norm/relay-daemon/internal/contract"
	"github.com/norm/relay-daemon/internal/recycle"
	"github.com/spf13/cobra"
)

func newHealthCmd() *cobra.Command {
	var contractPath string
	var format string
	var roles []string

	cmd := &cobra.Command{
		Use:   "health",
		Short: "Evaluate per-role health and recycle readiness",
		Long: `Reads sidecar telemetry (CC/OC) or parses CX statusline for context usage.
Compares against contract RecycleSpec thresholds. If a role exceeds its threshold
and its recycle state is 'ready', transitions the state to 'exiting'.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			projectName, _ := cmd.Flags().GetString("project")
			return runHealth(cmd, contractPath, projectName, format, roles)
		},
	}
	cmd.Flags().StringVar(&contractPath, "contract-path", "", "path to contract JSON")
	cmd.Flags().StringVar(&format, "format", "human", "output format: human or json")
	cmd.Flags().StringSliceVar(&roles, "role", nil, "limit to specific roles (default: all)")
	return cmd
}

// roleHealth is the per-role health evaluation result.
type roleHealth struct {
	Role              string `json:"role"`
	Tool              string `json:"tool"`
	ContextUsedPct    int    `json:"context_used_pct"`
	ThresholdPct      int    `json:"threshold_pct"`
	Exceeded          bool   `json:"exceeded"`
	RecycleState      string `json:"recycle_state"`
	RecycleTriggered  bool   `json:"recycle_triggered"`
	AgentPID          int    `json:"agent_pid"`
	Alive             bool   `json:"alive"`
	TelemetrySource   string `json:"telemetry_source"` // "sidecar" or "statusline"
	TelemetryAge      string `json:"telemetry_age,omitempty"`
	Error             string `json:"error,omitempty"`
}

func runHealth(cmd *cobra.Command, contractPath, projectName, format string, filterRoles []string) error {
	c, err := loadOrBuildContract(contractPath, projectName)
	if err != nil {
		return fmt.Errorf("load contract: %w", err)
	}

	filterSet := map[string]bool{}
	for _, r := range filterRoles {
		filterSet[r] = true
	}

	var results []roleHealth
	for _, role := range c.Roles {
		if len(filterSet) > 0 && !filterSet[role.Name] {
			continue
		}
		result := evaluateRole(c, role)
		results = append(results, result)
	}

	switch format {
	case "json":
		data, err := json.MarshalIndent(results, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal: %w", err)
		}
		fmt.Fprintln(cmd.OutOrStdout(), string(data))
	default:
		printHealthHuman(cmd, results)
	}

	return nil
}

func evaluateRole(c *contract.Contract, role contract.RoleSpec) roleHealth {
	tool, ok := c.Tools[role.Tool]
	if !ok {
		return roleHealth{
			Role:  role.Name,
			Tool:  role.Tool,
			Error: fmt.Sprintf("unknown tool %q", role.Tool),
		}
	}

	result := roleHealth{
		Role:         role.Name,
		Tool:         role.Tool,
		ThresholdPct: tool.Recycle.ThresholdUsedPct,
	}

	// Read context usage
	if tool.Telemetry.HasSidecar {
		pct, age, err := readSidecarContext(c.Paths.StateDir, role.Name, tool.Telemetry)
		if err != nil {
			result.Error = fmt.Sprintf("sidecar: %v", err)
		} else {
			result.ContextUsedPct = pct
			result.TelemetrySource = "sidecar"
			result.TelemetryAge = age.Round(time.Second).String()
		}
	} else {
		// CX: would need pane text parsing, which is out of scope for Phase 2 Go.
		// For now, report that we can't read CX telemetry natively.
		result.TelemetrySource = "unavailable"
		result.Error = "no sidecar; CX context must be read via pane parser"
	}

	// Check recycle state
	state, err := recycle.LoadState(c.Paths.StateDir, role.Name)
	if err != nil {
		result.Error = fmt.Sprintf("recycle state: %v", err)
		return result
	}
	result.RecycleState = string(state.State)
	result.AgentPID = state.AgentPID
	result.Alive = recycle.IsAlive(state.AgentPID)

	// Evaluate threshold
	if result.ContextUsedPct >= result.ThresholdPct && result.ThresholdPct > 0 {
		result.Exceeded = true

		// Trigger recycle if state is ready
		if state.State == recycle.StateReady {
			lock, lockErr := recycle.AcquireLock(c.Paths.StateDir, role.Name)
			if lockErr == nil {
				defer lock.Release()
				// Re-load state under lock
				state, err = recycle.LoadState(c.Paths.StateDir, role.Name)
				if err == nil && state.State == recycle.StateReady {
					if transErr := state.Transition(recycle.StateExiting); transErr == nil {
						state.RecycleReason = fmt.Sprintf("context %d%% >= threshold %d%%", result.ContextUsedPct, result.ThresholdPct)
						if saveErr := state.Save(c.Paths.StateDir, role.Name); saveErr == nil {
							result.RecycleTriggered = true
							result.RecycleState = string(recycle.StateExiting)
						}
					}
				}
			}
		}
	}

	return result
}

// sidecarData represents the telemetry sidecar JSON file structure.
type sidecarData struct {
	Role       string  `json:"role"`
	ContextPct float64 `json:"context_pct"`
	UpdatedAt  string  `json:"updated_at"`
}

// readSidecarContext reads context usage from a sidecar telemetry file.
func readSidecarContext(stateDir, role string, tel contract.TelemetrySpec) (int, time.Duration, error) {
	// Resolve sidecar path: replace ${role} placeholder
	path := tel.SidecarPath
	path = strings.ReplaceAll(path, "${role}", role)

	// If path is relative or uses template vars that weren't expanded, try state dir
	if !strings.HasPrefix(path, "/") {
		path = fmt.Sprintf("%s/telemetry-%s.json", stateDir, role)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, fmt.Errorf("read %s: %w", path, err)
	}

	var sd sidecarData
	if err := json.Unmarshal(data, &sd); err != nil {
		return 0, 0, fmt.Errorf("decode sidecar: %w", err)
	}

	// Validate identity
	if sd.Role != "" && sd.Role != role {
		return 0, 0, fmt.Errorf("sidecar role mismatch: got %q, want %q", sd.Role, role)
	}

	// Parse context percentage
	pct := int(sd.ContextPct)

	// Try reading context_pct as a string percentage if it's 0
	if pct == 0 {
		// Check for string format or alternative key
		var raw map[string]interface{}
		json.Unmarshal(data, &raw)
		if v, ok := raw[tel.ContextKey]; ok {
			switch val := v.(type) {
			case float64:
				pct = int(val)
			case string:
				val = strings.TrimSuffix(val, "%")
				if n, err := strconv.Atoi(val); err == nil {
					pct = n
				}
			}
		}
	}

	// Parse age
	var age time.Duration
	if sd.UpdatedAt != "" {
		if t, err := time.Parse(time.RFC3339, sd.UpdatedAt); err == nil {
			age = time.Since(t)
		}
	}

	return pct, age, nil
}

func printHealthHuman(cmd *cobra.Command, results []roleHealth) {
	for _, r := range results {
		status := "OK"
		if r.Exceeded {
			status = "RECYCLE"
		}
		if r.RecycleTriggered {
			status = "TRIGGERED"
		}
		if r.Error != "" && r.ContextUsedPct == 0 {
			status = "UNKNOWN"
		}

		fmt.Fprintf(cmd.OutOrStdout(), "%s (%s): context %d%%/%d%% [%s] state=%s pid=%d alive=%v\n",
			r.Role, r.Tool, r.ContextUsedPct, r.ThresholdPct, status, r.RecycleState, r.AgentPID, r.Alive)

		if r.RecycleTriggered {
			fmt.Fprintf(cmd.OutOrStdout(), "  → Recycle initiated: state transitioned to exiting\n")
		}
		if r.Error != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "  ! %s\n", r.Error)
		}
	}
}
