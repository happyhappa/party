#!/bin/bash
# create-pod.sh - Create a complete pod with worktrees for all agents
# A pod is a set of 3 worktrees (oc/cc/cx) that share beads via git
#
# Usage: create-pod.sh [--name NAME] [--base-dir DIR] [--branch BRANCH]

set -euo pipefail

POD_NAME=""
BASE_DIR=""
BRANCH="main"
ROLES="oc cc cx"

usage() {
    cat <<EOF
Usage: $(basename "$0") [OPTIONS]

Create a pod with worktrees for all agent roles (oc, cc, cx).

OPTIONS:
    --name NAME       Pod name (default: repo name)
    --base-dir DIR    Parent directory for pod (default: parent of repo)
    --branch BRANCH   Branch to checkout (default: main)
    --new-branches    Create role-specific branches
    -h, --help        Show this help

A pod consists of:
    {base-dir}/{pod-name}-oc/   Orchestrator worktree
    {base-dir}/{pod-name}-cc/   Coder Claude worktree
    {base-dir}/{pod-name}-cx/   Codex worktree

Each worktree has:
    .beads/          Shared bead storage (via git)
    .agent-role      Role marker file
    .pod-name        Pod identifier

EXAMPLES:
    $(basename "$0")                      # Create pod with repo name
    $(basename "$0") --name myproject     # Create pod with custom name
    $(basename "$0") --new-branches       # Each role gets its own branch
EOF
    exit "${1:-0}"
}

NEW_BRANCHES=false

# Parse arguments
while [[ $# -gt 0 ]]; do
    case "$1" in
        --name)
            POD_NAME="$2"
            shift 2
            ;;
        --base-dir)
            BASE_DIR="$2"
            shift 2
            ;;
        --branch)
            BRANCH="$2"
            shift 2
            ;;
        --new-branches)
            NEW_BRANCHES=true
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

# Get repo info
REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null) || {
    echo "Error: Not in a git repository" >&2
    exit 1
}
REPO_NAME=$(basename "$REPO_ROOT")

# Set defaults
if [[ -z "$POD_NAME" ]]; then
    POD_NAME="$REPO_NAME"
fi

if [[ -z "$BASE_DIR" ]]; then
    BASE_DIR=$(dirname "$REPO_ROOT")
fi

# LLM share directory for state
LLM_SHARE="${LLM_SHARE:-$HOME/llm-share}"
POD_STATE_DIR="$LLM_SHARE/pods/$POD_NAME"

echo "Creating pod '$POD_NAME'..."
echo "  Repository: $REPO_NAME"
echo "  Base directory: $BASE_DIR"
echo "  Branch: $BRANCH"
echo ""

# Check if any worktrees already exist
for role in $ROLES; do
    WORKTREE_PATH="$BASE_DIR/${POD_NAME}-${role}"
    if [[ -d "$WORKTREE_PATH" ]]; then
        echo "Error: Worktree already exists at $WORKTREE_PATH" >&2
        echo "Use remove-pod.sh to clean up first, or choose a different name." >&2
        exit 1
    fi
done

# Create worktrees for each role
for role in $ROLES; do
    WORKTREE_PATH="$BASE_DIR/${POD_NAME}-${role}"
    echo "Creating worktree for $role..."

    if [[ "$NEW_BRANCHES" == "true" ]]; then
        BRANCH_NAME="${POD_NAME}-${role}"
        git worktree add -b "$BRANCH_NAME" "$WORKTREE_PATH" "$BRANCH"
    else
        git worktree add "$WORKTREE_PATH" "$BRANCH"
    fi

    # Initialize .beads/ directory
    mkdir -p "$WORKTREE_PATH/.beads"
    touch "$WORKTREE_PATH/.beads/.gitkeep"

    # Create role marker
    echo "$role" > "$WORKTREE_PATH/.agent-role"

    # Create pod marker
    echo "$POD_NAME" > "$WORKTREE_PATH/.pod-name"

    # Create environment sample
    cat > "$WORKTREE_PATH/.envrc.sample" <<ENVRC
# Source this or add to your shell config
export AGENT_ROLE=$role
export POD_NAME=$POD_NAME
export PARTY_WORKTREE=$WORKTREE_PATH
ENVRC

    echo "  Created: $WORKTREE_PATH"
done

# Initialize pod state directory
echo ""
echo "Initializing pod state..."
mkdir -p "$POD_STATE_DIR"

# Create pod manifest
cat > "$POD_STATE_DIR/manifest.json" <<MANIFEST
{
    "pod_name": "$POD_NAME",
    "repo_root": "$REPO_ROOT",
    "base_dir": "$BASE_DIR",
    "branch": "$BRANCH",
    "created_at": "$(date -u +"%Y-%m-%dT%H:%M:%SZ")",
    "worktrees": {
        "oc": "$BASE_DIR/${POD_NAME}-oc",
        "cc": "$BASE_DIR/${POD_NAME}-cc",
        "cx": "$BASE_DIR/${POD_NAME}-cx"
    }
}
MANIFEST

echo "  Pod manifest: $POD_STATE_DIR/manifest.json"

# Initialize shared beads in main repo (will be synced via git)
if [[ ! -d "$REPO_ROOT/.beads" ]]; then
    mkdir -p "$REPO_ROOT/.beads"
    touch "$REPO_ROOT/.beads/.gitkeep"
    echo "  Initialized .beads/ in main repo"
fi

echo ""
echo "Pod '$POD_NAME' created successfully!"
echo ""
echo "Worktrees:"
for role in $ROLES; do
    echo "  $role: $BASE_DIR/${POD_NAME}-${role}"
done
echo ""
echo "Next steps:"
echo "  1. Start the pod:  start-pod.sh --name $POD_NAME"
echo "  2. Or cd into a worktree and run agents manually"
echo ""
echo "Beads are shared via git commits. Use sync-beads.sh to synchronize."
