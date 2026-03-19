package contract

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// ValidationResult holds structured validation findings.
type ValidationResult struct {
	Valid  bool              `json:"valid"`
	Errors []ValidationError `json:"errors,omitempty"`
}

// ValidationError describes a single validation failure.
type ValidationError struct {
	Field   string `json:"field"`
	Check   string `json:"check"`
	Message string `json:"message"`
}

func (r *ValidationResult) addError(field, check, msg string) {
	r.Errors = append(r.Errors, ValidationError{
		Field:   field,
		Check:   check,
		Message: msg,
	})
}

// Validate performs deep validation of a resolved contract against the
// current environment. It checks binary availability, path existence,
// config files, and internal consistency.
func Validate(c *Contract) *ValidationResult {
	r := &ValidationResult{Valid: true}
	if c == nil {
		r.Valid = false
		r.addError("contract", "nil", "contract is nil")
		return r
	}

	// Schema-level checks first
	if err := c.ValidateBasic(); err != nil {
		r.addError("contract", "schema", err.Error())
	}

	validateBinaries(c, r)
	validatePaths(c, r)
	validateConfigFiles(c, r)
	validateRoleToolRefs(c, r)
	validateLayoutRoles(c, r)
	validateRoleDuplication(c, r)
	validateToolSpecs(c, r)

	r.Valid = len(r.Errors) == 0
	return r
}

// validateBinaries checks that required binaries are on PATH.
func validateBinaries(c *Contract, r *ValidationResult) {
	// Collect unique binaries from tool launch commands
	seen := map[string]bool{}
	for _, tool := range c.Tools {
		cmd := tool.Launch.Command
		if cmd != "" && !seen[cmd] {
			seen[cmd] = true
			if _, err := exec.LookPath(cmd); err != nil {
				r.addError(
					fmt.Sprintf("tools.%s.launch.command", tool.Name),
					"binary_exists",
					fmt.Sprintf("binary %q not found on PATH", cmd),
				)
			}
		}
	}
	// tmux is always required
	if !seen["tmux"] {
		if _, err := exec.LookPath("tmux"); err != nil {
			r.addError("environment", "binary_exists", "binary \"tmux\" not found on PATH")
		}
	}
}

// validatePaths checks that required directories exist and are writable.
func validatePaths(c *Contract, r *ValidationResult) {
	checkDirExists(c.Project.RootDir, "project.root_dir", r)
	checkDirExists(c.Project.MainDir, "project.main_dir", r)
	checkDirWritable(c.Paths.StateDir, "paths.state_dir", r)
	checkDirWritable(c.Paths.LogDir, "paths.log_dir", r)
	checkDirWritable(c.Paths.InboxDir, "paths.inbox_dir", r)

	for i, role := range c.Roles {
		field := fmt.Sprintf("roles[%d].worktree_dir", i)
		checkDirExists(role.WorktreeDir, field, r)
	}
}

func checkDirExists(path, field string, r *ValidationResult) {
	if path == "" {
		r.addError(field, "required", "path is empty")
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		r.addError(field, "dir_exists", fmt.Sprintf("directory does not exist: %s", path))
		return
	}
	if !info.IsDir() {
		r.addError(field, "is_dir", fmt.Sprintf("path is not a directory: %s", path))
	}
}

func checkDirWritable(path, field string, r *ValidationResult) {
	if path == "" {
		r.addError(field, "required", "path is empty")
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		r.addError(field, "dir_exists", fmt.Sprintf("directory does not exist: %s", path))
		return
	}
	if !info.IsDir() {
		r.addError(field, "is_dir", fmt.Sprintf("path is not a directory: %s", path))
		return
	}
	// Attempt to create a temp file to verify writability
	f, err := os.CreateTemp(path, ".partyctl-validate-*")
	if err != nil {
		r.addError(field, "writable", fmt.Sprintf("directory is not writable: %s", path))
		return
	}
	name := f.Name()
	f.Close()
	os.Remove(name)
}

