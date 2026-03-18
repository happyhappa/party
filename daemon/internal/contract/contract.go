package contract

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

const CurrentVersion = 1

// Duration wraps time.Duration so JSON uses human-readable strings like "5m".
type Duration struct {
	time.Duration
}

func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.Duration.String())
}

func (d *Duration) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("duration must be a string: %w", err)
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil {
		return fmt.Errorf("parse duration %q: %w", raw, err)
	}
	d.Duration = parsed
	return nil
}

// Contract is the canonical runtime description of one party instance.
type Contract struct {
	Version     int                      `json:"version"`
	GeneratedAt time.Time                `json:"generated_at"`
	Project     ProjectSpec              `json:"project"`
	Paths       PathSpec                 `json:"paths"`
	Session     SessionSpec              `json:"session"`
	Roles       []RoleSpec               `json:"roles"`
	Layout      LayoutSpec               `json:"layout"`
	Thresholds  ThresholdSpec            `json:"thresholds"`
	Tools       map[string]AgentToolSpec `json:"tools"`
}

type ProjectSpec struct {
	Name    string `json:"name"`
	RootDir string `json:"root_dir"`
	MainDir string `json:"main_dir"`
	PodName string `json:"pod_name,omitempty"`
}

type PathSpec struct {
	ShareDir     string `json:"share_dir"`
	StateDir     string `json:"state_dir"`
	LogDir       string `json:"log_dir"`
	InboxDir     string `json:"inbox_dir"`
	BeadsDir     string `json:"beads_dir"`
	PaneMap      string `json:"pane_map"`
	RelayLog     string `json:"relay_log"`
	ContractPath string `json:"contract_path"`
	DocsDir      string `json:"docs_dir,omitempty"`
}

type SessionSpec struct {
	Name       string `json:"name"`
	WindowName string `json:"window_name"`
}

// RoleSpec binds a party role to a reusable tool definition.
type RoleSpec struct {
	Name          string            `json:"name"`
	Tool          string            `json:"tool"`
	WorktreeDir   string            `json:"worktree_dir"`
	PaneTitle     string            `json:"pane_title"`
	PaneID        string            `json:"pane_id,omitempty"`
	ProjectDir    string            `json:"project_dir,omitempty"`
	Env           map[string]string `json:"env,omitempty"`
	ExpectedProcs []string          `json:"expected_procs"`
	Overrides     RoleOverrideSpec  `json:"overrides,omitempty"`
}

// RoleOverrideSpec replaces the entire pointed-to field when non-nil.
type RoleOverrideSpec struct {
	Launch     *CommandSpec    `json:"launch,omitempty"`
	Compaction *CompactionSpec `json:"compaction,omitempty"`
	Health     *ToolHealthSpec `json:"health,omitempty"`
}

type LayoutSpec struct {
	SchemaVersion int              `json:"schema_version"`
	Panes         []PaneLayoutSpec `json:"panes"`
}

type PaneLayoutSpec struct {
	Role      string `json:"role"`
	Position  string `json:"position"`
	SplitFrom string `json:"split_from,omitempty"`
	SplitAxis string `json:"split_axis,omitempty"`
	SplitSize string `json:"split_size,omitempty"`
}

type ThresholdSpec struct {
	InjectorBaseDelay    Duration `json:"injector_base_delay"`
	InjectorCharsPerStep int      `json:"injector_chars_per_step"`
	InjectorDelayPerStep Duration `json:"injector_delay_per_step"`
	InjectorMaxDelay     Duration `json:"injector_max_delay"`
	HealthInterval       Duration `json:"health_interval"`
	IdleThreshold        Duration `json:"idle_threshold"`
	SidecarMaxAge        Duration `json:"sidecar_max_age"`
}

// AgentToolSpec defines reusable behavior for one agent tool family.
type AgentToolSpec struct {
	Name        string           `json:"name"`
	Launch      CommandSpec      `json:"launch"`
	Sandbox     SandboxSpec      `json:"sandbox"`
	Telemetry   TelemetrySpec    `json:"telemetry"`
	ConfigFiles []ConfigFileSpec `json:"config_files,omitempty"`
	Injection   InjectionSpec    `json:"injection"`
	PaneParser  PaneParserSpec   `json:"pane_parser"`
	Compaction  CompactionSpec   `json:"compaction"`
	Health      ToolHealthSpec   `json:"health"`
}

type CommandSpec struct {
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env,omitempty"`
}

type SandboxSpec struct {
	Restricted       bool     `json:"restricted"`
	RequiredEnv      []string `json:"required_env,omitempty"`
	AllowedWriteDirs []string `json:"allowed_write_dirs,omitempty"`
}

