#!/usr/bin/env bash
#
# admin-checkpoint-cycle.sh - Dispatch coordinated checkpoint to all agent panes
#
# Reads panes.json, generates a cycle nonce, injects /checkpoint --respond into
# OC/CC panes and /prompts:checkpoint into CX pane. Fire-and-forget.
#
# Environment:
#   RELAY_STATE_DIR - State directory (default: ~/llm-share/relay/state)
#

set -euo pipefail

STATE_DIR="${RELAY_STATE_DIR:-$HOME/llm-share/relay/state}"
LOG_FILE="$STATE_DIR/checkpoints.log"
PANES_FILE="$STATE_DIR/panes.json"

# Guard: skip if checkpoint dispatched within last 8 minutes
LAST_DISPATCH=$(grep '"type":"checkpoint-cycle"' "$LOG_FILE" 2>/dev/null | tail -1 | jq -r '.timestamp // empty' 2>/dev/null || true)
if [[ -n "$LAST_DISPATCH" ]]; then
    LAST_EPOCH=$(date -d "$LAST_DISPATCH" +%s 2>/dev/null || echo 0)
    NOW_EPOCH=$(date +%s)
    AGE=$(( NOW_EPOCH - LAST_EPOCH ))
    if [[ $AGE -lt 480 ]]; then
        echo "Checkpoint dispatched ${AGE}s ago, skipping."
        exit 0
    fi
fi

# Read pane registry
if [[ ! -f "$PANES_FILE" ]]; then
    echo "ERROR: panes.json not found at $PANES_FILE" >&2
    exit 1
fi

PANES_JSON=$(cat "$PANES_FILE")
OC_PANE=$(echo "$PANES_JSON" | jq -r '.panes.oc // empty')
CC_PANE=$(echo "$PANES_JSON" | jq -r '.panes.cc // empty')

# Generate cycle nonce
CHK_ID="chk-$(date +%s)-$(head -c 4 /dev/urandom | xxd -p)"

# Track which panes were dispatched to
DISPATCHED=()

# Dispatch to OC
if [[ -n "$OC_PANE" ]]; then
    tmux-inject "$OC_PANE" "/checkpoint --respond $CHK_ID" && DISPATCHED+=("oc") || echo "WARN: OC inject failed"
fi

# Dispatch to CC
if [[ -n "$CC_PANE" ]]; then
    tmux-inject "$CC_PANE" "/checkpoint --respond $CHK_ID" && DISPATCHED+=("cc") || echo "WARN: CC inject failed"
fi

# Dispatch to CX only if it's at an idle prompt
CX_PANE=$(echo "$PANES_JSON" | jq -r '.panes.cx // empty')
if [[ -n "$CX_PANE" ]]; then
    CX_TAIL=$(tmux capture-pane -t "$CX_PANE" -p -S -5 2>/dev/null || true)
    if echo "$CX_TAIL" | grep -q '? for shortcuts'; then
        cx-checkpoint-inject && DISPATCHED+=("cx") || echo "WARN: CX inject failed"
    else
        echo "SKIP: CX not at idle prompt, skipping checkpoint"
    fi
fi

# Log the dispatch
DISPATCHED_JSON=$(printf '%s\n' "${DISPATCHED[@]}" | jq -R . | jq -s .)
echo "{\"timestamp\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\",\"type\":\"checkpoint-cycle\",\"cycle_id\":\"$CHK_ID\",\"dispatched_to\":$DISPATCHED_JSON,\"status\":\"dispatched\"}" >> "$LOG_FILE"

echo "Checkpoint cycle $CHK_ID dispatched."
