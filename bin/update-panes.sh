#!/bin/bash
# update-panes.sh - Update panes.json for relay daemon
# Refreshes pane IDs after tmux session changes
#
# Usage: update-panes.sh [--name NAME] [--session SESSION]

set -euo pipefail

POD_NAME=""
SESSION=""
LLM_SHARE="${LLM_SHARE:-$HOME/llm-share}"

usage() {
    cat <<EOF
Usage: $(basename "$0") [OPTIONS]

Update panes.json for the relay daemon.

Refreshes pane ID mappings after tmux session reconnects or changes.

OPTIONS:
    --name NAME         Pod name (for pod sessions)
    --session SESSION   tmux session name (default: auto-detect)
    -h, --help          Show this help

OUTPUT:
    Writes to: \$LLM_SHARE/relay/state/panes.json

    Format:
    {
        "oc": "%123",
        "cc": "%124",
        "cx": "%125",
        "pod": "myproj",
        "session": "pod-myproj"
    }

EXAMPLES:
    $(basename "$0")                      # Auto-detect and update
    $(basename "$0") --name myproj        # Update for specific pod
    $(basename "$0") --session party      # Update for legacy session
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
        --session)
            SESSION="$2"
            shift 2
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

# Auto-detect session
if [[ -z "$SESSION" ]]; then
    if [[ -n "$POD_NAME" ]]; then
        SESSION="pod-$POD_NAME"
    elif [[ -f ".pod-name" ]]; then
        POD_NAME=$(cat ".pod-name")
        SESSION="pod-$POD_NAME"
    else
        # Try to find any party/pod session
        SESSION=$(tmux list-sessions -F "#{session_name}" 2>/dev/null | grep -E "^(party|pod-)" | head -1 || true)
    fi
fi

if [[ -z "$SESSION" ]]; then
    echo "Error: Could not determine session." >&2
    echo "Use --session or --name to specify." >&2
    exit 1
fi

# Check if session exists
if ! tmux has-session -t "$SESSION" 2>/dev/null; then
    echo "Error: Session '$SESSION' not running." >&2
    exit 1
fi

echo "Updating panes.json for session: $SESSION"

# Get pane IDs by index (fixed mapping: 0=OC, 1=CC, 2=CX)
OC_PANE=$(tmux display-message -p -t "$SESSION:main.0" '#{pane_id}' 2>/dev/null || true)
CC_PANE=$(tmux display-message -p -t "$SESSION:main.1" '#{pane_id}' 2>/dev/null || true)
CX_PANE=$(tmux display-message -p -t "$SESSION:main.2" '#{pane_id}' 2>/dev/null || true)

# Validate we got all panes
if [[ -z "$OC_PANE" ]] || [[ -z "$CC_PANE" ]] || [[ -z "$CX_PANE" ]]; then
    echo "Error: Could not get all pane IDs." >&2
    echo "  OC: $OC_PANE"
    echo "  CC: $CC_PANE"
    echo "  CX: $CX_PANE"
    exit 1
fi

# Also try to get from @role option as backup verification
for pane_id in "$OC_PANE" "$CC_PANE" "$CX_PANE"; do
    role=$(tmux show-options -p -t "$pane_id" @role 2>/dev/null | cut -d' ' -f2 || true)
    if [[ -n "$role" ]]; then
        echo "  Verified: $pane_id has @role=$role"
    fi
done

# Write panes.json
PANE_MAP="$LLM_SHARE/relay/state/panes.json"
mkdir -p "$(dirname "$PANE_MAP")"

# Determine pod name
if [[ -z "$POD_NAME" ]] && [[ "$SESSION" == pod-* ]]; then
    POD_NAME="${SESSION#pod-}"
fi

# Write JSON
if [[ -n "$POD_NAME" ]]; then
    printf '{"oc":"%s","cc":"%s","cx":"%s","pod":"%s","session":"%s"}\n' \
        "$OC_PANE" "$CC_PANE" "$CX_PANE" "$POD_NAME" "$SESSION" > "$PANE_MAP"
else
    printf '{"oc":"%s","cc":"%s","cx":"%s","session":"%s"}\n' \
        "$OC_PANE" "$CC_PANE" "$CX_PANE" "$SESSION" > "$PANE_MAP"
fi

echo ""
echo "Pane map updated: $PANE_MAP"
cat "$PANE_MAP"
echo ""

# Update @role options on panes (in case they were lost)
echo "Refreshing @role pane options..."
tmux set-option -p -t "$OC_PANE" @role oc 2>/dev/null || true
tmux set-option -p -t "$CC_PANE" @role cc 2>/dev/null || true
tmux set-option -p -t "$CX_PANE" @role cx 2>/dev/null || true

echo "Done!"
