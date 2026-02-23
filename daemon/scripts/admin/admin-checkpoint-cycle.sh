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

# Idle detection with grace period (mirrors original Go idle.go logic)
LAST_DISPATCH_FILE="$STATE_DIR/last-checkpoint-dispatch"
GRACE_PERIOD=120   # 2 minutes — ignore JSONL writes caused by checkpoint response
BACKSTOP_INTERVAL=7200  # 2 hours — force checkpoint even when idle

is_agent_idle() {
    local role="$1"
    local project_dir
    project_dir=$(jq -r ".${role} // empty" "$STATE_DIR/project-dirs.json" 2>/dev/null)
    [[ -z "$project_dir" ]] && return 1  # can't determine, assume active

    local latest_jsonl
    latest_jsonl=$(ls -t "$project_dir"/*.jsonl 2>/dev/null | head -1)
    [[ -z "$latest_jsonl" ]] && return 1

    local mtime now last_dispatch cutoff
    mtime=$(stat -c %Y "$latest_jsonl" 2>/dev/null || echo 0)
    now=$(date +%s)
    last_dispatch=$(cat "$LAST_DISPATCH_FILE" 2>/dev/null || echo 0)
    cutoff=$(( last_dispatch + GRACE_PERIOD ))

    # Activity within grace period of last dispatch = checkpoint response, still idle
    if (( mtime <= cutoff )); then
        return 0  # idle
    fi

    # Activity after grace period = genuinely active
    return 1  # active
}

should_backstop() {
    local last_dispatch now age
    last_dispatch=$(cat "$LAST_DISPATCH_FILE" 2>/dev/null || echo 0)
    now=$(date +%s)
    age=$(( now - last_dispatch ))
    (( age > BACKSTOP_INTERVAL ))
}

# Generate cycle nonce
CHK_ID="chk-$(date +%s)-$(head -c 4 /dev/urandom | xxd -p)"

# Track which panes were dispatched to
DISPATCHED=()

# Backstop: force checkpoint if >2h since last dispatch
FORCE_DISPATCH=false
if should_backstop; then
    FORCE_DISPATCH=true
    echo "BACKSTOP: >2h since last dispatch, forcing checkpoint"
fi

# Dispatch to OC
if [[ -n "$OC_PANE" ]]; then
    if [[ "$FORCE_DISPATCH" != "true" ]] && is_agent_idle "oc"; then
        echo "SKIP: OC idle, skipping checkpoint"
    else
        tmux-inject "$OC_PANE" "/checkpoint --respond $CHK_ID" && DISPATCHED+=("oc") || echo "WARN: OC inject failed"
    fi
fi

# Dispatch to CC
if [[ -n "$CC_PANE" ]]; then
    if [[ "$FORCE_DISPATCH" != "true" ]] && is_agent_idle "cc"; then
        echo "SKIP: CC idle, skipping checkpoint"
    else
        tmux-inject "$CC_PANE" "/checkpoint --respond $CHK_ID" && DISPATCHED+=("cc") || echo "WARN: CC inject failed"
    fi
fi

# Dispatch to CX only if it's at an idle prompt
CX_PANE=$(echo "$PANES_JSON" | jq -r '.panes.cx // empty')
if [[ -n "$CX_PANE" ]]; then
    CX_TAIL=$(tmux capture-pane -t "$CX_PANE" -p -S -5 2>/dev/null || true)
    if echo "$CX_TAIL" | grep -qE '(context left|\? for shortcuts)'; then
        cx-checkpoint-inject "$CHK_ID" && DISPATCHED+=("cx") || echo "WARN: CX inject failed"
    else
        echo "SKIP: CX not at idle prompt, skipping checkpoint"
    fi
fi

# Log the dispatch
DISPATCHED_JSON=$(printf '%s\n' "${DISPATCHED[@]}" | jq -R . | jq -s .)
echo "{\"timestamp\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\",\"type\":\"checkpoint-cycle\",\"cycle_id\":\"$CHK_ID\",\"dispatched_to\":$DISPATCHED_JSON,\"status\":\"dispatched\"}" >> "$LOG_FILE"

# Record dispatch time for grace period calculation
date +%s > "$LAST_DISPATCH_FILE"

echo "Checkpoint cycle $CHK_ID dispatched."
