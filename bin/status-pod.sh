#!/bin/bash
# status-pod.sh - Show pod health and status
# Displays session state, pane info, and bead sync status
#
# Usage: status-pod.sh [--name NAME] [--json]

set -euo pipefail

POD_NAME=""
JSON_OUTPUT=false
LLM_SHARE="${LLM_SHARE:-$HOME/llm-share}"

usage() {
    cat <<EOF
Usage: $(basename "$0") [OPTIONS]

Show pod health and status.

OPTIONS:
    --name NAME     Pod name (default: auto-detect)
    --json          Output as JSON
    --all           Show all pods
    -h, --help      Show this help

STATUS CHECKS:
    - tmux session running
    - Pane processes alive
    - Worktree health
    - Bead sync status
    - Recent relay activity

EXAMPLES:
    $(basename "$0")                  # Status of current pod
    $(basename "$0") --name myproj    # Status of specific pod
    $(basename "$0") --all            # List all pods
    $(basename "$0") --json           # Machine-readable output
EOF
    exit "${1:-0}"
}

SHOW_ALL=false

# Parse arguments
while [[ $# -gt 0 ]]; do
    case "$1" in
        --name)
            POD_NAME="$2"
            shift 2
            ;;
        --json)
            JSON_OUTPUT=true
            shift
            ;;
        --all)
            SHOW_ALL=true
            shift
            ;;
        -h|--help)
            usage 0
            ;;
        -*)
            echo "Error: Unknown option $1" >&2
            usage 1
            ;;
        *)
            echo "Error: Unexpected argument $1" >&2
            usage 1
            ;;
    esac
done

# Colors (only for non-JSON output)
if [[ "$JSON_OUTPUT" != "true" ]]; then
    GREEN='\033[0;32m'
    YELLOW='\033[0;33m'
    RED='\033[0;31m'
    BLUE='\033[0;34m'
    NC='\033[0m'
else
    GREEN=""
    YELLOW=""
    RED=""
    BLUE=""
    NC=""
fi

