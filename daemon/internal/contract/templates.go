package contract

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"
)

func expandTemplate(raw string, vars map[string]string) (string, error) {
	if raw == "" {
		return "", nil
	}
	tmpl, err := template.New("value").Option("missingkey=error").Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse template %q: %w", raw, err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, vars); err != nil {
		return "", fmt.Errorf("execute template %q: %w", raw, err)
	}
	return buf.String(), nil
}

// ExpandPaths resolves template-bearing contract fields in place.
func ExpandPaths(c *Contract, vars map[string]string) error {
	if c == nil {
		return fmt.Errorf("contract is nil")
	}

	var err error
	if c.Session.Name, err = expandTemplate(c.Session.Name, vars); err != nil {
		return err
	}
	if c.Session.WindowName, err = expandTemplate(c.Session.WindowName, vars); err != nil {
		return err
	}

	if c.Paths.ShareDir, err = expandTemplate(c.Paths.ShareDir, vars); err != nil {
		return err
	}
	if c.Paths.StateDir, err = expandTemplate(c.Paths.StateDir, vars); err != nil {
		return err
	}
	if c.Paths.LogDir, err = expandTemplate(c.Paths.LogDir, vars); err != nil {
		return err
	}
	if c.Paths.InboxDir, err = expandTemplate(c.Paths.InboxDir, vars); err != nil {
		return err
	}
	if c.Paths.BeadsDir, err = expandTemplate(c.Paths.BeadsDir, vars); err != nil {
		return err
	}
	if c.Paths.PaneMap, err = expandTemplate(c.Paths.PaneMap, vars); err != nil {
		return err
	}
	if c.Paths.RelayLog, err = expandTemplate(c.Paths.RelayLog, vars); err != nil {
		return err
	}
	if c.Paths.ContractPath, err = expandTemplate(c.Paths.ContractPath, vars); err != nil {
		return err
	}
	if c.Paths.DocsDir, err = expandTemplate(c.Paths.DocsDir, vars); err != nil {
		return err
	}

	for i := range c.Roles {
		if c.Roles[i].WorktreeDir, err = expandTemplate(c.Roles[i].WorktreeDir, varsForRole(vars, c.Roles[i].Name)); err != nil {
			return err
		}
		if c.Roles[i].ProjectDir, err = expandTemplate(c.Roles[i].ProjectDir, varsForRole(vars, c.Roles[i].Name)); err != nil {
			return err
		}
		for k, v := range c.Roles[i].Env {
			ev, e := expandTemplate(v, varsForRole(vars, c.Roles[i].Name))
			if e != nil {
				return e
			}
			c.Roles[i].Env[k] = ev
		}
	}

	for toolName, tool := range c.Tools {
		for i, arg := range tool.Launch.Args {
			value, e := expandTemplate(arg, vars)
			if e != nil {
				return e
			}
			tool.Launch.Args[i] = value
		}
		for k, v := range tool.Launch.Env {
			value, e := expandTemplate(v, vars)
			if e != nil {
				return e
			}
			tool.Launch.Env[k] = value
		}
		for i, dir := range tool.Sandbox.AllowedWriteDirs {
			value, e := expandTemplate(dir, vars)
			if e != nil {
				return e
			}
			tool.Sandbox.AllowedWriteDirs[i] = value
		}
		// SidecarPath uses ${role} for runtime resolution — only expand
		// build-time {{.var}} templates here, ${role} passes through unchanged.
		if tool.Telemetry.SidecarPath, err = expandTemplate(tool.Telemetry.SidecarPath, vars); err != nil {
			return err
		}
		for i := range tool.ConfigFiles {
			if tool.ConfigFiles[i].Path, err = expandTemplate(tool.ConfigFiles[i].Path, vars); err != nil {
				return err
			}
			for j := range tool.ConfigFiles[i].Mutations {
				m := &tool.ConfigFiles[i].Mutations[j]
				if m.StringValue, err = expandTemplate(m.StringValue, vars); err != nil {
					return err
				}
				for k, v := range m.ObjectValue {
					value, e := expandTemplate(v, vars)
					if e != nil {
						return e
					}
					m.ObjectValue[k] = value
				}
				for idx, v := range m.ArrayValue {
					value, e := expandTemplate(v, vars)
					if e != nil {
						return e
					}
					m.ArrayValue[idx] = value
				}
			}
		}
		c.Tools[toolName] = tool
	}

	return nil
}

// ExpandRuntimeVars resolves ${key} placeholders that are deferred past
// build-time template expansion (e.g., ${role} in SidecarPath).
func ExpandRuntimeVars(s string, vars map[string]string) string {
	for k, v := range vars {
		s = strings.ReplaceAll(s, "${"+k+"}", v)
	}
	return s
}

func varsForRole(base map[string]string, role string) map[string]string {
	out := make(map[string]string, len(base)+1)
	for k, v := range base {
		out[k] = v
	}
	out["role"] = role
	return out
}