// validateConfigFiles checks that config files referenced in tool specs exist.
func validateConfigFiles(c *Contract, r *ValidationResult) {
	for _, tool := range c.Tools {
		for i, cf := range tool.ConfigFiles {
			if !cf.Required {
				continue
			}
			field := fmt.Sprintf("tools.%s.config_files[%d].path", tool.Name, i)
			if cf.Path == "" {
				r.addError(field, "required", "config file path is empty")
				continue
			}
			if _, err := os.Stat(cf.Path); err != nil {
				r.addError(field, "file_exists", fmt.Sprintf("required config file missing: %s", cf.Path))
			}
		}
	}
}

// validateRoleToolRefs checks that every role references an existing tool.
func validateRoleToolRefs(c *Contract, r *ValidationResult) {
	for i, role := range c.Roles {
		if role.Tool == "" {
			r.addError(fmt.Sprintf("roles[%d].tool", i), "required", fmt.Sprintf("role %q has no tool reference", role.Name))
			continue
		}
		if _, ok := c.Tools[role.Tool]; !ok {
			r.addError(
				fmt.Sprintf("roles[%d].tool", i),
				"tool_exists",
				fmt.Sprintf("role %q references unknown tool %q", role.Name, role.Tool),
			)
		}
	}
}

// validateLayoutRoles checks that every layout pane references a defined role.
func validateLayoutRoles(c *Contract, r *ValidationResult) {
	roleNames := map[string]bool{}
	for _, role := range c.Roles {
		roleNames[role.Name] = true
	}
	for i, pane := range c.Layout.Panes {
		if !roleNames[pane.Role] {
			r.addError(
				fmt.Sprintf("layout.panes[%d].role", i),
				"role_exists",
				fmt.Sprintf("layout pane references undefined role %q", pane.Role),
			)
		}
	}
}

// validateRoleDuplication checks for duplicate role names.
func validateRoleDuplication(c *Contract, r *ValidationResult) {
	seen := map[string]bool{}
	for i, role := range c.Roles {
		if seen[role.Name] {
			r.addError(
				fmt.Sprintf("roles[%d].name", i),
				"unique",
				fmt.Sprintf("duplicate role name %q", role.Name),
			)
		}
		seen[role.Name] = true
	}
}

