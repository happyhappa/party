#!/bin/bash
# start-pod.sh - Start a pod's tmux session with all agents
# Creates 3-tier layout with each pane in its worktree
#
# Usage: start-pod.sh [--name NAME] [--attach]

set -euo pipefail

POD_NAME=""
ATTACH=true
LLM_SHARE="${LLM_SHARE:-$HOME/llm-share}"

usage() {
    cat <<EOF
Usage: $(basename "$0") [OPTIONS]

Start a pod's tmux session with all agent panes.

OPTIONS:
    --name NAME     Pod name (default: auto-detect from .pod-name)
    --no-attach     Don't attach to session after creation
    -h, --help      Show this help

LAYOUT:
    ┌────────────┬────────────────────┐
    │            │        CC (50%)    │
    │     OC     ├────────────────────┤
    │            │        CX (50%)    │
    └────────────┴────────────────────┘

Each pane:
    - Runs in its worktree directory
    - Has AGENT_ROLE environment variable set
    - Has POD_NAME environment variable set

EXAMPLES:
    $(basename "$0")                  # Start pod (auto-detect name)
    $(basename "$0") --name myproj    # Start specific pod
    $(basename "$0") --no-attach      # Start without attaching
EOF
    exit "${1:-0}"
}

# Parse arguments
while [[ $# -gt 0 ]]; do
    case "$1" in
        --name)
            POD_NAME="$2"
            shift 2
            ;;
        --no-attach)
            ATTACH=false
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

