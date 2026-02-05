#!/bin/bash
# remove-pod.sh - Remove a pod and all its worktrees
# Safely cleans up worktrees and pod state
#
# Usage: remove-pod.sh [--name NAME] [--force]

set -euo pipefail

POD_NAME=""
FORCE=false
LLM_SHARE="${LLM_SHARE:-$HOME/llm-share}"

usage() {
    cat <<EOF
Usage: $(basename "$0") [OPTIONS]

Remove a pod and all its worktrees.

OPTIONS:
    --name NAME     Pod name (required or auto-detect)
    --force         Force removal without checks
    -h, --help      Show this help

SAFETY CHECKS:
    - Stops running tmux session first
    - Warns about uncommitted changes
    - Requires --force to override

EXAMPLES:
    $(basename "$0") --name myproj           # Remove specific pod
    $(basename "$0") --name myproj --force   # Force remove
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
        --force)
            FORCE=true
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
    fi
fi

if [[ -z "$POD_NAME" ]]; then
    echo "Error: Pod name required. Use --name to specify." >&2
    exit 1
fi

POD_STATE_DIR="$LLM_SHARE/pods/$POD_NAME"
MANIFEST="$POD_STATE_DIR/manifest.json"
SESSION="pod-$POD_NAME"

# Colors
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
RED='\033[0;31m'
NC='\033[0m'

log() {
    echo -e "${GREEN}[remove-pod]${NC} $1"
}

warn() {
    echo -e "${YELLOW}[remove-pod]${NC} $1"
}

err() {
    echo -e "${RED}[remove-pod]${NC} $1" >&2
}

# Check if pod exists
if [[ ! -f "$MANIFEST" ]]; then
    err "Pod '$POD_NAME' not found."
    exit 1
fi

log "Removing pod '$POD_NAME'..."

# Stop running session first
if tmux has-session -t "$SESSION" 2>/dev/null; then
    log "Stopping running session..."
    stop-pod.sh --name "$POD_NAME" --force 2>/dev/null || true
fi

# Get worktree paths
OC_DIR=$(grep -o '"oc": *"[^"]*"' "$MANIFEST" | cut -d'"' -f4)
CC_DIR=$(grep -o '"cc": *"[^"]*"' "$MANIFEST" | cut -d'"' -f4)
CX_DIR=$(grep -o '"cx": *"[^"]*"' "$MANIFEST" | cut -d'"' -f4)

# Check for uncommitted changes
HAS_CHANGES=false
for dir in "$OC_DIR" "$CC_DIR" "$CX_DIR"; do
    if [[ -d "$dir" ]]; then
        if ! git -C "$dir" diff --quiet 2>/dev/null || \
           ! git -C "$dir" diff --cached --quiet 2>/dev/null; then
            warn "Uncommitted changes in: $dir"
            HAS_CHANGES=true
        fi
    fi
done

if [[ "$HAS_CHANGES" == "true" ]] && [[ "$FORCE" != "true" ]]; then
    err "Aborting due to uncommitted changes. Use --force to override."
    exit 1
fi

# Get repo root for worktree removal
REPO_ROOT=$(grep -o '"repo_root": *"[^"]*"' "$MANIFEST" | cut -d'"' -f4)

# Remove worktrees
log "Removing worktrees..."
REMOVAL_FAILED=false

for dir in "$OC_DIR" "$CC_DIR" "$CX_DIR"; do
    if [[ -d "$dir" ]]; then
        log "  Removing: $dir"
        if [[ "$FORCE" == "true" ]]; then
            if ! git -C "$REPO_ROOT" worktree remove --force "$dir" 2>&1; then
                warn "  git worktree remove failed, trying rm -rf..."
                if ! rm -rf "$dir" 2>&1; then
                    err "  Failed to remove directory: $dir"
                    REMOVAL_FAILED=true
                fi
            fi
        else
            if ! git -C "$REPO_ROOT" worktree remove "$dir" 2>&1; then
                err "  Failed to remove worktree: $dir"
                err "  Use --force to force removal"
                REMOVAL_FAILED=true
            fi
        fi
    fi
done

# Prune worktrees
log "Pruning worktree references..."
git -C "$REPO_ROOT" worktree prune -v 2>&1 || warn "Prune had warnings"

# Verify worktrees are actually removed
log "Verifying removal..."
REMAINING=false
for dir in "$OC_DIR" "$CC_DIR" "$CX_DIR"; do
    if [[ -d "$dir" ]]; then
        err "  Worktree still exists: $dir"
        REMAINING=true
    fi
done

if [[ "$REMAINING" == "true" ]]; then
    err "Some worktrees were not removed!"
    exit 1
fi

if [[ "$REMOVAL_FAILED" == "true" ]]; then
    err "Removal had errors but directories are gone."
fi

# Remove pod state
log "Removing pod state..."
rm -rf "$POD_STATE_DIR"

# Clear panes.json if it points to this pod
PANE_MAP="$LLM_SHARE/relay/state/panes.json"
if [[ -f "$PANE_MAP" ]]; then
    CURRENT_POD=$(grep -o '"pod":"[^"]*"' "$PANE_MAP" 2>/dev/null | cut -d'"' -f4 || true)
    if [[ "$CURRENT_POD" == "$POD_NAME" ]]; then
        rm -f "$PANE_MAP"
    fi
fi

log ""
log "Pod '$POD_NAME' removed successfully!"

# Show remaining worktrees
log ""
log "Remaining worktrees:"
git -C "$REPO_ROOT" worktree list 2>/dev/null || true
