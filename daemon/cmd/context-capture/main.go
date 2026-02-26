package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/norm/relay-daemon/internal/contextcapture"
)

const bdTimeout = 10 * time.Second

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	sub := os.Args[1]
	switch sub {
	case "tail":
		runTail(os.Args[2:])
	case "checkpoint-template":
		runCheckpointTemplate()
	case "restore-render":
		runRestoreRender(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "Usage: context-capture <tail|checkpoint-template|restore-render> [flags]")
}

func runTail(args []string) {
	fs := flag.NewFlagSet("tail", flag.ExitOnError)
	configPath := fs.String("config", "", "config file path")
	tokens := fs.Int("tokens", 0, "override tail token count")
	_ = fs.Parse(args)

	cfg, err := loadConfig(*configPath)
	if err != nil {
		exitErr(err)
	}
	path, err := contextcapture.DiscoverSessionLog(cfg)
	if err != nil {
		exitErr(err)
	}

	tailTokens := cfg.Recovery.TailTokens
	if *tokens > 0 {
		tailTokens = *tokens
	}

	out, err := contextcapture.TailExtract(path, tailTokens, cfg.Recovery.TailBytesPerToken)
	if err != nil {
		exitErr(err)
	}
	fmt.Println(out)
}

func runCheckpointTemplate() {
	fmt.Println(`# Checkpoint

**Generated:** {timestamp}
**Role:** {role}
**Checkpoint ID:** {chk_id}
**Plan:** {plan_id or "none"}

## Current Goal
[1-2 sentences: What we're trying to accomplish right now]

## Key Decisions
[Bullet list: Decisions made and why, constraints chosen]

## Blockers
[Bullet list: What's preventing progress, open questions]

## Next Steps
[Numbered list: Immediate actions in priority order]

---
*Wisp: {session_log_path} [bytes {start}-{end}] | Prev: {prev_chk_id}*`)
}

func runRestoreRender(args []string) {
	fs := flag.NewFlagSet("restore-render", flag.ExitOnError)
	configPath := fs.String("config", "", "config file path")
	tokens := fs.Int("tokens", 0, "override tail token count")
	includeSummaries := fs.Bool("summaries", true, "include chunk summaries and rollups")
	_ = fs.Parse(args)

	cfg, err := loadConfig(*configPath)
	if err != nil {
		exitErr(err)
	}

	role := os.Getenv("AGENT_ROLE")
	if role == "" {
		role = "unknown"
	}

	repo := "unknown"
	if cwd, err := os.Getwd(); err == nil {
		repo = filepath.Base(cwd)
	}

	bdPath := resolveBDPath()

	checkpointID, checkpointBody, checkpointSource := fetchCheckpoint(bdPath, role)
	if checkpointID == "" {
		checkpointID = "none"
	}
	if checkpointSource == "" {
		checkpointSource = "unknown"
	}

	// If primary source was a task bead, supplement with most recent session brief
	var sessionBriefSupplement string
	if strings.HasPrefix(checkpointSource, "task") && bdPath != "" {
		_, sessionBriefSupplement = queryBeadByLabel(bdPath, role, "kind:session_brief")
	}

	// Fetch summaries (Phase 2)
	var stateRollup, chunkSummaries string
	var lastSummaryOffset int64
	if *includeSummaries && bdPath != "" {
		stateRollup, _ = fetchLatestStateRollup(bdPath, role)
		chunkSummaries, lastSummaryOffset = fetchRecentChunkSummaries(bdPath, role, 3)
	}

	path, err := contextcapture.DiscoverSessionLog(cfg)
	if err != nil {
		path = ""
	}

	tailTokens := cfg.Recovery.TailTokens
	if *tokens > 0 {
		tailTokens = *tokens
	}

	tailText := ""
	if path != "" {
		// If we have summaries, skip content already covered (overlap skip)
		startOffset := lastSummaryOffset
		if out, err := contextcapture.TailExtractFromOffset(path, tailTokens, cfg.Recovery.TailBytesPerToken, startOffset); err == nil {
			tailText = out
		} else {
			// Fallback to regular tail
			if out, err := contextcapture.TailExtract(path, tailTokens, cfg.Recovery.TailBytesPerToken); err == nil {
				tailText = out
			}
		}
	}
	if tailText == "" {
		tailText = "(tail unavailable)"
	}

	// Render output
	fmt.Println("## Recovery Context")
	fmt.Printf("**Checkpoint:** %s (%s, %s)\n", checkpointID, checkpointSource, "age unknown")
	fmt.Printf("**Role:** %s\n", role)
	fmt.Printf("**Repo:** %s\n\n", repo)

	fmt.Println("### State Summary (from checkpoint)")
	if checkpointBody == "" {
		fmt.Println("(no checkpoint found)")
	} else {
		fmt.Println(strings.TrimSpace(checkpointBody))
	}

	// Supplement: session brief context when primary source is a task bead
	if sessionBriefSupplement != "" {
		fmt.Println("\n### Session Brief (supplement)")
		fmt.Println(strings.TrimSpace(sessionBriefSupplement))
	}

	// Phase 2: Include summaries section
	if stateRollup != "" || chunkSummaries != "" {
		fmt.Println("\n### Session Summaries")
		if stateRollup != "" {
			fmt.Println("#### State Rollup")
			fmt.Println(strings.TrimSpace(stateRollup))
		}
		if chunkSummaries != "" {
			fmt.Println("\n#### Recent Chunks")
			fmt.Println(strings.TrimSpace(chunkSummaries))
		}
	}

	fmt.Println("\n### Recent Activity (from tail capture)")
	if lastSummaryOffset > 0 {
		fmt.Printf("*(starting from byte %d to avoid overlap with summaries)*\n\n", lastSummaryOffset)
	}
	fmt.Println(tailText)
}

