package contract

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// RegistryEntry represents a registered party session.
type RegistryEntry struct {
	ProjectName  string    `json:"project_name"`
	ContractPath string    `json:"contract_path"`
	ProjectRoot  string    `json:"project_root"`
	TmuxSession  string    `json:"tmux_session"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// FindOptions configures contract discovery.
type FindOptions struct {
	ExplicitPath  string
	RelayStateDir string
	ProjectName   string
	CWD           string
}

func RegisterSession(entry RegistryEntry) error {
	path, err := registryEntryPath(entry.ProjectName)
	if err != nil {
		return err
	}
	if entry.UpdatedAt.IsZero() {
		entry.UpdatedAt = time.Now().UTC()
	}
	data, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal registry entry: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

func DeregisterSession(projectName string) error {
	path, err := registryEntryPath(projectName)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func ListSessions() ([]RegistryEntry, error) {
	dir, err := registryDir()
	if err != nil {
		return nil, err
	}
	paths, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, err
	}
	entries := make([]RegistryEntry, 0, len(paths))
	for _, path := range paths {
		entry, err := loadRegistryEntry(path)
		if err != nil {
			log.Printf("warning: skipping malformed registry file %s: %v", path, err)
			continue
		}
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].ProjectName < entries[j].ProjectName
	})
	return entries, nil
}

func FindContractPath(opts FindOptions) (string, error) {
	if strings.TrimSpace(opts.ExplicitPath) != "" {
		return opts.ExplicitPath, nil
	}
	if strings.TrimSpace(opts.RelayStateDir) != "" {
		path := filepath.Join(opts.RelayStateDir, ContractFilename)
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}

	if strings.TrimSpace(opts.ProjectName) != "" {
		path, err := registryEntryPath(opts.ProjectName)
		if err != nil {
			return "", err
		}
		entry, err := loadRegistryEntry(path)
		if err != nil {
			if os.IsNotExist(err) {
				return "", fmt.Errorf("project %q not found in registry", opts.ProjectName)
			}
			return "", err
		}
		if contractPathExists(entry.ContractPath) {
			return entry.ContractPath, nil
		}
	}

	entries, err := ListSessions()
	if err != nil {
		return "", err
	}
	if len(entries) > 0 {
		validEntries := make([]RegistryEntry, 0, len(entries))
		for _, entry := range entries {
			if contractPathExists(entry.ContractPath) {
				validEntries = append(validEntries, entry)
			}
		}
		if strings.TrimSpace(opts.CWD) != "" {
			matches := make([]RegistryEntry, 0, len(validEntries))
			for _, entry := range validEntries {
				if cwdWithinRoot(entry.ProjectRoot, opts.CWD) {
					matches = append(matches, entry)
				}
			}
			if len(matches) == 1 {
				return matches[0].ContractPath, nil
			}
			if len(matches) > 1 {
				sort.Slice(matches, func(i, j int) bool {
					return len(matches[i].ProjectRoot) > len(matches[j].ProjectRoot)
				})
				return matches[0].ContractPath, nil
			}
		}
		if strings.TrimSpace(opts.CWD) != "" {
			if path, ok := findContractByWalkUp(opts.CWD); ok {
				return path, nil
			}
		}
		if len(validEntries) == 1 {
			return validEntries[0].ContractPath, nil
		}
		if len(validEntries) == 0 {
			if strings.TrimSpace(opts.CWD) != "" {
				if path, ok := findContractByWalkUp(opts.CWD); ok {
					return path, nil
				}
			}
			return "", nil
		}
		names := make([]string, 0, len(validEntries))
		for _, entry := range validEntries {
			names = append(names, entry.ProjectName)
		}
		return "", fmt.Errorf("multiple registered projects found; specify --project (%s)", strings.Join(names, ", "))
	}

	if strings.TrimSpace(opts.CWD) != "" {
		if path, ok := findContractByWalkUp(opts.CWD); ok {
			return path, nil
		}
	}
	return "", nil
}

func cwdWithinRoot(projectRoot, cwd string) bool {
	root, err := canonicalPath(projectRoot)
	if err != nil {
		return false
	}
	current, err := canonicalPath(cwd)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(root, current)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func registryDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "party", "sessions"), nil
}

func registryEntryPath(projectName string) (string, error) {
	if !validProjectName(projectName) {
		return "", fmt.Errorf("invalid project name %q", projectName)
	}
	dir, err := registryDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, projectName+".json"), nil
}

func loadRegistryEntry(path string) (RegistryEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return RegistryEntry{}, err
	}
	var entry RegistryEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return RegistryEntry{}, err
	}
	if strings.TrimSpace(entry.ProjectName) == "" {
		return RegistryEntry{}, fmt.Errorf("project_name is required")
	}
	if strings.TrimSpace(entry.ContractPath) == "" {
		return RegistryEntry{}, fmt.Errorf("contract_path is required")
	}
	if strings.TrimSpace(entry.ProjectRoot) == "" {
		return RegistryEntry{}, fmt.Errorf("project_root is required")
	}
	return entry, nil
}

func findContractByWalkUp(cwd string) (string, bool) {
	start, err := canonicalPath(cwd)
	if err != nil {
		return "", false
	}
	for dir := start; ; dir = filepath.Dir(dir) {
		path := filepath.Join(dir, ".relay", "state", ContractFilename)
		if _, err := os.Stat(path); err == nil {
			return path, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
	}
	return "", false
}

func canonicalPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err == nil {
		return resolved, nil
	}
	return filepath.Clean(abs), nil
}

func contractPathExists(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	if _, err := os.Stat(path); err == nil {
		return true
	}
	return false
}

func validProjectName(name string) bool {
	if strings.TrimSpace(name) == "" {
		return false
	}
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			continue
		}
		return false
	}
	return true
}
