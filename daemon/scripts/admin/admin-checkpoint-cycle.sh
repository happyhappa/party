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
PROJECT_DIRS_FILE="$STATE_DIR/project-dirs.json"
LAST_DISPATCH_FILE="$STATE_DIR/last-checkpoint-dispatch"
INBOX_DIR="${RELAY_INBOX_DIR:-$HOME/.local/share/relay/outbox}"
GRACE_PERIOD=300
BACKSTOP_INTERVAL="${RELAY_IDLE_BACKSTOP_INTERVAL:-7200}"

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

last_dispatch_epoch() {
    cat "$LAST_DISPATCH_FILE" 2>/dev/null || echo 0
}

project_dir_for_role() {
    local role="$1"
    jq -r ".${role} // empty" "$PROJECT_DIRS_FILE" 2>/dev/null
}

has_jsonl_activity() {
    local role="$1" cutoff="$2"
    local project_dir latest_jsonl mtime

    project_dir=$(project_dir_for_role "$role")
    [[ -z "$project_dir" ]] && return 1

    latest_jsonl=$(ls -t "$project_dir"/*.jsonl 2>/dev/null | head -1)
    [[ -z "$latest_jsonl" ]] && return 1

    mtime=$(stat -c %Y "$latest_jsonl" 2>/dev/null || echo 0)
    (( mtime > cutoff ))
}

has_source_activity() {
    local role="$1" cutoff="$2"
    local project_dir recent

    project_dir=$(project_dir_for_role "$role")
    [[ -z "$project_dir" ]] && return 1

    recent=$(find "$project_dir" -maxdepth 3 -type f \( \
        -name '*.ts' -o -name '*.tsx' -o -name '*.js' -o -name '*.jsx' -o \
        -name '*.go' -o -name '*.py' -o -name '*.rs' -o -name '*.java' -o \
        -name '*.c' -o -name '*.cc' -o -name '*.cpp' -o -name '*.h' -o \
        -name '*.hpp' -o -name '*.rb' -o -name '*.php' -o -name '*.swift' -o \
        -name '*.kt' -o -name '*.m' -o -name '*.mm' -o -name '*.cs' \
    \) -printf '%T@\n' 2>/dev/null | awk -v cutoff="$cutoff" '$1 > cutoff {print; exit}')

    [[ -n "$recent" ]]
}

has_outbox_activity() {
    local role="$1" cutoff="$2"
    local outbox_dir mtime

    outbox_dir="$INBOX_DIR/$role"
    [[ -d "$outbox_dir" ]] || return 1

    mtime=$(stat -c %Y "$outbox_dir" 2>/dev/null || echo 0)
    (( mtime > cutoff ))
}

is_oc_idle() {
    local cutoff="$1"
    if has_jsonl_activity "oc" "$cutoff"; then
        return 1
    fi
    return 0
}

is_source_or_outbox_idle() {
    local role="$1" cutoff="$2"
    local has_signal=false
    local project_dir outbox_dir

    project_dir=$(project_dir_for_role "$role")
    if [[ -n "$project_dir" ]]; then
        has_signal=true
    fi

    outbox_dir="$INBOX_DIR/$role"
    if [[ -d "$outbox_dir" ]]; then
        has_signal=true
    fi

    if [[ "$has_signal" != "true" ]]; then
        return 1
    fi

    if has_source_activity "$role" "$cutoff" || has_outbox_activity "$role" "$cutoff"; then
        return 1
    fi
    return 0
}

should_backstop() {
    local now age last_dispatch
    now=$(date +%s)
    last_dispatch=$(last_dispatch_epoch)
    age=$(( now - last_dispatch ))
    (( age > BACKSTOP_INTERVAL ))
}

# Generate cycle nonce
CHK_ID="chk-$(date +%s)-$(head -c 4 /dev/urandom | xxd -p)"

# Persist checkpoint ID for CX handoff before injection.
printf '%s\n' "$CHK_ID" > "$CHK_ID_FILE"

# Track which panes were dispatched to
DISPATCHED=()

# Idle/backstop gating
LAST_DISPATCH_EPOCH=$(last_dispatch_epoch)
CUTOFF_EPOCH=$(( LAST_DISPATCH_EPOCH + GRACE_PERIOD ))

FORCE_DISPATCH=false
if should_backstop; then
    FORCE_DISPATCH=true
    echo "BACKSTOP: >${BACKSTOP_INTERVAL}s since last dispatch, forcing checkpoint"
fi

# Dispatch to OC
if [[ -n "$OC_PANE" ]]; then
    if [[ "$FORCE_DISPATCH" != "true" ]] && is_oc_idle "$CUTOFF_EPOCH"; then
        echo "SKIP: OC idle, skipping checkpoint"
    else
        tmux-inject "$OC_PANE" "/checkpoint --respond $CHK_ID" && DISPATCHED+=("oc") || echo "WARN: OC inject failed"
    fi
fi

# Dispatch to CC
if [[ -n "$CC_PANE" ]]; then
    if [[ "$FORCE_DISPATCH" != "true" ]] && is_source_or_outbox_idle "cc" "$CUTOFF_EPOCH"; then
        echo "SKIP: CC idle, skipping checkpoint"
    else
        tmux-inject "$CC_PANE" "/checkpoint --respond $CHK_ID" && DISPATCHED+=("cc") || echo "WARN: CC inject failed"
    fi
fi

# Dispatch to CX (uses dedicated script, no chk_id arg)
CX_PANE=$(echo "$PANES_JSON" | jq -r '.panes.cx // empty')
if [[ -n "$CX_PANE" ]]; then
    if [[ "$FORCE_DISPATCH" != "true" ]] && is_source_or_outbox_idle "cx" "$CUTOFF_EPOCH"; then
        echo "SKIP: CX idle, skipping checkpoint"
    else
        cx-checkpoint-inject && DISPATCHED+=("cx") || echo "WARN: CX inject failed"
    fi
fi

# Log the dispatch
DISPATCHED_JSON=$(printf '%s\n' "${DISPATCHED[@]}" | jq -R . | jq -s .)
echo "{\"timestamp\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\",\"type\":\"checkpoint-cycle\",\"cycle_id\":\"$CHK_ID\",\"dispatched_to\":$DISPATCHED_JSON,\"status\":\"dispatched\"}" >> "$LOG_FILE"

# Record dispatch timestamp for idle grace period / backstop.
date +%s > "$LAST_DISPATCH_FILE"

echo "Checkpoint cycle $CHK_ID dispatched."
