package contract

import (
	"path/filepath"
	"time"
)

// DefaultContract returns the default party contract described in RFC-011.
func DefaultContract(projectRoot, mainDir string) *Contract {
	return &Contract{
		Version: CurrentVersion,
		Project: ProjectSpec{
			Name:    filepath.Base(projectRoot),
			RootDir: projectRoot,
			MainDir: mainDir,
		},
		Paths: PathSpec{
			ShareDir:     "{{.project_root}}/.relay",
			StateDir:     "{{.share_dir}}/state",
			LogDir:       "{{.share_dir}}/log",
			InboxDir:     "{{.share_dir}}/outbox",
			BeadsDir:     "{{.main_dir}}/.beads",
			PaneMap:      "{{.state_dir}}/panes.json",
			RelayLog:     "{{.log_dir}}/events.jsonl",
			ContractPath: "{{.state_dir}}/party-contract.json",
		},
		Session: SessionSpec{
			Name:       "party-{{.project_name}}",
			WindowName: "main",
		},
		Roles: []RoleSpec{
			{
				Name:          "oc",
				Tool:          "claude_code",
				WorktreeDir:   "{{.main_dir}}",
				PaneTitle:     "OC",
				ExpectedProcs: []string{"claude", "node"},
			},
			{
				Name:          "cc",
				Tool:          "claude_code",
				WorktreeDir:   "{{.project_root}}/cc-wt",
				PaneTitle:     "CC",
				ExpectedProcs: []string{"claude", "node"},
			},
			{
				Name:          "cx",
				Tool:          "codex",
				WorktreeDir:   "{{.project_root}}/cx-wt",
				PaneTitle:     "CX",
				ExpectedProcs: []string{"codex", "node"},
			},
		},
		Layout: LayoutSpec{
			SchemaVersion: 1,
			Panes: []PaneLayoutSpec{
				{Role: "oc", Position: "left"},
				{Role: "cc", Position: "right-top", SplitFrom: "oc", SplitAxis: "horizontal", SplitSize: "50%"},
				{Role: "cx", Position: "right-bottom", SplitFrom: "cc", SplitAxis: "vertical", SplitSize: "50%"},
			},
		},
		Thresholds: ThresholdSpec{
			InjectorBaseDelay:    Duration{Duration: 500 * time.Millisecond},
			InjectorCharsPerStep: 200,
			InjectorDelayPerStep: Duration{Duration: 100 * time.Millisecond},
			InjectorMaxDelay:     Duration{Duration: 3 * time.Second},
			HealthInterval:       Duration{Duration: 5 * time.Minute},
			IdleThreshold:        Duration{Duration: 5 * time.Minute},
			SidecarMaxAge:        Duration{Duration: 60 * time.Second},
		},
		Tools: map[string]AgentToolSpec{
			"claude_code": defaultClaudeCodeTool(),
			"codex":       defaultCodexTool(),
		},
	}
}

func defaultClaudeCodeTool() AgentToolSpec {
	return AgentToolSpec{
		Name: "claude_code",
		Launch: CommandSpec{
			Command: "claude",
			Args:    []string{"--model", "claude-opus-4-6", "--dangerously-skip-permissions"},
		},
		Sandbox: SandboxSpec{Restricted: false},
		Telemetry: TelemetrySpec{
			HasSidecar:  true,
			SidecarPath: "{{.state_dir}}/telemetry-${role}.json",
			IdentityKey: "role",
			ContextKey:  "context_pct",
		},
		ConfigFiles: []ConfigFileSpec{
			{
				Name:     "claude_settings",
				Format:   "json",
				Path:     "{{.home}}/.claude/settings.json",
				Required: false,
				Mutations: []ConfigMutationSpec{
					{
						Path:        "statusLine",
						Action:      "ensure_exists",
						ValueType:   "object",
						ObjectValue: map[string]string{"type": "command", "command": "{{.scripts_dir}}/claude-statusline.sh"},
						Owned:       true,
					},
				},
			},
		},
		Injection: InjectionSpec{
			RequiresPromptReady: true,
			SlashCommandMode:    "bare",
		},
		PaneParser: PaneParserSpec{
			Strategy:       "separator_scan",
			PromptPrefixes: []string{"❯"},
			SkipMatchers: []LineMatcherSpec{
				{MatchType: "prefix", Values: []string{"⏵"}},
				{MatchType: "prefix", Values: []string{"─"}},
			},
			CompactedMatchers: []TextMatcherSpec{
				{MatchType: "regex", Value: "(?i)✻\\s*conversation compacted"},
			},
			ReadyPolicy: "prompt_only",
			IdlePolicy:  "prompt_only",
		},
		Compaction: CompactionSpec{
			Command:          "/compact",
			RestoreCommand:   "/rec",
			ThresholdUsedPct: 75,
			RecentWindow:     Duration{Duration: 5 * time.Minute},
		},
		Health: ToolHealthSpec{
			ProcessMatchers: []string{"claude", "node"},
		},
	}
}

