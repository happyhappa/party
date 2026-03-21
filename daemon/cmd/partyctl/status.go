package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"text/tabwriter"
	"time"

	"github.com/norm/relay-daemon/internal/contract"
	"github.com/norm/relay-daemon/internal/pane"
	"github.com/spf13/cobra"
)

type recycleStateFile struct {
	State           string `json:"state"`
	EnteredAt       string `json:"entered_at"`
	AgentPID        int    `json:"agent_pid"`
	SessionID       string `json:"session_id"`
	TranscriptPath  string `json:"transcript_path"`
	LastBriefOffset int64  `json:"last_brief_offset"`
	LastBriefAt     string `json:"last_brief_at"`
	RecycleReason   string `json:"recycle_reason"`
	Error           string `json:"error"`
}

type statusRow struct {
	Role          string  `json:"role"`
	Tool          string  `json:"tool"`
	ContextPct    *int    `json:"context_pct,omitempty"`
	RecycleState  string  `json:"recycle_state"`
	LastBriefAt   string  `json:"last_brief_at,omitempty"`
	AgentPID      int     `json:"agent_pid,omitempty"`
	UptimeSeconds int64   `json:"uptime_seconds,omitempty"`
	SessionID     string  `json:"session_id,omitempty"`
	ModelID       string  `json:"model_id,omitempty"`
	ModelDisplay  string  `json:"model_display,omitempty"`
	CostUSD       float64 `json:"cost_usd,omitempty"`
	Error         string  `json:"error,omitempty"`
}

func newStatusCmd() *cobra.Command {
	var contractPath string
	var format string

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show aggregated party status",
		RunE: func(cmd *cobra.Command, args []string) error {
			projectName, _ := cmd.Flags().GetString("project")
			return runStatus(cmd, contractPath, projectName, format, time.Now())
		},
	}
	cmd.Flags().StringVar(&contractPath, "contract-path", "", "path to contract JSON")
	cmd.Flags().StringVar(&format, "format", "human", "output format: human or json")
	return cmd
}

func runStatus(cmd *cobra.Command, contractPath, projectName, format string, now time.Time) error {
	c, err := loadOrBuildContract(contractPath, projectName)
	if err != nil {
		return fmt.Errorf("load contract: %w", err)
	}

	rows, err := collectStatusRows(c, now)
	if err != nil {
		return err
	}

	switch format {
	case "human":
		renderStatusHuman(cmd.OutOrStdout(), rows)
	case "json":
		data, err := json.MarshalIndent(rows, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal status: %w", err)
		}
		fmt.Fprintln(cmd.OutOrStdout(), string(data))
	default:
		return fmt.Errorf("unsupported format %q (want human or json)", format)
	}

	return nil
}

func collectStatusRows(c *contract.Contract, now time.Time) ([]statusRow, error) {
	rows := make([]statusRow, 0, len(c.Roles))
	for _, role := range c.Roles {
		row := statusRow{
			Role:         role.Name,
			Tool:         role.Tool,
			RecycleState: "unknown",
		}

		toolSpec, ok := c.Tools[role.Tool]
		if ok && toolSpec.Telemetry.HasSidecar {
			td, err := pane.ReadTelemetrySidecar(c.Paths.StateDir, role.Name)
			if err == nil && td != nil {
				if td.ContextPct >= 0 {
					ctx := int(td.ContextPct)
					row.ContextPct = &ctx
				}
				row.SessionID = td.SessionID
				row.ModelID = td.ModelID
				row.ModelDisplay = td.ModelDisplay
				row.CostUSD = td.CostUSD
			}
		}

		recyclePath := filepath.Join(c.Paths.StateDir, fmt.Sprintf("recycle-%s.json", role.Name))
		rs, err := readRecycleStateFile(recyclePath)
		if err != nil {
			if !os.IsNotExist(err) {
				row.Error = err.Error()
			}
			rows = append(rows, row)
			continue
		}

		if rs.State != "" {
			row.RecycleState = rs.State
		}
		row.AgentPID = rs.AgentPID
		row.LastBriefAt = rs.LastBriefAt
		if row.SessionID == "" {
			row.SessionID = rs.SessionID
		}
		if enteredAt, ok := parseRFC3339(rs.EnteredAt); ok {
			if !enteredAt.After(now) {
				row.UptimeSeconds = int64(now.Sub(enteredAt).Seconds())
			}
		}

		rows = append(rows, row)
	}
	return rows, nil
}

func readRecycleStateFile(path string) (*recycleStateFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var state recycleStateFile
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	return &state, nil
}

func parseRFC3339(value string) (time.Time, bool) {
	if value == "" {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, false
	}
	return parsed, true
}

func renderStatusHuman(w io.Writer, rows []statusRow) {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ROLE\tTOOL\tCTX\tRECYCLE\tLAST BRIEF\tPID\tUPTIME")
	for _, row := range rows {
		fmt.Fprintf(
			tw,
			"%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			row.Role,
			row.Tool,
			formatContextPct(row.ContextPct),
			row.RecycleState,
			formatTimestamp(row.LastBriefAt),
			formatPID(row.AgentPID),
			formatUptime(row.UptimeSeconds),
		)
		if row.Error != "" {
			fmt.Fprintf(tw, "%s\t%s\t%s\n", "", "error:", row.Error)
		}
	}
	_ = tw.Flush()
}

func formatContextPct(pct *int) string {
	if pct == nil {
		return "n/a"
	}
	return fmt.Sprintf("%d%%", *pct)
}

func formatTimestamp(raw string) string {
	if raw == "" {
		return "n/a"
	}
	if ts, ok := parseRFC3339(raw); ok {
		return ts.Format(time.RFC3339)
	}
	return raw
}

func formatPID(pid int) string {
	if pid <= 0 {
		return "n/a"
	}
	return fmt.Sprintf("%d", pid)
}

func formatUptime(seconds int64) string {
	if seconds <= 0 {
		return "n/a"
	}
	d := time.Duration(seconds) * time.Second
	if d < time.Minute {
		return d.String()
	}
	return d.Truncate(time.Second).String()
}
