package recycle

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	// DefaultMaxRawInput is the hard cap on raw bytes read from transcript (100KB).
	DefaultMaxRawInput int64 = 100 * 1024
	// DefaultBriefMinDelta is the minimum raw JSONL bytes to trigger a brief (10KB).
	DefaultBriefMinDelta int64 = 10 * 1024
)

// BriefOptions configures a single brief generation run.
type BriefOptions struct {
	Role           string
	TranscriptPath string
	StartOffset    int64
	EndOffset      int64 // 0 means EOF
	SessionID      string
	FilterPath     string // path to party-jsonl-filter binary
	PromptPath     string // path to party-brief-prompt.txt
	Generator      string // "codex" (default) or "claude"
	Source         string // "continuous" or "final"
	MaxRawInput    int64  // hard cap on raw bytes (default 100KB)
	BeadsDir       string // where to store bead files
}

// BriefResult holds the output of a brief generation.
type BriefResult struct {
	BeadID    string
	ByteRange [2]int64
	Content   string
}

// GenerateBrief creates a session brief from a transcript slice.
//
// Flow:
//  1. Read raw JSONL from StartOffset to EndOffset (capped at MaxRawInput)
//  2. Filter through party-jsonl-filter
//  3. Generate brief via codex exec (or claude --print)
//  4. Store as bead with identity metadata
func GenerateBrief(opts BriefOptions) (*BriefResult, error) {
	if opts.MaxRawInput <= 0 {
		opts.MaxRawInput = DefaultMaxRawInput
	}

	// Resolve end offset
	endOffset := opts.EndOffset
	if endOffset <= 0 {
		info, err := os.Stat(opts.TranscriptPath)
		if err != nil {
			return nil, fmt.Errorf("stat transcript: %w", err)
		}
		endOffset = info.Size()
	}

	delta := endOffset - opts.StartOffset
	if delta <= 0 {
		return nil, fmt.Errorf("no new transcript data (start=%d, end=%d)", opts.StartOffset, endOffset)
	}

	// Cap raw input
	readStart := opts.StartOffset
	if delta > opts.MaxRawInput {
		readStart = endOffset - opts.MaxRawInput
		delta = opts.MaxRawInput
	}

	// Read raw slice
	f, err := os.Open(opts.TranscriptPath)
	if err != nil {
		return nil, fmt.Errorf("open transcript: %w", err)
	}
	defer f.Close()

	if _, err := f.Seek(readStart, io.SeekStart); err != nil {
		return nil, fmt.Errorf("seek to offset %d: %w", readStart, err)
	}

	raw := make([]byte, delta)
	n, err := io.ReadFull(f, raw)
	if err != nil && err != io.ErrUnexpectedEOF {
		return nil, fmt.Errorf("read transcript slice: %w", err)
	}
	raw = raw[:n]

	// Filter through party-jsonl-filter
	filtered, err := runFilter(opts.FilterPath, raw)
	if err != nil {
		return nil, fmt.Errorf("filter transcript: %w", err)
	}
	if len(filtered) == 0 {
		return nil, fmt.Errorf("filter produced empty output")
	}

	// Read prompt
	prompt, err := os.ReadFile(opts.PromptPath)
	if err != nil {
		return nil, fmt.Errorf("read brief prompt: %w", err)
	}

	// Generate brief
	briefContent, err := runGenerator(opts.Generator, string(prompt), string(filtered))
	if err != nil {
		return nil, fmt.Errorf("generate brief: %w", err)
	}
	if strings.TrimSpace(briefContent) == "" {
		return nil, fmt.Errorf("generator produced empty output")
	}

	// Store as bead
	byteRange := [2]int64{readStart, endOffset}
	beadID, err := storeBriefBead(opts, briefContent, byteRange)
	if err != nil {
		// Brief content is still valid even if bead storage fails
		return &BriefResult{
			ByteRange: byteRange,
			Content:   briefContent,
		}, fmt.Errorf("store bead (brief generated but not persisted): %w", err)
	}

	return &BriefResult{
		BeadID:    beadID,
		ByteRange: byteRange,
		Content:   briefContent,
	}, nil
}

