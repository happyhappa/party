#!/usr/bin/env bash
#
# admin-checkpoint-cycle.sh - Dispatch coordinated checkpoint to all agent panes
#
# Reads panes.json, generates a cycle nonce, injects /checkpoint --respond into
# OC/CC panes and /prompts:checkpoint into CX pane. Fire-and-forget.
#
# Environment:
#   RELAY_STATE_DIR - State directory (required)
#

set -euo pipefail

STATE_DIR="${RELAY_STATE_DIR:?RELAY_STATE_DIR not set — must be exported by bin/party}"
LOG_FILE="$STATE_DIR/checkpoints.log"
PANES_FILE="$STATE_DIR/panes.json"
CHK_ID_FILE="$STATE_DIR/cx-chk-id"

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

# Persist checkpoint ID for CX handoff before injection.
printf '%s\n' "$CHK_ID" > "$CHK_ID_FILE"

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

# Dispatch to CX (uses dedicated script, no chk_id arg)
cx-checkpoint-inject && DISPATCHED+=("cx") || echo "WARN: CX inject failed"

# Log the dispatch
DISPATCHED_JSON=$(printf '%s\n' "${DISPATCHED[@]}" | jq -R . | jq -s .)
echo "{\"timestamp\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\",\"type\":\"checkpoint-cycle\",\"cycle_id\":\"$CHK_ID\",\"dispatched_to\":$DISPATCHED_JSON,\"status\":\"dispatched\"}" >> "$LOG_FILE"

echo "Checkpoint cycle $CHK_ID dispatched."