# Show all pods
if [[ "$SHOW_ALL" == "true" ]]; then
    PODS_DIR="$LLM_SHARE/pods"

    if [[ ! -d "$PODS_DIR" ]]; then
        echo "No pods found."
        exit 0
    fi

    echo -e "${BLUE}=== All Pods ===${NC}"
    echo ""

    for manifest in "$PODS_DIR"/*/manifest.json; do
        [[ -f "$manifest" ]] || continue
        pod_name=$(dirname "$manifest" | xargs basename)
        state_file="$(dirname "$manifest")/state.json"

        # Get status
        status="unknown"
        if [[ -f "$state_file" ]]; then
            status=$(grep -o '"status": *"[^"]*"' "$state_file" 2>/dev/null | cut -d'"' -f4 || echo "unknown")
        fi

        # Check if actually running
        session="pod-$pod_name"
        if tmux has-session -t "$session" 2>/dev/null; then
            status="running"
        elif [[ "$status" == "running" ]]; then
            status="stale"
        fi

        # Color code status
        case "$status" in
            running) status_color="${GREEN}$status${NC}" ;;
            stopped) status_color="${YELLOW}$status${NC}" ;;
            stale)   status_color="${RED}$status${NC}" ;;
            *)       status_color="$status" ;;
        esac

        printf "  %-20s %s\n" "$pod_name" "$status_color"
    done

    exit 0
fi

# Auto-detect pod name
if [[ -z "$POD_NAME" ]]; then
    if [[ -f ".pod-name" ]]; then
        POD_NAME=$(cat ".pod-name")
    else
        PANE_MAP="$LLM_SHARE/relay/state/panes.json"
        if [[ -f "$PANE_MAP" ]]; then
            POD_NAME=$(grep -o '"pod":"[^"]*"' "$PANE_MAP" 2>/dev/null | cut -d'"' -f4 || true)
        fi
    fi
fi

if [[ -z "$POD_NAME" ]]; then
    echo "Error: Could not determine pod name." >&2
    echo "Use --name to specify, or --all to list pods." >&2
    exit 1
fi

POD_STATE_DIR="$LLM_SHARE/pods/$POD_NAME"
MANIFEST="$POD_STATE_DIR/manifest.json"
STATE_FILE="$POD_STATE_DIR/state.json"
SESSION="pod-$POD_NAME"

# Check if pod exists
if [[ ! -f "$MANIFEST" ]]; then
    echo "Error: Pod '$POD_NAME' not found." >&2
    exit 1
fi

# Gather status information
SESSION_RUNNING=false
if tmux has-session -t "$SESSION" 2>/dev/null; then
    SESSION_RUNNING=true
fi

# Get worktree paths
OC_DIR=$(grep -o '"oc": *"[^"]*"' "$MANIFEST" | cut -d'"' -f4)
CC_DIR=$(grep -o '"cc": *"[^"]*"' "$MANIFEST" | cut -d'"' -f4)
CX_DIR=$(grep -o '"cx": *"[^"]*"' "$MANIFEST" | cut -d'"' -f4)

# Check worktree health
check_worktree() {
    local dir="$1"
    local role="$2"

    if [[ ! -d "$dir" ]]; then
        echo "missing"
        return
    fi

    # Check if git worktree is valid
    if ! git -C "$dir" rev-parse --git-dir &>/dev/null; then
        echo "invalid"
        return
    fi

    # Check for uncommitted changes
    if ! git -C "$dir" diff --quiet 2>/dev/null; then
        echo "dirty"
        return
    fi

    echo "healthy"
}

OC_HEALTH=$(check_worktree "$OC_DIR" "oc")
CC_HEALTH=$(check_worktree "$CC_DIR" "cc")
CX_HEALTH=$(check_worktree "$CX_DIR" "cx")

# Count beads
count_beads() {
    local dir="$1"
    if [[ -d "$dir/.beads" ]]; then
        find "$dir/.beads" -name "*.json" -o -name "*.md" 2>/dev/null | wc -l
    else
        echo "0"
    fi
}

OC_BEADS=$(count_beads "$OC_DIR")
CC_BEADS=$(count_beads "$CC_DIR")
CX_BEADS=$(count_beads "$CX_DIR")

# Get last state change
LAST_CHANGE=""
if [[ -f "$STATE_FILE" ]]; then
    LAST_CHANGE=$(grep -o '"stopped_at": *"[^"]*"\|"started_at": *"[^"]*"' "$STATE_FILE" 2>/dev/null | head -1 | cut -d'"' -f4 || true)
fi

# JSON output
if [[ "$JSON_OUTPUT" == "true" ]]; then
    cat <<JSON
{
    "pod_name": "$POD_NAME",
    "session": "$SESSION",
    "running": $SESSION_RUNNING,
    "worktrees": {
        "oc": {"path": "$OC_DIR", "health": "$OC_HEALTH", "beads": $OC_BEADS},
        "cc": {"path": "$CC_DIR", "health": "$CC_HEALTH", "beads": $CC_BEADS},
        "cx": {"path": "$CX_DIR", "health": "$CX_HEALTH", "beads": $CX_BEADS}
    },
    "last_change": "$LAST_CHANGE"
}
JSON
    exit 0
fi

# Human-readable output
echo -e "${BLUE}=== Pod Status: $POD_NAME ===${NC}"
echo ""

# Session status
if [[ "$SESSION_RUNNING" == "true" ]]; then
    echo -e "Session:    ${GREEN}running${NC} ($SESSION)"

    # Show pane info
    echo ""
    echo "Panes:"
    tmux list-panes -t "$SESSION:main" -F "  #{pane_index}: #{pane_title} (#{pane_id}) - #{pane_current_command}" 2>/dev/null || true
else
    echo -e "Session:    ${YELLOW}stopped${NC}"
fi

echo ""
echo "Worktrees:"

health_color() {
    case "$1" in
        healthy) echo "${GREEN}$1${NC}" ;;
        dirty)   echo "${YELLOW}$1${NC}" ;;
        *)       echo "${RED}$1${NC}" ;;
    esac
}

printf "  OC: %-50s %s (beads: %s)\n" "$OC_DIR" "$(health_color "$OC_HEALTH")" "$OC_BEADS"
printf "  CC: %-50s %s (beads: %s)\n" "$CC_DIR" "$(health_color "$CC_HEALTH")" "$CC_BEADS"
printf "  CX: %-50s %s (beads: %s)\n" "$CX_DIR" "$(health_color "$CX_HEALTH")" "$CX_BEADS"

if [[ -n "$LAST_CHANGE" ]]; then
    echo ""
    echo "Last change: $LAST_CHANGE"
fi

# Check relay activity
RELAY_LOG="$LLM_SHARE/relay/log/events.jsonl"
if [[ -f "$RELAY_LOG" ]]; then
    LAST_EVENT=$(tail -1 "$RELAY_LOG" 2>/dev/null | grep -o '"ts_ms":[0-9]*' | cut -d: -f2 || true)
    if [[ -n "$LAST_EVENT" ]]; then
        NOW_MS=$(($(date +%s) * 1000))
        AGE_MS=$((NOW_MS - LAST_EVENT))
        AGE_SEC=$((AGE_MS / 1000))
        echo ""
        echo "Last relay event: ${AGE_SEC}s ago"
    fi
fi