# Auto-detect pod name from current directory
if [[ -z "$POD_NAME" ]]; then
    if [[ -f ".pod-name" ]]; then
        POD_NAME=$(cat ".pod-name")
    else
        # Try to find a pod for current repo by checking manifest files
        REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null) || true
        if [[ -n "$REPO_ROOT" ]]; then
            REPO_NAME=$(basename "$REPO_ROOT")
            # Check if a pod manifest exists for this repo
            for manifest in "$LLM_SHARE/pods"/*/manifest.json; do
                [[ -f "$manifest" ]] || continue
                if grep -q "\"$REPO_NAME\"" "$manifest" 2>/dev/null; then
                    POD_NAME=$(basename "$(dirname "$manifest")")
                    break
                fi
            done
            # Fallback: use repo name as pod name if pod dir exists
            if [[ -z "$POD_NAME" && -d "$LLM_SHARE/pods/$REPO_NAME" ]]; then
                POD_NAME="$REPO_NAME"
            fi
        fi
    fi
fi

if [[ -z "$POD_NAME" ]]; then
    echo "Error: Could not determine pod name." >&2
    echo "Use --name to specify, or run from a pod worktree." >&2
    exit 1
fi

# Load pod manifest
MANIFEST="$LLM_SHARE/pods/$POD_NAME/manifest.json"
if [[ ! -f "$MANIFEST" ]]; then
    echo "Error: Pod manifest not found: $MANIFEST" >&2
    echo "Create the pod first with: create-pod.sh --name $POD_NAME" >&2
    exit 1
fi

# Parse manifest (simple JSON parsing)
OC_DIR=$(grep -o '"oc": *"[^"]*"' "$MANIFEST" | cut -d'"' -f4)
CC_DIR=$(grep -o '"cc": *"[^"]*"' "$MANIFEST" | cut -d'"' -f4)
CX_DIR=$(grep -o '"cx": *"[^"]*"' "$MANIFEST" | cut -d'"' -f4)

# Verify worktrees exist
for dir in "$OC_DIR" "$CC_DIR" "$CX_DIR"; do
    if [[ ! -d "$dir" ]]; then
        echo "Error: Worktree not found: $dir" >&2
        echo "Recreate the pod with: create-pod.sh --name $POD_NAME" >&2
        exit 1
    fi
done

SESSION="pod-$POD_NAME"

# Colors
GREEN='\033[0;32m'
RED='\033[0;31m'
NC='\033[0m'

log() {
    echo -e "${GREEN}[start-pod]${NC} $1"
}

err() {
    echo -e "${RED}[start-pod]${NC} $1" >&2
}

# Check if session already exists
if tmux has-session -t "$SESSION" 2>/dev/null; then
    log "Session '$SESSION' already exists."
    if [[ "$ATTACH" == "true" ]]; then
        log "Attaching..."
        exec tmux attach -t "$SESSION"
    else
        log "Use: tmux attach -t $SESSION"
        exit 0
    fi
fi

# Agent commands (can be overridden via config)
CONFIG_FILE="$HOME/.config/relay/party.conf"
[[ -f "$CONFIG_FILE" ]] && source "$CONFIG_FILE"

OC_CMD="${RELAY_OC_CMD:-claude -c --dangerously-skip-permissions}"
CC_CMD="${RELAY_CC_CMD:-claude --dangerously-skip-permissions}"
CX_CMD="${RELAY_CX_CMD:-codex -a never -s workspace-write --add-dir /tmp --add-dir ~/llm-share --add-dir ~/.cache}"

log "Starting pod '$POD_NAME'..."

# Ensure relay log exists
RELAY_LOG="$LLM_SHARE/relay/log/events.jsonl"
mkdir -p "$(dirname "$RELAY_LOG")"
touch "$RELAY_LOG"

# Create session with OC pane (left side)
tmux new-session -d -s "$SESSION" -n main -c "$OC_DIR"

# Split right side (60% width)
tmux split-window -h -l 60% -t "$SESSION:main.0" -c "$CC_DIR"

# Split right column into CC (top) and CX (bottom)
tmux split-window -v -l 50% -t "$SESSION:main.1" -c "$CX_DIR"

# Name panes
tmux select-pane -t "$SESSION:main.0" -T "OC"
tmux select-pane -t "$SESSION:main.1" -T "CC"
tmux select-pane -t "$SESSION:main.2" -T "CX"

# Set pane border format
tmux set-option -t "$SESSION" pane-border-status top
tmux set-option -t "$SESSION" pane-border-format " #{pane_title} [pod:$POD_NAME] "

# Get pane IDs
OC_PANE=$(tmux display-message -p -t "$SESSION:main.0" '#{pane_id}')
CC_PANE=$(tmux display-message -p -t "$SESSION:main.1" '#{pane_id}')
CX_PANE=$(tmux display-message -p -t "$SESSION:main.2" '#{pane_id}')

# Set @role pane options
tmux set-option -p -t "$OC_PANE" @role oc
tmux set-option -p -t "$CC_PANE" @role cc
tmux set-option -p -t "$CX_PANE" @role cx

# Write panes.json for relay daemon
PANE_MAP="$LLM_SHARE/relay/state/panes.json"
mkdir -p "$(dirname "$PANE_MAP")"
printf '{"oc":"%s","cc":"%s","cx":"%s","pod":"%s","session":"%s"}\n' \
    "$OC_PANE" "$CC_PANE" "$CX_PANE" "$POD_NAME" "$SESSION" > "$PANE_MAP"

log "Pane map written to $PANE_MAP"

# Update pod state
POD_STATE="$LLM_SHARE/pods/$POD_NAME/state.json"
cat > "$POD_STATE" <<STATE
{
    "status": "running",
    "session": "$SESSION",
    "started_at": "$(date -u +"%Y-%m-%dT%H:%M:%SZ")",
    "panes": {
        "oc": "$OC_PANE",
        "cc": "$CC_PANE",
        "cx": "$CX_PANE"
    }
}
STATE

# Select OC pane as default
tmux select-pane -t "$SESSION:main.0"

log ""
log "Launching agents with AGENT_ROLE and POD_NAME set..."

# Launch agents with environment variables (3.2.3: AGENT_ROLE injection)
tmux send-keys -t "$SESSION:main.0" "export AGENT_ROLE=oc POD_NAME=$POD_NAME && $OC_CMD" Enter
tmux send-keys -t "$SESSION:main.1" "export AGENT_ROLE=cc POD_NAME=$POD_NAME && $CC_CMD" Enter
tmux send-keys -t "$SESSION:main.2" "export AGENT_ROLE=cx POD_NAME=$POD_NAME && $CX_CMD" Enter

log ""
log "Pod '$POD_NAME' started!"
log ""
log "Agents:"
log "  OC: $OC_DIR"
log "  CC: $CC_DIR"
log "  CX: $CX_DIR"
log ""

if [[ "$ATTACH" == "true" ]]; then
    exec tmux attach -t "$SESSION"
else
    log "Session: $SESSION"
    log "Attach with: tmux attach -t $SESSION"
fi
