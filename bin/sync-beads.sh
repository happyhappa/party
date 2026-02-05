#!/bin/bash
# sync-beads.sh - Synchronize .beads/ directory across worktrees via git
# Single-writer enforcement: only one agent should write at a time
#
# Usage: sync-beads.sh [push|pull|status]

set -euo pipefail

BEADS_DIR=".beads"

usage() {
    cat <<EOF
Usage: $(basename "$0") COMMAND

Synchronize .beads/ directory across agent worktrees via git.

COMMANDS:
    push      Commit and push local bead changes
    pull      Pull bead changes from remote
    status    Show bead sync status
    init      Initialize .beads/ directory if missing

WORKFLOW:
    1. Agent creates/updates beads locally
    2. Agent runs 'sync-beads.sh push' to share
    3. Other agents run 'sync-beads.sh pull' to receive

SINGLE-WRITER:
    Only one agent should push at a time to avoid conflicts.
    The relay daemon coordinates this via checkpoint ACKs.

EXAMPLES:
    $(basename "$0") status   # Check for local changes
    $(basename "$0") push     # Share your beads
    $(basename "$0") pull     # Get others' beads
EOF
    exit "${1:-0}"
}

# Check we're in a git repo
check_repo() {
    if ! git rev-parse --show-toplevel &>/dev/null; then
        echo "Error: Not in a git repository" >&2
        exit 1
    fi
}

# Get current role from .agent-role file or AGENT_ROLE env
get_role() {
    if [[ -f ".agent-role" ]]; then
        cat ".agent-role"
    elif [[ -n "${AGENT_ROLE:-}" ]]; then
        echo "$AGENT_ROLE"
    else
        echo "unknown"
    fi
}

cmd_init() {
    check_repo

    if [[ ! -d "$BEADS_DIR" ]]; then
        mkdir -p "$BEADS_DIR"
        touch "$BEADS_DIR/.gitkeep"
        git add "$BEADS_DIR/.gitkeep"
        echo "Initialized $BEADS_DIR/"
    else
        echo "$BEADS_DIR/ already exists"
    fi

    # Ensure .gitkeep exists
    if [[ ! -f "$BEADS_DIR/.gitkeep" ]]; then
        touch "$BEADS_DIR/.gitkeep"
        git add "$BEADS_DIR/.gitkeep"
    fi
}

cmd_status() {
    check_repo

    echo "=== Bead Sync Status ==="
    echo "Role: $(get_role)"
    echo "Directory: $BEADS_DIR/"
    echo ""

    if [[ ! -d "$BEADS_DIR" ]]; then
        echo "WARNING: $BEADS_DIR/ does not exist. Run 'sync-beads.sh init'"
        exit 0
    fi

    # Count beads
    BEAD_COUNT=$(find "$BEADS_DIR" -name "*.json" -o -name "*.md" 2>/dev/null | wc -l)
    echo "Local beads: $BEAD_COUNT"

    # Check for uncommitted changes
    echo ""
    echo "=== Local Changes ==="
    if git diff --quiet "$BEADS_DIR" 2>/dev/null && git diff --cached --quiet "$BEADS_DIR" 2>/dev/null; then
        UNTRACKED=$(git ls-files --others --exclude-standard "$BEADS_DIR" 2>/dev/null | wc -l)
        if [[ "$UNTRACKED" -eq 0 ]]; then
            echo "No local changes"
        else
            echo "Untracked files: $UNTRACKED"
            git ls-files --others --exclude-standard "$BEADS_DIR"
        fi
    else
        echo "Modified files:"
        git diff --name-only "$BEADS_DIR" 2>/dev/null
        git diff --cached --name-only "$BEADS_DIR" 2>/dev/null
    fi

    # Check remote status
    echo ""
    echo "=== Remote Status ==="
    git fetch origin --quiet 2>/dev/null || true

    LOCAL=$(git rev-parse HEAD 2>/dev/null)
    REMOTE=$(git rev-parse @{u} 2>/dev/null || echo "")

    if [[ -z "$REMOTE" ]]; then
        echo "No upstream branch configured"
    elif [[ "$LOCAL" == "$REMOTE" ]]; then
        echo "Up to date with remote"
    else
        BEHIND=$(git rev-list --count HEAD..@{u} 2>/dev/null || echo "0")
        AHEAD=$(git rev-list --count @{u}..HEAD 2>/dev/null || echo "0")
        echo "Behind: $BEHIND commits, Ahead: $AHEAD commits"
    fi
}

cmd_push() {
    check_repo
    local ROLE=$(get_role)

    if [[ ! -d "$BEADS_DIR" ]]; then
        echo "Error: $BEADS_DIR/ does not exist. Run 'sync-beads.sh init'" >&2
        exit 1
    fi

    # Check for changes
    git add "$BEADS_DIR/"

    if git diff --cached --quiet "$BEADS_DIR"; then
        echo "No bead changes to push"
        return 0
    fi

    # Commit
    TIMESTAMP=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
    git commit -m "beads: sync from $ROLE at $TIMESTAMP" -- "$BEADS_DIR/"

    echo "Committed bead changes"

    # Push
    if git push 2>/dev/null; then
        echo "Pushed to remote"
    else
        echo "WARNING: Push failed. You may need to pull first."
        exit 1
    fi
}

cmd_pull() {
    check_repo

    # Stash local bead changes if any
    STASHED=false
    if ! git diff --quiet "$BEADS_DIR" 2>/dev/null; then
        echo "Stashing local bead changes..."
        git stash push -m "sync-beads auto-stash" -- "$BEADS_DIR/"
        STASHED=true
    fi

    # Pull
    if git pull --rebase 2>/dev/null; then
        echo "Pulled latest changes"
    else
        echo "WARNING: Pull failed"
        if [[ "$STASHED" == "true" ]]; then
            git stash pop
        fi
        exit 1
    fi

    # Restore stashed changes
    if [[ "$STASHED" == "true" ]]; then
        echo "Restoring local changes..."
        if ! git stash pop; then
            echo "WARNING: Merge conflict in beads. Manual resolution required."
            exit 1
        fi
    fi

    echo "Bead sync complete"
}

# Main
case "${1:-}" in
    init)
        cmd_init
        ;;
    status)
        cmd_status
        ;;
    push)
        cmd_push
        ;;
    pull)
        cmd_pull
        ;;
    -h|--help|help)
        usage 0
        ;;
    "")
        usage 1
        ;;
    *)
        echo "Error: Unknown command '$1'" >&2
        usage 1
        ;;
esac