// validateToolSpecs checks internal consistency of tool specs.
func validateToolSpecs(c *Contract, r *ValidationResult) {
	validStrategies := map[string]bool{
		"separator_scan":      true,
		"last_nonempty_skip":  true,
	}
	validReadyPolicies := map[string]bool{
		"prompt_only":           true,
		"prompt_and_no_footer":  true,
		"footer_or_prompt":      true,
	}

	for _, tool := range c.Tools {
		prefix := fmt.Sprintf("tools.%s", tool.Name)

		if tool.PaneParser.Strategy != "" && !validStrategies[tool.PaneParser.Strategy] {
			r.addError(
				prefix+".pane_parser.strategy",
				"valid_strategy",
				fmt.Sprintf("unknown parser strategy %q", tool.PaneParser.Strategy),
			)
		}
		if tool.PaneParser.ReadyPolicy != "" && !validReadyPolicies[tool.PaneParser.ReadyPolicy] {
			r.addError(
				prefix+".pane_parser.ready_policy",
				"valid_policy",
				fmt.Sprintf("unknown ready policy %q", tool.PaneParser.ReadyPolicy),
			)
		}
		if tool.PaneParser.IdlePolicy != "" && !validReadyPolicies[tool.PaneParser.IdlePolicy] {
			r.addError(
				prefix+".pane_parser.idle_policy",
				"valid_policy",
				fmt.Sprintf("unknown idle policy %q", tool.PaneParser.IdlePolicy),
			)
		}

		for i, m := range tool.ConfigFiles {
			validFormats := map[string]bool{"json": true, "toml": true}
			if m.Format != "" && !validFormats[m.Format] {
				r.addError(
					fmt.Sprintf("%s.config_files[%d].format", prefix, i),
					"valid_format",
					fmt.Sprintf("unknown config format %q", m.Format),
				)
			}
		}

		validActions := map[string]bool{
			"replace": true, "merge_union": true,
			"ensure_exists": true, "ensure_table": true,
		}
		for i, cf := range tool.ConfigFiles {
			for j, mut := range cf.Mutations {
				if mut.Action != "" && !validActions[mut.Action] {
					r.addError(
						fmt.Sprintf("%s.config_files[%d].mutations[%d].action", prefix, i, j),
						"valid_action",
						fmt.Sprintf("unknown mutation action %q", mut.Action),
					)
				}
			}
		}

		if tool.Injection.SlashCommandMode != "" {
			valid := map[string]bool{"bare": true, "wrapped": true}
			if !valid[tool.Injection.SlashCommandMode] {
				r.addError(
					prefix+".injection.slash_command_mode",
					"valid_mode",
					fmt.Sprintf("unknown slash command mode %q", tool.Injection.SlashCommandMode),
				)
			}
		}

		// Validate mutation value types
		validValueTypes := map[string]bool{
			"string": true, "bool": true, "string_array": true, "object": true,
		}
		for i, cf := range tool.ConfigFiles {
			for j, mut := range cf.Mutations {
				if mut.ValueType != "" && !validValueTypes[mut.ValueType] {
					r.addError(
						fmt.Sprintf("%s.config_files[%d].mutations[%d].value_type", prefix, i, j),
						"valid_value_type",
						fmt.Sprintf("unknown value type %q", mut.ValueType),
					)
				}
			}
		}

		// Validate pre-inject action enums
		validWhen := map[string]bool{
			"always": true, "footer_visible": true, "suggestion_visible": true, "ready_false": true,
		}
		validInjectAction := map[string]bool{
			"send_key": true, "send_text": true, "sleep": true, "recapture": true,
		}
		for i, action := range tool.Injection.PreInjectActions {
			if action.When != "" && !validWhen[action.When] {
				r.addError(
					fmt.Sprintf("%s.injection.pre_inject_actions[%d].when", prefix, i),
					"valid_when",
					fmt.Sprintf("unknown inject condition %q", action.When),
				)
			}
			if action.Action != "" && !validInjectAction[action.Action] {
				r.addError(
					fmt.Sprintf("%s.injection.pre_inject_actions[%d].action", prefix, i),
					"valid_inject_action",
					fmt.Sprintf("unknown inject action %q", action.Action),
				)
			}
		}

		// Validate line matcher match_type enums
		validMatchType := map[string]bool{
			"contains_all": true, "prefix": true, "regex": true,
		}
		validateLineMatchers := func(matchers []LineMatcherSpec, fieldPath string) {
			for i, m := range matchers {
				if m.MatchType != "" && !validMatchType[m.MatchType] {
					r.addError(
						fmt.Sprintf("%s[%d].match_type", fieldPath, i),
						"valid_match_type",
						fmt.Sprintf("unknown match type %q", m.MatchType),
					)
				}
			}
		}
		pp := prefix + ".pane_parser"
		validateLineMatchers(tool.PaneParser.FooterMatchers, pp+".footer_matchers")
		validateLineMatchers(tool.PaneParser.StatuslineMatchers, pp+".statusline_matchers")
		validateLineMatchers(tool.PaneParser.SkipMatchers, pp+".skip_matchers")
		validateLineMatchers(tool.PaneParser.SuggestionMatchers, pp+".suggestion_matchers")
		for i, ce := range tool.PaneParser.ContextExtractors {
			validateLineMatchers(ce.RequireLineMatchers, fmt.Sprintf("%s.context_extractors[%d].require_line_matchers", pp, i))
		}

		// Validate text matcher match_type enums
		validTextMatchType := map[string]bool{
			"contains": true, "regex": true,
		}
		for i, m := range tool.PaneParser.CompactedMatchers {
			if m.MatchType != "" && !validTextMatchType[m.MatchType] {
				r.addError(
					fmt.Sprintf("%s.compacted_matchers[%d].match_type", pp, i),
					"valid_match_type",
					fmt.Sprintf("unknown text match type %q", m.MatchType),
				)
			}
		}
	}
}

// FormatHuman returns a human-readable validation report.
func (r *ValidationResult) FormatHuman() string {
	if r.Valid {
		return "Contract is valid."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Contract validation failed with %d error(s):\n", len(r.Errors))
	for i, e := range r.Errors {
		fmt.Fprintf(&b, "  %d. [%s] %s: %s\n", i+1, e.Check, e.Field, e.Message)
	}
	return b.String()
}
