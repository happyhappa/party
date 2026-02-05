#!/bin/bash
# create-worktree.sh - Create agent worktrees for party multi-agent system
# Creates {repo}-{role} worktrees for oc/cc/cx agents
#
# Usage: create-worktree.sh [--base-dir DIR] [--branch BRANCH] ROLE
#   ROLE: oc, cc, or cx
#   --base-dir: Parent directory for worktrees (default: parent of repo)
#   --branch: Branch name (default: main)

set -euo pipefail

VALID_ROLES="oc cc cx"
BASE_DIR=""
BRANCH="main"
CREATE_BRANCH=false

usage() {
    cat <<EOF
Usage: $(basename "$0") [OPTIONS] ROLE

Create a git worktree for an agent role.

ROLE:
    oc    Orchestrator agent
    cc    Coder Claude agent
    cx    Codex agent

OPTIONS:
    --base-dir DIR    Parent directory for worktrees (default: parent of repo)
    --branch BRANCH   Branch to checkout (default: main)
    --new-branch      Create a new branch named {repo}-{role}
    -h, --help        Show this help

EXAMPLES:
    $(basename "$0") cc                    # Create worktree for cc on main branch
    $(basename "$0") --new-branch oc       # Create worktree with new branch
    $(basename "$0") --base-dir ~/work cx  # Create in specific directory
EOF
    exit "${1:-0}"
}

# Parse arguments
while [[ $# -gt 0 ]]; do
    case "$1" in
        --base-dir)
            BASE_DIR="$2"
            shift 2
            ;;
        --branch)
            BRANCH="$2"
            shift 2
            ;;
        --new-branch)
            CREATE_BRANCH=true
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
            ROLE="$1"
            shift
            ;;
    esac
done

# Validate role
if [[ -z "${ROLE:-}" ]]; then
    echo "Error: ROLE is required" >&2
    usage 1
fi

if [[ ! " $VALID_ROLES " =~ " $ROLE " ]]; then
    echo "Error: Invalid role '$ROLE'. Must be one of: $VALID_ROLES" >&2
    exit 1
fi

# Get repo info
REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null) || {
    echo "Error: Not in a git repository" >&2
    exit 1
}
REPO_NAME=$(basename "$REPO_ROOT")

# Set base directory
if [[ -z "$BASE_DIR" ]]; then
    BASE_DIR=$(dirname "$REPO_ROOT")
fi

# Construct worktree path
WORKTREE_PATH="$BASE_DIR/${REPO_NAME}-${ROLE}"

# Check if worktree already exists
if [[ -d "$WORKTREE_PATH" ]]; then
    echo "Error: Worktree already exists at $WORKTREE_PATH" >&2
    exit 1
fi

# Check if worktree is already registered
if git worktree list | grep -q "$WORKTREE_PATH"; then
    echo "Error: Worktree already registered for $WORKTREE_PATH" >&2
    exit 1
fi

echo "Creating worktree for role '$ROLE'..."
echo "  Repository: $REPO_NAME"
echo "  Path: $WORKTREE_PATH"
echo "  Branch: $BRANCH"

# Create worktree
if [[ "$CREATE_BRANCH" == "true" ]]; then
    BRANCH_NAME="${REPO_NAME}-${ROLE}"
    echo "  Creating new branch: $BRANCH_NAME"
    git worktree add -b "$BRANCH_NAME" "$WORKTREE_PATH" "$BRANCH"
else
    git worktree add "$WORKTREE_PATH" "$BRANCH"
fi

# Ensure .beads/ directory exists and is tracked
BEADS_DIR="$WORKTREE_PATH/.beads"
if [[ ! -d "$BEADS_DIR" ]]; then
    mkdir -p "$BEADS_DIR"
    touch "$BEADS_DIR/.gitkeep"
    echo "  Created .beads/ directory"
fi

# Create role-specific config marker
echo "$ROLE" > "$WORKTREE_PATH/.agent-role"

# Set up environment hint
cat > "$WORKTREE_PATH/.envrc.sample" <<ENVRC
# Source this or add to your shell config
export AGENT_ROLE=$ROLE
export PARTY_WORKTREE=$WORKTREE_PATH
ENVRC

echo ""
echo "Worktree created successfully!"
echo ""
echo "To use this worktree:"
echo "  cd $WORKTREE_PATH"
echo "  export AGENT_ROLE=$ROLE"
echo ""
echo "The .beads/ directory is shared via git commits."
echo "Commit and push bead changes to share with other agents."
