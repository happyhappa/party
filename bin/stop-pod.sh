#!/bin/bash
# stop-pod.sh - Stop a pod's tmux session and save state
# Gracefully stops agents and updates pod state
#
# Usage: stop-pod.sh [--name NAME] [--force]

set -euo pipefail

POD_NAME=""
FORCE=false
LLM_SHARE="${LLM_SHARE:-$HOME/llm-share}"
RELAY_SHARE_DIR="${RELAY_SHARE_DIR:-$LLM_SHARE}"
RELAY_STATE_DIR="${RELAY_STATE_DIR:-$RELAY_SHARE_DIR/relay/state}"

usage() {
    cat <<EOF
Usage: $(basename "$0") [OPTIONS]

Stop a pod's tmux session and save state.

OPTIONS:
    --name NAME     Pod name (default: auto-detect)
    --force         Kill session without graceful shutdown
    --save-beads    Commit and push beads before stopping
    -h, --help      Show this help

SHUTDOWN PROCESS:
    1. Sends Ctrl-C to all panes (graceful)
    2. Waits for agents to exit
    3. Updates pod state to 'stopped'
    4. Optionally syncs beads
    5. Kills tmux session

EXAMPLES:
    $(basename "$0")                  # Stop current pod
    $(basename "$0") --name myproj    # Stop specific pod
    $(basename "$0") --force          # Force kill
EOF
    exit "${1:-0}"
}

SAVE_BEADS=false

# Parse arguments
while [[ $# -gt 0 ]]; do
    case "$1" in
        --name)
            POD_NAME="$2"
            shift 2
            ;;
        --force)
            FORCE=true
            shift
            ;;
        --save-beads)
            SAVE_BEADS=true
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

# Auto-detect pod name
if [[ -z "$POD_NAME" ]]; then
    if [[ -f ".pod-name" ]]; then
        POD_NAME=$(cat ".pod-name")
    else
        # Try to find from panes.json
        PANE_MAP="$RELAY_STATE_DIR/panes.json"
        if [[ -f "$PANE_MAP" ]]; then
            POD_NAME=$(grep -o '"pod":"[^"]*"' "$PANE_MAP" 2>/dev/null | cut -d'"' -f4 || true)
        fi
    fi
fi

if [[ -z "$POD_NAME" ]]; then
    echo "Error: Could not determine pod name." >&2
    echo "Use --name to specify." >&2
    exit 1
fi

SESSION="pod-$POD_NAME"
POD_STATE_DIR="$LLM_SHARE/pods/$POD_NAME"

# Colors
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
RED='\033[0;31m'
NC='\033[0m'

log() {
    echo -e "${GREEN}[stop-pod]${NC} $1"
}

warn() {
    echo -e "${YELLOW}[stop-pod]${NC} $1"
}

err() {
    echo -e "${RED}[stop-pod]${NC} $1" >&2
}

# Check if session exists
if ! tmux has-session -t "$SESSION" 2>/dev/null; then
    warn "Session '$SESSION' not running."

    # Update state anyway
    if [[ -d "$POD_STATE_DIR" ]]; then
        cat > "$POD_STATE_DIR/state.json" <<STATE
{
    "status": "stopped",
    "stopped_at": "$(date -u +"%Y-%m-%dT%H:%M:%SZ")",
    "reason": "session_not_found"
}
STATE
    fi
    exit 0
fi

log "Stopping pod '$POD_NAME'..."

# Save beads if requested
if [[ "$SAVE_BEADS" == "true" ]]; then
    log "Syncing beads..."
    MANIFEST="$POD_STATE_DIR/manifest.json"
    if [[ -f "$MANIFEST" ]]; then
        for role in oc cc cx; do
            DIR=$(grep -o "\"$role\": *\"[^\"]*\"" "$MANIFEST" | cut -d'"' -f4)
            if [[ -d "$DIR/.beads" ]]; then
                pushd "$DIR" > /dev/null
                if [[ -x "$(command -v sync-beads.sh)" ]]; then
                    sync-beads.sh push 2>/dev/null || warn "  Failed to sync $role beads"
                fi
                popd > /dev/null
            fi
        done
    fi
fi

if [[ "$FORCE" != "true" ]]; then
    log "Sending graceful shutdown to agents..."

    # Send Ctrl-C to all panes
    for pane in 0 1 2; do
        tmux send-keys -t "$SESSION:main.$pane" C-c 2>/dev/null || true
    done

    # Wait a moment for graceful shutdown
    log "Waiting for agents to exit..."
    sleep 2

    # Send exit command as backup
    for pane in 0 1 2; do
        tmux send-keys -t "$SESSION:main.$pane" "exit" Enter 2>/dev/null || true
    done

    sleep 1
fi

# Kill the session
log "Killing tmux session..."
tmux kill-session -t "$SESSION" 2>/dev/null || true

# Update pod state
if [[ -d "$POD_STATE_DIR" ]]; then
    cat > "$POD_STATE_DIR/state.json" <<STATE
{
    "status": "stopped",
    "stopped_at": "$(date -u +"%Y-%m-%dT%H:%M:%SZ")",
    "reason": "$(if [[ "$FORCE" == "true" ]]; then echo "force_killed"; else echo "graceful_shutdown"; fi)"
}
STATE
    log "Pod state updated"
fi

# Clear panes.json if it points to this pod
PANE_MAP="$RELAY_STATE_DIR/panes.json"
if [[ -f "$PANE_MAP" ]]; then
    CURRENT_POD=$(grep -o '"pod":"[^"]*"' "$PANE_MAP" 2>/dev/null | cut -d'"' -f4 || true)
    if [[ "$CURRENT_POD" == "$POD_NAME" ]]; then
        rm -f "$PANE_MAP"
        log "Cleared pane map"
    fi
fi

log ""
log "Pod '$POD_NAME' stopped."
