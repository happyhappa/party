// Package sessionmap implements session log discovery and mapping for multi-agent pods.
// It discovers Claude and Codex session logs per worktree/pane.
package sessionmap

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// DiscoverClaudeSessionLog finds the Claude session log for a specific worktree.
// Uses the worktree path to determine the encoded project directory name.
func DiscoverClaudeSessionLog(worktreePath string) (string, error) {
	abs, err := filepath.Abs(worktreePath)
	if err != nil {
		return "", fmt.Errorf("resolve worktree path: %w", err)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("get home dir: %w", err)
	}

	// Try encoded path candidates for the worktree
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

	return "", fmt.Errorf("no Claude session log found for worktree: %s", worktreePath)
}

// DiscoverCodexSessionLogByLsof finds the Codex session log by checking which
// files the Codex process has open. This is useful for mapping a tmux pane to
// its session log.
func DiscoverCodexSessionLogByLsof(pid int) (string, error) {
	if pid <= 0 {
		return "", errors.New("invalid pid")
	}

	// Run lsof to find open files for the process
	cmd := exec.Command("lsof", "-p", fmt.Sprintf("%d", pid), "-Fn")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("lsof failed: %w", err)
	}

	// Parse lsof output looking for .jsonl files in .codex/sessions
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	var sessionLogs []string

	for scanner.Scan() {
		line := scanner.Text()
		// lsof -Fn outputs 'n' prefix for file names
		if strings.HasPrefix(line, "n") {
			path := line[1:]
			if isCodexSessionLog(path) {
				sessionLogs = append(sessionLogs, path)
			}
		}
	}

	if len(sessionLogs) == 0 {
		return "", errors.New("no Codex session log found in open files")
	}

	// Return the most recently modified one if multiple
	return latestByMtime(sessionLogs)
}

// DiscoverCodexSessionLogByPaneID finds the Codex session log for a tmux pane.
// It gets the shell PID from tmux, then finds child processes, then uses lsof.
func DiscoverCodexSessionLogByPaneID(paneID string) (string, error) {
	// Get the shell PID for this pane
	cmd := exec.Command("tmux", "display-message", "-p", "-t", paneID, "#{pane_pid}")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("get pane pid: %w", err)
	}

	shellPid := strings.TrimSpace(string(output))
	if shellPid == "" {
		return "", errors.New("empty pane pid")
	}

	// Find child processes of the shell (the actual codex process)
	childPids, err := getChildPids(shellPid)
	if err != nil {
		return "", fmt.Errorf("get child pids: %w", err)
	}

	// Try lsof on each child process
	for _, pid := range childPids {
		if path, err := DiscoverCodexSessionLogByLsof(pid); err == nil {
			return path, nil
		}
	}

	// Fallback: try the shell pid itself
	var pidInt int
	fmt.Sscanf(shellPid, "%d", &pidInt)
	if path, err := DiscoverCodexSessionLogByLsof(pidInt); err == nil {
		return path, nil
	}

	return "", errors.New("no Codex session log found for pane")
}

// DiscoverCodexSessionLogByDir finds Codex session log by walking the sessions directory.
// This is a fallback when lsof isn't available or doesn't work.
func DiscoverCodexSessionLogByDir() (string, error) {
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
		if isCodexSessionLog(path) {
			matches = append(matches, path)
		}
		return nil
	})

	return latestByMtime(matches)
}

// getChildPids returns the PIDs of child processes for a given parent PID.
func getChildPids(parentPid string) ([]int, error) {
	// Use pgrep to find children
	cmd := exec.Command("pgrep", "-P", parentPid)
	output, err := cmd.Output()
	if err != nil {
		// pgrep returns exit 1 if no matches, which is not an error for us
		return nil, nil
	}

	var pids []int
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		var pid int
		if _, err := fmt.Sscanf(scanner.Text(), "%d", &pid); err == nil {
			pids = append(pids, pid)
			// Recursively get grandchildren
			grandchildren, _ := getChildPids(fmt.Sprintf("%d", pid))
			pids = append(pids, grandchildren...)
		}
	}

	return pids, nil
}

// isCodexSessionLog checks if a path looks like a Codex session log.
func isCodexSessionLog(path string) bool {
	if !strings.HasSuffix(path, ".jsonl") {
		return false
	}
	// Check for typical Codex session log patterns
	if strings.Contains(path, ".codex/sessions") {
		return true
	}
	if strings.Contains(path, "rollout-") {
		return true
	}
	return false
}

// latestByMtime returns the path with the most recent modification time.
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

// encodeClaudeProjectPathCandidates generates possible encoded directory names
// for a given absolute path.
func encodeClaudeProjectPathCandidates(abs string) []string {
	slashed := filepath.ToSlash(abs)
	base := strings.ReplaceAll(slashed, "/", "-")
	candidates := []string{base}

	// Also try with underscores converted
	alt := strings.ReplaceAll(base, "_", "-")
	if alt != base {
		candidates = append(candidates, alt)
	}

	// Try stripping leading dash if present
	if strings.HasPrefix(base, "-") {
		stripped := base[1:]
		candidates = append(candidates, stripped)
	}

	return candidates
}

// ValidateSessionLog checks if a path is a valid, readable session log.
func ValidateSessionLog(path string) error {
	if path == "" {
		return errors.New("empty path")
	}

	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat: %w", err)
	}

	if info.IsDir() {
		return errors.New("path is a directory")
	}

	if !strings.HasSuffix(path, ".jsonl") {
		return errors.New("not a .jsonl file")
	}

	// Try to open for reading
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	f.Close()

	return nil
}

// SessionLogType identifies the type of session log.
type SessionLogType string

const (
	SessionLogTypeClaude SessionLogType = "claude"
	SessionLogTypeCodex  SessionLogType = "codex"
	SessionLogTypeUnknown SessionLogType = "unknown"
)

// DetectSessionLogType determines if a session log is from Claude or Codex.
func DetectSessionLogType(path string) SessionLogType {
	if strings.Contains(path, ".claude/") {
		return SessionLogTypeClaude
	}
	if strings.Contains(path, ".codex/") || strings.Contains(path, "rollout-") {
		return SessionLogTypeCodex
	}
	return SessionLogTypeUnknown
}

// SessionLogPattern is a regex for matching session log filenames.
var SessionLogPattern = regexp.MustCompile(`^[a-f0-9-]+\.jsonl$|^rollout-[a-z0-9]+\.jsonl$`)
