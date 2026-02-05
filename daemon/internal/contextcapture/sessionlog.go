package contextcapture

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// DiscoverSessionLog resolves the session JSONL path using config, env, or auto-discovery.
func DiscoverSessionLog(cfg *Config) (string, error) {
	if cfg != nil && cfg.SessionLogPath != "" {
		return cfg.SessionLogPath, nil
	}
	if env := os.Getenv("SESSION_LOG_PATH"); env != "" {
		return env, nil
	}

	if path, err := discoverClaudeLog(); err == nil {
		return path, nil
	}

	if path, err := discoverCodexLog(); err == nil {
		return path, nil
	}

	return "", errors.New("no session log found")
}

func discoverClaudeLog() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return "", err
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	for _, encoded := range encodeClaudeProjectPathCandidates(abs) {
		pattern := filepath.Join(home, ".claude", "projects", encoded, "*.jsonl")
		matches, err := filepath.Glob(pattern)
		if err != nil {
			continue
		}
		if path, err := latestByMtime(matches); err == nil {
			return path, nil
		}
	}
	return "", errors.New("no Claude session logs found")
}

func discoverCodexLog() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	root := filepath.Join(home, ".codex", "sessions")
	var matches []string

	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if strings.HasPrefix(name, "rollout-") && strings.HasSuffix(name, ".jsonl") {
			matches = append(matches, path)
		}
		return nil
	})

	return latestByMtime(matches)
}

func latestByMtime(paths []string) (string, error) {
	if len(paths) == 0 {
		return "", errors.New("no session logs found")
	}

	var latestPath string
	var latestModTime int64

	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		mod := info.ModTime().UnixNano()
		if latestPath == "" || mod > latestModTime {
			latestPath = path
			latestModTime = mod
		}
	}

	if latestPath == "" {
		return "", fmt.Errorf("no readable session logs in %d candidates", len(paths))
	}
	return latestPath, nil
}

func encodeClaudeProjectPathCandidates(abs string) []string {
	slashed := filepath.ToSlash(abs)
	base := strings.ReplaceAll(slashed, "/", "-")
	candidates := []string{base}

	alt := strings.ReplaceAll(base, "_", "-")
	if alt != base {
		candidates = append(candidates, alt)
	}
	return candidates
}