func defaultCodexTool() AgentToolSpec {
	return AgentToolSpec{
		Name: "codex",
		Launch: CommandSpec{
			Command: "codex",
			Args: []string{
				"-a", "never", "-s", "workspace-write",
				"--add-dir", "/tmp",
				"--add-dir", "{{.share_dir}}",
				"--add-dir", "{{.home}}/.cache",
				"--add-dir", "{{.inbox_dir}}/cx",
				"--add-dir", "/mnt/llm-share",
			},
		},
		Sandbox: SandboxSpec{
			Restricted: true,
			RequiredEnv: []string{
				"PATH", "HOME", "AGENT_ROLE", "CODEX_HOME",
				"RELAY_INBOX_DIR", "RELAY_STATE_DIR", "RELAY_LOG_DIR",
				"RELAY_SHARE_DIR", "RELAY_MAIN_DIR", "RELAY_TMUX_SESSION", "BEADS_DIR",
			},
		},
		Telemetry: TelemetrySpec{HasSidecar: false},
		ConfigFiles: []ConfigFileSpec{
			{
				Name:     "codex_config",
				Format:   "toml",
				Path:     "{{.home}}/.codex/config.toml",
				Required: false,
				Mutations: []ConfigMutationSpec{
					{
						Path:          "tui",
						Action:        "ensure_table",
						ValueType:     "object",
						CreateParents: true,
						Owned:         false,
					},
					{
						Path:          "tui.status_line",
						Action:        "replace",
						ValueType:     "string_array",
						ArrayValue:    []string{"model-with-reasoning", "context-used", "project-root", "git-branch"},
						Owned:         true,
						CreateParents: true,
					},
					{
						Path:          "shell_environment_policy.include_only",
						Action:        "merge_union",
						ValueType:     "string_array",
						ArrayValue:    []string{"PATH", "HOME", "AGENT_ROLE", "CODEX_HOME", "RELAY_INBOX_DIR", "RELAY_STATE_DIR", "RELAY_LOG_DIR", "RELAY_SHARE_DIR", "RELAY_MAIN_DIR", "RELAY_TMUX_SESSION", "BEADS_DIR"},
						Owned:         false,
						CreateParents: true,
					},
				},
			},
		},
		Injection: InjectionSpec{
			RequiresPromptReady: true,
			SlashCommandMode:    "wrapped",
			PreInjectActions: []InjectActionSpec{
				{When: "footer_visible", Action: "send_key", Key: "Space", MaxAttempts: 1},
				{When: "footer_visible", Action: "sleep", Sleep: Duration{Duration: 200 * time.Millisecond}},
				{When: "footer_visible", Action: "send_key", Key: "BSpace", MaxAttempts: 1},
				{When: "footer_visible", Action: "sleep", Sleep: Duration{Duration: 200 * time.Millisecond}},
				{When: "footer_visible", Action: "recapture"},
			},
		},
		PaneParser: PaneParserSpec{
			Strategy:       "last_nonempty_skip",
			PromptPrefixes: []string{"›"},
			FooterMatchers: []LineMatcherSpec{
				{MatchType: "contains_all", Values: []string{"? for shortcuts"}},
				{MatchType: "contains_all", Values: []string{"% context left"}},
				{MatchType: "contains_all", Values: []string{"% left ·"}},
			},
			StatuslineMatchers: []LineMatcherSpec{
				{MatchType: "contains_all", Values: []string{"·", "used"}},
			},
			SkipMatchers: []LineMatcherSpec{
				{MatchType: "contains_all", Values: []string{"·", "used"}},
			},
			SuggestionMatchers: []LineMatcherSpec{
				{MatchType: "prefix", Values: []string{"›"}},
			},
			CompactedMatchers: []TextMatcherSpec{
				{MatchType: "regex", Value: "(?i)context compacted"},
			},
			ContextExtractors: []ContextExtractSpec{
				{
					Regex:      "(\\d+)%\\s*used",
					ValueGroup: 1,
					RequireLineMatchers: []LineMatcherSpec{
						{MatchType: "contains_all", Values: []string{"·"}},
					},
				},
			},
			ReadyPolicy: "prompt_and_no_footer",
			IdlePolicy:  "footer_or_prompt",
		},
		Compaction: CompactionSpec{
			Command:          "/compact",
			RestoreCommand:   "/rec",
			ThresholdUsedPct: 40,
			RecentWindow:     Duration{Duration: 2 * time.Minute},
		},
		Health: ToolHealthSpec{
			ProcessMatchers: []string{"codex", "node"},
		},
	}
}
