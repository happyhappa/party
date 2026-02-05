#!/bin/bash
# remove-worktree.sh - Remove agent worktrees safely
# Checks for uncommitted changes and locked worktrees before removal
#
# Usage: remove-worktree.sh [--force] [--prune] ROLE|PATH

set -euo pipefail

VALID_ROLES="oc cc cx"
FORCE=false
PRUNE=false

usage() {
    cat <<EOF
Usage: $(basename "$0") [OPTIONS] ROLE|PATH

Remove a git worktree for an agent role.

ARGUMENTS:
    ROLE    Agent role (oc, cc, cx) - resolves to {repo}-{role}
    PATH    Direct path to worktree

OPTIONS:
    --force     Force removal even with uncommitted changes
    --prune     Run git worktree prune after removal
    -h, --help  Show this help

SAFETY CHECKS:
    - Warns if worktree has uncommitted changes
    - Warns if worktree is locked
    - Requires --force to override warnings

EXAMPLES:
    $(basename "$0") cc                  # Remove cc worktree
    $(basename "$0") --prune cx          # Remove and prune
    $(basename "$0") --force oc          # Force remove with changes
    $(basename "$0") /path/to/worktree   # Remove by path
EOF
    exit "${1:-0}"
}

# Parse arguments
while [[ $# -gt 0 ]]; do
    case "$1" in
        --force)
            FORCE=true
            shift
            ;;
        --prune)
            PRUNE=true
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
            TARGET="$1"
            shift
            ;;
    esac
done

# Validate target
if [[ -z "${TARGET:-}" ]]; then
    echo "Error: ROLE or PATH is required" >&2
    usage 1
fi

# Get repo info
REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null) || {
    echo "Error: Not in a git repository" >&2
    exit 1
}
REPO_NAME=$(basename "$REPO_ROOT")
BASE_DIR=$(dirname "$REPO_ROOT")

# Resolve target to worktree path
if [[ " $VALID_ROLES " =~ " $TARGET " ]]; then
    WORKTREE_PATH="$BASE_DIR/${REPO_NAME}-${TARGET}"
    ROLE="$TARGET"
else
    WORKTREE_PATH="$TARGET"
    ROLE=""
fi

# Check if worktree exists
if [[ ! -d "$WORKTREE_PATH" ]]; then
    echo "Error: Worktree does not exist at $WORKTREE_PATH" >&2
    exit 1
fi

# Check if it's a valid worktree
if ! git worktree list | grep -q "$WORKTREE_PATH"; then
    echo "Error: $WORKTREE_PATH is not a registered worktree" >&2
    exit 1
fi

echo "Checking worktree: $WORKTREE_PATH"

# Check for uncommitted changes
UNCOMMITTED=false
if [[ -d "$WORKTREE_PATH/.git" ]] || [[ -f "$WORKTREE_PATH/.git" ]]; then
    pushd "$WORKTREE_PATH" > /dev/null
    if ! git diff --quiet 2>/dev/null || ! git diff --cached --quiet 2>/dev/null; then
        UNCOMMITTED=true
        echo "WARNING: Worktree has uncommitted changes!"
        git status --short
    fi

    # Check for untracked files in .beads/
    if [[ -d ".beads" ]]; then
        UNTRACKED=$(git ls-files --others --exclude-standard .beads/ 2>/dev/null | wc -l)
        if [[ "$UNTRACKED" -gt 0 ]]; then
            echo "WARNING: .beads/ has $UNTRACKED untracked files!"
            git ls-files --others --exclude-standard .beads/
            UNCOMMITTED=true
        fi
    fi
    popd > /dev/null
fi

# Check if worktree is locked
# Git uses path-encoded names for worktrees outside standard locations,
# so we use 'git worktree list --porcelain' to find the actual internal name
LOCKED=false
WORKTREE_INTERNAL_NAME=""

# Parse porcelain output to find the worktree entry
while IFS= read -r line; do
    case "$line" in
        "worktree "*)
            current_path="${line#worktree }"
            ;;
        "locked"*)
            if [[ "$current_path" == "$WORKTREE_PATH" ]]; then
                LOCKED=true
                LOCK_REASON="${line#locked }"
                [[ -z "$LOCK_REASON" ]] && LOCK_REASON="(no reason given)"
            fi
            ;;
    esac
done < <(git worktree list --porcelain 2>/dev/null)

if [[ "$LOCKED" == "true" ]]; then
    echo "WARNING: Worktree is locked: $LOCK_REASON"
fi

# Handle warnings
if [[ "$UNCOMMITTED" == "true" ]] || [[ "$LOCKED" == "true" ]]; then
    if [[ "$FORCE" != "true" ]]; then
        echo ""
        echo "Aborting due to warnings. Use --force to override."
        exit 1
    fi
    echo ""
    echo "Proceeding with --force..."
fi

# Remove the worktree
echo ""
echo "Removing worktree..."

if [[ "$FORCE" == "true" ]]; then
    git worktree remove --force "$WORKTREE_PATH"
else
    git worktree remove "$WORKTREE_PATH"
fi

echo "Worktree removed: $WORKTREE_PATH"

# Prune if requested
if [[ "$PRUNE" == "true" ]]; then
    echo "Pruning stale worktree entries..."
    git worktree prune -v
fi

echo ""
echo "Done!"

# List remaining worktrees
echo ""
echo "Remaining worktrees:"
git worktree list
