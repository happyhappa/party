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
		if out, err := contextcapture.TailExtract(path, tailTokens, cfg.Recovery.TailBytesPerToken); err == nil {
			tailText = out
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

	fmt.Println("\n### Recent Activity (from tail capture)")
	fmt.Println(tailText)
}

func loadConfig(path string) (*contextcapture.Config, error) {
	if path != "" {
		return contextcapture.LoadFromPath(path)
	}
	return contextcapture.Load()
}

func fetchCheckpoint(role string) (string, string, string) {
	cmd, err := bdCommand("list", "--type", "checkpoint", "--label", "role:"+role, "--limit", "1", "--json")
	if err != nil {
		return "", "", ""
	}
	listOut, err := cmd.Output()
	if err != nil {
		return "", "", ""
	}

	checkpointID := parseCheckpointID(listOut)
	if checkpointID == "" {
		return "", "", ""
	}

	bodyCmd, err := bdCommand("show", checkpointID, "--body")
	if err != nil {
		return checkpointID, "", "beads"
	}
	body, _ := bodyCmd.Output()
	if len(body) == 0 {
		fallbackCmd, err := bdCommand("show", checkpointID)
		if err == nil {
			body, _ = fallbackCmd.Output()
		}
	}

	return checkpointID, strings.TrimSpace(string(body)), "beads"
}

func bdCommand(args ...string) (*exec.Cmd, error) {
	bdPath, err := exec.LookPath("bd")
	if err != nil {
		return nil, err
	}
	fullArgs := args
	if beadsDir := os.Getenv("BEADS_DIR"); beadsDir != "" {
		dbPath := filepath.Join(beadsDir, "beads.db")
		if _, err := os.Stat(dbPath); err == nil {
			fullArgs = append([]string{"--db", dbPath}, fullArgs...)
		}
	}
	return exec.Command(bdPath, fullArgs...), nil
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
