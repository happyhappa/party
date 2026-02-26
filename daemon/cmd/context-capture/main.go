package main

import (
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

	checkpointID, checkpointBody, checkpointSource := fetchCheckpoint(role)
	if checkpointID == "" {
		checkpointID = "none"
	}
	if checkpointSource == "" {
		checkpointSource = "unknown"
	}

	// If primary source was a task bead, supplement with most recent session brief
	var sessionBriefSupplement string
	if strings.HasPrefix(checkpointSource, "task") {
		sessionBriefSupplement = fetchSessionBrief(role)
	}

	// Fetch summaries (Phase 2)
	var stateRollup, chunkSummaries string
	var lastSummaryOffset int64
	if *includeSummaries {
		stateRollup, _ = fetchLatestStateRollup(role)
		chunkSummaries, lastSummaryOffset = fetchRecentChunkSummaries(role, 3)
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

func fetchCheckpoint(role string) (string, string, string) {
	bdPath := os.ExpandEnv("$HOME/go/bin/bd")
	if _, err := exec.LookPath(bdPath); err != nil {
		return "", "", ""
	}

	// Primary: active task bead (in_progress first, then open, then blocked)
	for _, status := range []string{"in_progress", "open", "blocked"} {
		if id, body := queryBead(bdPath, "task", role, status); id != "" {
			return id, body, "task"
		}
	}

	// Fallback A: recently completed task (within 2h)
	twoHoursAgo := time.Now().Add(-2 * time.Hour).Format(time.RFC3339)
	if id, body := queryBeadWithExtra(bdPath, "task", role, "closed", "--closed-after", twoHoursAgo); id != "" {
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

// queryBead queries bd for the most recent bead of the given type, role, and optional status.
func queryBead(bdPath, beadType, role, status string) (string, string) {
	args := []string{"list", "--type", beadType, "--label", "role:" + role, "--limit", "1", "--json"}
	if status != "" {
		args = append(args, "--status", status)
	}
	return fetchBeadBody(bdPath, args)
}

// queryBeadWithExtra is like queryBead but appends extra flag pairs (e.g. --closed-after, value).
func queryBeadWithExtra(bdPath, beadType, role, status string, extra ...string) (string, string) {
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
	listOut, err := exec.Command(bdPath, listArgs...).Output()
	if err != nil {
		return "", ""
	}

	beadID := parseCheckpointID(listOut)
	if beadID == "" {
		return "", ""
	}

	body, _ := exec.Command(bdPath, "show", beadID, "--body").Output()
	if len(body) == 0 {
		body, _ = exec.Command(bdPath, "show", beadID).Output()
	}
	return beadID, strings.TrimSpace(string(body))
}

// fetchSessionBrief returns the body of the most recent session brief for a role.
func fetchSessionBrief(role string) string {
	bdPath := os.ExpandEnv("$HOME/go/bin/bd")
	if _, err := exec.LookPath(bdPath); err != nil {
		return ""
	}
	_, body := queryBeadByLabel(bdPath, role, "kind:session_brief")
	return body
}

// fetchLatestStateRollup retrieves the most recent state_rollup bead for a role.
func fetchLatestStateRollup(role string) (string, error) {
	bdPath := os.ExpandEnv("$HOME/go/bin/bd")
	if _, err := exec.LookPath(bdPath); err != nil {
		return "", err
	}

	listOut, err := exec.Command(bdPath, "list", "--type", "state_rollup", "--label", "role:"+role, "--limit", "1", "--json").Output()
	if err != nil {
		return "", err
	}

	beadID := parseCheckpointID(listOut)
	if beadID == "" {
		return "", nil
	}

	body, err := exec.Command(bdPath, "show", beadID, "--body").Output()
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(body)), nil
}

// fetchRecentChunkSummaries retrieves recent chunk_summary beads.
// Returns the concatenated summaries and the end_offset of the most recent chunk.
func fetchRecentChunkSummaries(role string, limit int) (string, int64) {
	bdPath := os.ExpandEnv("$HOME/go/bin/bd")
	if _, err := exec.LookPath(bdPath); err != nil {
		return "", 0
	}

	listOut, err := exec.Command(bdPath, "list", "--type", "chunk_summary", "--label", "role:"+role, "--limit", fmt.Sprintf("%d", limit), "--json").Output()
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

		body, err := exec.Command(bdPath, "show", beadID, "--body").Output()
		if err != nil {
			continue
		}
		summaries = append(summaries, strings.TrimSpace(string(body)))

		// Extract end_offset from labels
		if labels, ok := bead["labels"].(map[string]any); ok {
			if endOffset, ok := labels["end_offset"].(string); ok {
				var offset int64
				fmt.Sscanf(endOffset, "%d", &offset)
				if offset > maxOffset {
					maxOffset = offset
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
