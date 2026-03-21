package contract

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type InitOptions struct {
	ProjectRoot  string
	MainDir      string
	ShareDir     string
	StateDir     string
	EnvOverrides map[string]string
}

// BuildContract constructs a fully resolved contract without writing it to disk.
func BuildContract(opts InitOptions) (*Contract, error) {
	projectRoot, err := resolveProjectRoot(opts.ProjectRoot)
	if err != nil {
		return nil, err
	}
	mainDir := opts.MainDir
	if strings.TrimSpace(mainDir) == "" {
		mainDir = filepath.Join(projectRoot, "main")
	} else if strings.TrimSpace(opts.ProjectRoot) == "" {
		projectRoot = filepath.Dir(mainDir)
	}
	shareDir := opts.ShareDir
	if strings.TrimSpace(shareDir) == "" {
		shareDir = filepath.Join(projectRoot, ".relay")
	}
	stateDir := opts.StateDir
	if strings.TrimSpace(stateDir) == "" {
		stateDir = filepath.Join(shareDir, "state")
	}

	c := DefaultContract(projectRoot, mainDir)
	c.GeneratedAt = time.Now().UTC()

	vars, err := buildTemplateVars(projectRoot, mainDir, shareDir, stateDir, opts.EnvOverrides)
	if err != nil {
		return nil, err
	}
	if err := ExpandPaths(c, vars); err != nil {
		return nil, err
	}
	c.Paths.ShareDir = shareDir
	c.Paths.StateDir = stateDir
	if c.Paths.LogDir == "" {
		c.Paths.LogDir = filepath.Join(shareDir, "log")
	}
	if c.Paths.InboxDir == "" {
		c.Paths.InboxDir = filepath.Join(shareDir, "outbox")
	}
	if c.Paths.ContractPath == "" {
		c.Paths.ContractPath = filepath.Join(stateDir, ContractFilename)
	}

	if err := c.ValidateBasic(); err != nil {
		return nil, err
	}
	return c, nil
}

func ShowContract(c *Contract) ([]byte, error) {
	return json.MarshalIndent(c, "", "  ")
}

func WriteContract(c *Contract, path string) error {
	if c == nil {
		return fmt.Errorf("contract is nil")
	}
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("contract path is required")
	}
	data, err := ShowContract(c)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func resolveProjectRoot(projectRoot string) (string, error) {
	if strings.TrimSpace(projectRoot) != "" {
		return filepath.Abs(projectRoot)
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for dir := wd; ; dir = filepath.Dir(dir) {
		if isPartyProjectRoot(dir) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
	}
	return wd, nil
}

func isPartyProjectRoot(dir string) bool {
	if dir == "" {
		return false
	}
	if stat, err := os.Stat(filepath.Join(dir, "daemon")); err != nil || !stat.IsDir() {
		return false
	}
	if stat, err := os.Stat(filepath.Join(dir, "bin")); err != nil || !stat.IsDir() {
		return false
	}
	return true
}

func buildTemplateVars(projectRoot, mainDir, shareDir, stateDir string, overrides map[string]string) (map[string]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	logDir := filepath.Join(shareDir, "log")
	inboxDir := filepath.Join(shareDir, "outbox")
	scriptsDir := filepath.Join(mainDir, "daemon", "scripts", "statusline")
	projectName := filepath.Base(projectRoot)
	vars := map[string]string{
		"home":         home,
		"project_root": projectRoot,
		"project_name": projectName,
		"main_dir":     mainDir,
		"share_dir":    shareDir,
		"state_dir":    stateDir,
		"log_dir":      logDir,
		"inbox_dir":    inboxDir,
		"scripts_dir":  scriptsDir,
		"session_name": "party-" + projectName,
		"beads_dir":    filepath.Join(mainDir, ".beads"),
		"role":         "",
	}
	for k, v := range overrides {
		vars[k] = v
	}
	return vars, nil
}
