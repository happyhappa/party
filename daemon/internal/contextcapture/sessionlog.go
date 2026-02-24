package contextcapture

import (
	"bufio"
	"encoding/json"
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

	scoped := codexScopePaths()
	if len(scoped) > 0 {
		if path, err := latestMatchingCodexCwd(matches, scoped); err == nil {
			return path, nil
		}
		// TODO: fallback is global latest when no scoped Codex session metadata matches.
	}

	return latestByMtime(matches)
}

func codexScopePaths() []string {
	if os.Getenv("RELAY_STATE_DIR") == "" {
		return nil
	}

	seen := map[string]struct{}{}
	var paths []string
	add := func(path string) {
		if path == "" {
			return
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return
		}
		if _, ok := seen[abs]; ok {
			return
		}
		seen[abs] = struct{}{}
		paths = append(paths, abs)
	}

	add(os.Getenv("PROJECT_DIR"))
	add(os.Getenv("RELAY_MAIN_DIR"))
	if wd, err := os.Getwd(); err == nil {
		add(wd)
	}
	return paths
}

func latestMatchingCodexCwd(paths, scopedRoots []string) (string, error) {
	if len(paths) == 0 {
		return "", errors.New("no session logs found")
	}

	var filtered []string
	for _, path := range paths {
		cwd, ok := codexSessionCwd(path)
		if !ok || cwd == "" {
			continue
		}
		cwd = filepath.Clean(cwd)
		for _, root := range scopedRoots {
			root = filepath.Clean(root)
			if cwd == root || strings.HasPrefix(cwd, root+string(os.PathSeparator)) {
				filtered = append(filtered, path)
				break
			}
		}
	}
	return latestByMtime(filtered)
}

func codexSessionCwd(path string) (string, bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	if !scanner.Scan() {
		return "", false
	}
	line := scanner.Bytes()

	var event struct {
		Type    string `json:"type"`
		Payload struct {
			Cwd string `json:"cwd"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(line, &event); err != nil {
		return "", false
	}
	if event.Type != "session_meta" || event.Payload.Cwd == "" {
		return "", false
	}
	return event.Payload.Cwd, true
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