func loadConfig(path string) (*contextcapture.Config, error) {
	if path != "" {
		return contextcapture.LoadFromPath(path)
	}
	return contextcapture.Load()
}

// resolveBDPath finds the bd binary, returning empty string if not found.
func resolveBDPath() string {
	bdPath := os.ExpandEnv("$HOME/go/bin/bd")
	if _, err := exec.LookPath(bdPath); err != nil {
		return ""
	}
	return bdPath
}

// bdRun executes a bd command with a timeout and returns its output.
func bdRun(bdPath string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), bdTimeout)
	defer cancel()
	return exec.CommandContext(ctx, bdPath, args...).Output()
}

func fetchCheckpoint(bdPath, role string) (string, string, string) {
	if bdPath == "" {
		return "", "", ""
	}

	// Primary: active task bead — single query, filter active statuses in Go
	if id, body := queryActiveTaskBead(bdPath, role); id != "" {
		return id, body, "task"
	}

	// Fallback A: recently completed task (within 2h)
	twoHoursAgo := time.Now().Add(-2 * time.Hour).Format(time.RFC3339)
	if id, body := queryBead(bdPath, "task", role, "completed", "--created-after", twoHoursAgo); id != "" {
		return id, body, "task_completed"
	}

	// Fallback B (transitional): legacy recovery or checkpoint beads
	if id, body := queryBead(bdPath, "recovery", role, ""); id != "" {
		return id, body, "recovery"
	}
	if id, body := queryBead(bdPath, "checkpoint", role, ""); id != "" {
		return id, body, "checkpoint"
	}

	// Fallback C: session brief
	if id, body := queryBeadByLabel(bdPath, role, "kind:session_brief"); id != "" {
		return id, body, "session_brief"
	}

	return "", "", ""
}

// queryActiveTaskBead queries all task beads for a role in a single bd call,
// then filters for active statuses (open, in_progress, blocked) and picks the newest.
func queryActiveTaskBead(bdPath, role string) (string, string) {
	activeStatuses := map[string]bool{"open": true, "in_progress": true, "blocked": true}

	listOut, err := bdRun(bdPath,"list", "--type", "task", "--label", "role:"+role, "--limit", "10", "--json")
	if err != nil {
		return "", ""
	}

	var beads []map[string]any
	if err := json.Unmarshal(listOut, &beads); err != nil {
		return "", ""
	}

	var bestID, bestCreated string
	for _, bead := range beads {
		status, _ := bead["status"].(string)
		if !activeStatuses[status] {
			continue
		}
		id := firstID(bead)
		if id == "" {
			continue
		}
		createdAt, _ := bead["created_at"].(string)
		if bestID == "" || createdAt > bestCreated {
			bestID = id
			bestCreated = createdAt
		}
	}
	if bestID == "" {
		return "", ""
	}
	return bestID, fetchBody(bdPath, bestID)
}