// runFilter pipes raw data through party-jsonl-filter.
func runFilter(filterPath string, raw []byte) ([]byte, error) {
	if filterPath == "" {
		// Try to find on PATH
		p, err := exec.LookPath("party-jsonl-filter")
		if err != nil {
			return nil, fmt.Errorf("party-jsonl-filter not found: %w", err)
		}
		filterPath = p
	}

	cmd := exec.Command(filterPath)
	cmd.Stdin = strings.NewReader(string(raw))
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("run %s: %w", filterPath, err)
	}
	return out, nil
}

// runGenerator calls the selected brief generator.
func runGenerator(generator, prompt, filteredTranscript string) (string, error) {
	fullPrompt := prompt + "\n\n" + filteredTranscript

	switch generator {
	case "", "codex":
		return runCodexExec(fullPrompt)
	case "claude":
		return runClaudePrint(fullPrompt)
	default:
		return "", fmt.Errorf("unknown generator %q (want codex or claude)", generator)
	}
}

// runCodexExec generates a brief using codex exec.
func runCodexExec(prompt string) (string, error) {
	codexPath, err := exec.LookPath("codex")
	if err != nil {
		return "", fmt.Errorf("codex not found: %w", err)
	}
	cmd := exec.Command(codexPath, "exec", prompt)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("codex exec: %w", err)
	}
	return string(out), nil
}

// runClaudePrint generates a brief using claude --print.
func runClaudePrint(prompt string) (string, error) {
	claudePath, err := exec.LookPath("claude")
	if err != nil {
		return "", fmt.Errorf("claude not found: %w", err)
	}
	cmd := exec.Command(claudePath, "--print", "-p", prompt)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("claude --print: %w", err)
	}
	return string(out), nil
}

// storeBriefBead stores the brief as a bead with identity metadata.
func storeBriefBead(opts BriefOptions, content string, byteRange [2]int64) (string, error) {
	bdPath, err := exec.LookPath("bd")
	if err != nil {
		return "", fmt.Errorf("bd not found: %w", err)
	}

	title := fmt.Sprintf("%s session brief %s", opts.Role, time.Now().Format("2006-01-02 15:04"))
	labels := []string{
		fmt.Sprintf("kind:session_brief"),
		fmt.Sprintf("role:%s", opts.Role),
		fmt.Sprintf("source:%s", opts.Source),
		fmt.Sprintf("session_id:%s", opts.SessionID),
		fmt.Sprintf("transcript_path:%s", opts.TranscriptPath),
		fmt.Sprintf("byte_range:%d-%d", byteRange[0], byteRange[1]),
		fmt.Sprintf("generator:%s", generatorLabel(opts.Generator)),
	}

	args := []string{"create", "--type", "session_brief", "--title", title}
	for _, l := range labels {
		args = append(args, "--label", l)
	}
	args = append(args, "--body", content, "--silent")

	cmd := exec.Command(bdPath, args...)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("bd create: %w", err)
	}

	beadID := strings.TrimSpace(string(out))
	return beadID, nil
}

func generatorLabel(g string) string {
	switch g {
	case "", "codex":
		return "codex-o4-mini"
	case "claude":
		return "claude-sonnet"
	default:
		return g
	}
}

// CleanupOldBriefs retains the most recent `keep` continuous briefs for a role,
// deleting older ones. Uses bead metadata labels for identification.
func CleanupOldBriefs(beadsDir, role string, keep int) error {
	if beadsDir == "" || keep < 0 {
		return nil
	}

	// Find brief bead files by scanning the beads directory
	pattern := filepath.Join(beadsDir, "*.json")
	files, err := filepath.Glob(pattern)
	if err != nil {
		return fmt.Errorf("glob beads: %w", err)
	}

	type beadFile struct {
		path    string
		modTime time.Time
	}
	var briefs []beadFile

	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		content := string(data)
		// Check if this is a continuous brief for the target role
		if strings.Contains(content, "kind:session_brief") &&
			strings.Contains(content, fmt.Sprintf("role:%s", role)) &&
			strings.Contains(content, "source:continuous") {
			info, err := os.Stat(f)
			if err != nil {
				continue
			}
			briefs = append(briefs, beadFile{path: f, modTime: info.ModTime()})
		}
	}

	if len(briefs) <= keep {
		return nil
	}

	// Sort newest first
	sort.Slice(briefs, func(i, j int) bool {
		return briefs[i].modTime.After(briefs[j].modTime)
	})

	// Delete older than keep
	for _, b := range briefs[keep:] {
		os.Remove(b.path)
	}

	return nil
}
