package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/norm/relay-daemon/internal/recycle"
	"github.com/spf13/cobra"
)

type briefOutput struct {
	Role        string    `json:"role"`
	Source      string    `json:"source"`
	BeadID      string    `json:"bead_id,omitempty"`
	ByteRange   [2]int64  `json:"byte_range"`
	LastBriefAt time.Time `json:"last_brief_at"`
	Offset      int64     `json:"offset"`
	Transcript  string    `json:"transcript_path"`
	SessionID   string    `json:"session_id,omitempty"`
	Error       string    `json:"error,omitempty"`
}

func newBriefCmd() *cobra.Command {
	var contractPath string
	var final bool
	var format string

	cmd := &cobra.Command{
		Use:   "brief <role>",
		Short: "Generate a session brief for a role",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			projectName, _ := cmd.Flags().GetString("project")
			return runBrief(cmd, contractPath, projectName, args[0], final, format, time.Now().UTC())
		},
	}
	cmd.Flags().StringVar(&contractPath, "contract-path", "", "path to contract JSON")
	cmd.Flags().BoolVar(&final, "final", false, "generate a final brief using a larger transcript window")
	cmd.Flags().StringVar(&format, "format", "human", "output format: human or json")
	return cmd
}

func runBrief(cmd *cobra.Command, contractPath, projectName, role string, final bool, format string, now time.Time) error {
	c, roleSpec, toolSpec, err := loadContractRoleAndTool(contractPath, projectName, role)
	if err != nil {
		return err
	}

	lock, err := recycle.AcquireLock(c.Paths.StateDir, roleSpec.Name)
	if err != nil {
		return err
	}
	defer lock.Release()

	state, err := recycle.LoadState(c.Paths.StateDir, roleSpec.Name)
	if err != nil {
		return err
	}
	if state.TranscriptPath == "" {
		return fmt.Errorf("recycle state for %q has no transcript_path", roleSpec.Name)
	}

	info, err := os.Stat(state.TranscriptPath)
	if err != nil {
		return fmt.Errorf("stat transcript: %w", err)
	}

	opts := recycle.BriefOptions{
		Role:           roleSpec.Name,
		TranscriptPath: state.TranscriptPath,
		StartOffset:    state.LastBriefOffset,
		EndOffset:      info.Size(),
		SessionID:      state.SessionID,
		FilterPath:     findPartyJSONLFilter(),
		PromptPath:     findBriefPrompt(),
		Generator:      "codex",
		Source:         "continuous",
		MaxRawInput:    recycle.DefaultMaxRawInput,
		BeadsDir:       c.Paths.BeadsDir,
	}
	if toolSpec.Recycle.BriefMinDelta > 0 && !final {
		delta := opts.EndOffset - opts.StartOffset
		if delta < int64(toolSpec.Recycle.BriefMinDelta) {
			return fmt.Errorf("transcript delta %d bytes is below brief_min_delta %d", delta, toolSpec.Recycle.BriefMinDelta)
		}
	}
	if final {
		opts.Source = "final"
		opts.MaxRawInput = 200 * 1024
		if opts.EndOffset > opts.MaxRawInput {
			opts.StartOffset = opts.EndOffset - opts.MaxRawInput
		} else {
			opts.StartOffset = 0
		}
	}

	result, briefErr := recycle.GenerateBrief(opts)
	output := briefOutput{
		Role:        roleSpec.Name,
		Source:      opts.Source,
		LastBriefAt: now,
		Transcript:  state.TranscriptPath,
		SessionID:   state.SessionID,
	}
	if result != nil {
		output.BeadID = result.BeadID
		output.ByteRange = result.ByteRange
		output.Offset = result.ByteRange[1]
		state.LastBriefOffset = result.ByteRange[1]
		state.LastBriefAt = now
		if err := state.Save(c.Paths.StateDir, roleSpec.Name); err != nil {
			return fmt.Errorf("save recycle state: %w", err)
		}
		if !final {
			if err := recycle.CleanupOldBriefs(c.Paths.BeadsDir, roleSpec.Name, 3); err != nil {
				return fmt.Errorf("cleanup old briefs: %w", err)
			}
		}
	}
	if briefErr != nil {
		output.Error = briefErr.Error()
	}

	switch format {
	case "json":
		data, err := json.MarshalIndent(output, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal brief output: %w", err)
		}
		fmt.Fprintln(cmd.OutOrStdout(), string(data))
	case "human":
		if output.Error == "" {
			fmt.Fprintf(cmd.OutOrStdout(), "Generated %s brief for %s (%d-%d)\n", output.Source, output.Role, output.ByteRange[0], output.ByteRange[1])
		} else {
			fmt.Fprintf(cmd.OutOrStdout(), "Brief generation for %s failed after generation step: %s\n", output.Role, output.Error)
		}
	default:
		return fmt.Errorf("unsupported format %q (want human or json)", format)
	}

	if briefErr != nil {
		return briefErr
	}
	return nil
}

func findBriefPrompt() string {
	if path, err := execLookPath("party-brief-prompt.txt"); err == nil {
		return path
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidate := filepath.Join(home, ".local", "bin", "party-brief-prompt.txt")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	if cwd, err := os.Getwd(); err == nil {
		candidate := filepath.Join(cwd, "daemon", "scripts", "party-brief-prompt.txt")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return ""
}

func findPartyJSONLFilter() string {
	if path, err := execLookPath("party-jsonl-filter"); err == nil {
		return path
	}
	if cwd, err := os.Getwd(); err == nil {
		candidate := filepath.Join(cwd, "daemon", "scripts", "party-jsonl-filter")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return ""
}