type TelemetrySpec struct {
	HasSidecar  bool   `json:"has_sidecar"`
	SidecarPath string `json:"sidecar_path,omitempty"`
	IdentityKey string `json:"identity_key,omitempty"`
	ContextKey  string `json:"context_key,omitempty"`
}

type ConfigFileSpec struct {
	Name      string               `json:"name"`
	Format    string               `json:"format"`
	Path      string               `json:"path"`
	Required  bool                 `json:"required"`
	Mutations []ConfigMutationSpec `json:"mutations"`
}

type ConfigMutationSpec struct {
	Path          string            `json:"path"`
	Action        string            `json:"action"`
	ValueType     string            `json:"value_type"`
	StringValue   string            `json:"string_value,omitempty"`
	BoolValue     *bool             `json:"bool_value,omitempty"`
	ArrayValue    []string          `json:"array_value,omitempty"`
	ObjectValue   map[string]string `json:"object_value,omitempty"`
	Owned         bool              `json:"owned"`
	CreateParents bool              `json:"create_parents,omitempty"`
}

type InjectionSpec struct {
	RequiresPromptReady bool               `json:"requires_prompt_ready"`
	PreInjectActions    []InjectActionSpec `json:"pre_inject_actions,omitempty"`
	SlashCommandMode    string             `json:"slash_command_mode"`
}

type InjectActionSpec struct {
	When        string   `json:"when"`
	Action      string   `json:"action"`
	Key         string   `json:"key,omitempty"`
	Text        string   `json:"text,omitempty"`
	Sleep       Duration `json:"sleep,omitempty"`
	MaxAttempts int      `json:"max_attempts,omitempty"`
}

type PaneParserSpec struct {
	Strategy           string               `json:"strategy"`
	PromptPrefixes     []string             `json:"prompt_prefixes,omitempty"`
	FooterMatchers     []LineMatcherSpec    `json:"footer_matchers,omitempty"`
	StatuslineMatchers []LineMatcherSpec    `json:"statusline_matchers,omitempty"`
	SkipMatchers       []LineMatcherSpec    `json:"skip_matchers,omitempty"`
	SuggestionMatchers []LineMatcherSpec    `json:"suggestion_matchers,omitempty"`
	CompactedMatchers  []TextMatcherSpec    `json:"compacted_matchers,omitempty"`
	ContextExtractors  []ContextExtractSpec `json:"context_extractors,omitempty"`
	ReadyPolicy        string               `json:"ready_policy"`
	IdlePolicy         string               `json:"idle_policy"`
}

type LineMatcherSpec struct {
	MatchType string   `json:"match_type"`
	Values    []string `json:"values,omitempty"`
	Regex     string   `json:"regex,omitempty"`
}

type TextMatcherSpec struct {
	MatchType string `json:"match_type"`
	Value     string `json:"value"`
}

type ContextExtractSpec struct {
	Regex               string            `json:"regex"`
	ValueGroup          int               `json:"value_group"`
	RequireLineMatchers []LineMatcherSpec `json:"require_line_matchers,omitempty"`
}

type CompactionSpec struct {
	Command          string   `json:"command"`
	RestoreCommand   string   `json:"restore_command"`
	ThresholdUsedPct int      `json:"threshold_used_pct"`
	RecentWindow     Duration `json:"recent_window"`
}

type ToolHealthSpec struct {
	ProcessMatchers []string `json:"process_matchers"`
}

func (c *Contract) ValidateBasic() error {
	if c == nil {
		return fmt.Errorf("contract is nil")
	}
	if c.Version == 0 {
		return fmt.Errorf("contract version is required")
	}
	if c.Project.Name == "" {
		return fmt.Errorf("project.name is required")
	}
	if c.Project.RootDir == "" {
		return fmt.Errorf("project.root_dir is required")
	}
	if c.Paths.StateDir == "" {
		return fmt.Errorf("paths.state_dir is required")
	}
	if c.Session.Name == "" {
		return fmt.Errorf("session.name is required")
	}
	return nil
}

// LoadContract reads a contract and rejects unsupported schema versions.
func LoadContract(path string) (*Contract, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var probe struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, fmt.Errorf("decode contract version: %w", err)
	}
	if probe.Version != CurrentVersion {
		return nil, fmt.Errorf("unsupported contract version %d", probe.Version)
	}
	var c Contract
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("decode contract: %w", err)
	}
	if err := c.ValidateBasic(); err != nil {
		return nil, err
	}
	return &c, nil
}