// queryBead queries bd for the most recent bead of the given type, role, and optional
// status. Extra flag pairs (e.g. "--created-after", value) can be appended.
func queryBead(bdPath, beadType, role, status string, extra ...string) (string, string) {
	args := []string{"list", "--type", beadType, "--label", "role:" + role, "--limit", "1", "--json"}
	if status != "" {
		args = append(args, "--status", status)
	}
	args = append(args, extra...)
	return fetchBeadBody(bdPath, args)
}

// queryBeadByLabel queries bd filtering by an additional label (no type filter).
func queryBeadByLabel(bdPath, role, label string) (string, string) {
	args := []string{"list", "--label", "role:" + role, "--label", label, "--limit", "1", "--json"}
	return fetchBeadBody(bdPath, args)
}

// fetchBeadBody runs a bd list query and fetches the body of the first result.
func fetchBeadBody(bdPath string, listArgs []string) (string, string) {
	listOut, err := bdRun(bdPath,listArgs...)
	if err != nil {
		return "", ""
	}

	beadID := parseCheckpointID(listOut)
	if beadID == "" {
		return "", ""
	}

	return beadID, fetchBody(bdPath, beadID)
}

// fetchBody retrieves the body of a bead by ID.
func fetchBody(bdPath, beadID string) string {
	body, _ := bdRun(bdPath,"show", beadID, "--body")
	if len(body) == 0 {
		body, _ = bdRun(bdPath,"show", beadID)
	}
	return strings.TrimSpace(string(body))
}

// fetchLatestStateRollup retrieves the most recent state_rollup bead for a role.
func fetchLatestStateRollup(bdPath, role string) (string, error) {
	listOut, err := bdRun(bdPath,"list", "--type", "state_rollup", "--label", "role:"+role, "--limit", "1", "--json")
	if err != nil {
		return "", err
	}

	beadID := parseCheckpointID(listOut)
	if beadID == "" {
		return "", nil
	}

	body, err := bdRun(bdPath,"show", beadID, "--body")
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(body)), nil
}

// fetchRecentChunkSummaries retrieves recent chunk_summary beads.
// Returns the concatenated summaries and the end_offset of the most recent chunk.
func fetchRecentChunkSummaries(bdPath, role string, limit int) (string, int64) {
	listOut, err := bdRun(bdPath,"list", "--type", "chunk_summary", "--label", "role:"+role, "--limit", fmt.Sprintf("%d", limit), "--json")
	if err != nil {
		return "", 0
	}

	var beads []map[string]any
	if err := json.Unmarshal(listOut, &beads); err != nil {
		return "", 0
	}

	if len(beads) == 0 {
		return "", 0
	}

	var summaries []string
	var maxOffset int64

	for _, bead := range beads {
		beadID := firstID(bead)
		if beadID == "" {
			continue
		}

		body, err := bdRun(bdPath,"show", beadID, "--body")
		if err != nil {
			continue
		}
		summaries = append(summaries, strings.TrimSpace(string(body)))

		// Extract end_offset from labels (bd returns labels as []string, e.g. ["end_offset:12345"])
		if labelsRaw, ok := bead["labels"].([]any); ok {
			for _, l := range labelsRaw {
				str, ok := l.(string)
				if !ok {
					continue
				}
				if strings.HasPrefix(str, "end_offset:") {
					var offset int64
					fmt.Sscanf(strings.TrimPrefix(str, "end_offset:"), "%d", &offset)
					if offset > maxOffset {
						maxOffset = offset
					}
				}
			}
		}
	}

	return strings.Join(summaries, "\n\n---\n\n"), maxOffset
}

func parseCheckpointID(raw []byte) string {
	var list []map[string]any
	if err := json.Unmarshal(raw, &list); err == nil {
		if len(list) > 0 {
			return firstID(list[0])
		}
		return ""
	}

	var single map[string]any
	if err := json.Unmarshal(raw, &single); err == nil {
		return firstID(single)
	}

	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) > 0 {
			return fields[0]
		}
	}
	return ""
}

func firstID(m map[string]any) string {
	for _, key := range []string{"id", "bead_id", "checkpoint_id", "chk_id"} {
		if val, ok := m[key]; ok {
			if str, ok := val.(string); ok {
				return str
			}
		}
	}
	return ""
}

func exitErr(err error) {
	if errors.Is(err, flag.ErrHelp) {
		os.Exit(2)
	}
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
