#!/bin/bash
# recycle-agent.sh — Full recycle flow for a single role
#
# Implements the RFC-011 Addendum A recycle lifecycle:
#   health detect → notify → exit → confirm → [final brief] → relaunch → hydrate → ACK
#
# Usage: recycle-agent.sh <role> [--force] [--reason REASON]
#   --force: skip state check (resets failed state)
#   --reason: override recycle reason
#
# Environment:
#   RELAY_STATE_DIR    — required
#   RELAY_SHARE_DIR    — required
#   RELAY_TMUX_SESSION — required
#   PARTYCTL_BIN       — path to partyctl (default: find on PATH or build)
#   RELAY_MAIN_DIR     — for contract resolution

set -euo pipefail

: "${RELAY_STATE_DIR:?RELAY_STATE_DIR is required}"
: "${RELAY_SHARE_DIR:?RELAY_SHARE_DIR is required}"

ROLE="${1:?Usage: recycle-agent.sh <role> [--force] [--reason REASON]}"
shift

FORCE=false
REASON="context threshold exceeded"
while [[ $# -gt 0 ]]; do
    case "$1" in
        --force) FORCE=true; shift ;;
        --reason) REASON="$2"; shift 2 ;;
        *) echo "Unknown arg: $1" >&2; exit 1 ;;
    esac
done

PARTYCTL="${PARTYCTL_BIN:-$(command -v partyctl 2>/dev/null || echo "")}"
PANES_FILE="$RELAY_STATE_DIR/panes.json"
STATE_FILE="$RELAY_STATE_DIR/recycle-${ROLE}.json"
LOCK_FILE="$RELAY_STATE_DIR/recycle-${ROLE}.lock"
LOG_FILE="$RELAY_STATE_DIR/admin-health.log"

log() {
    local msg="$(date -u +%Y-%m-%dT%H:%M:%SZ) [recycle:$ROLE] $*"
    echo "$msg" >&2
    echo "$msg" >> "$LOG_FILE" 2>/dev/null || true
}

# Log event as JSON
log_event() {
    local event="$1" status="${2:-ok}"
    echo "{\"timestamp\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\",\"type\":\"recycle\",\"role\":\"$ROLE\",\"event\":\"$event\",\"status\":\"$status\"}" >> "$LOG_FILE" 2>/dev/null || true
}

# Update recycle state
update_state() {
    local new_state="$1"
    local extra="${2:-}"
    if [[ -n "$PARTYCTL" ]]; then
        # TODO: partyctl state transition command (Phase 3)
        :
    fi
    # Direct state file update
    local now
    now="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    if [[ -f "$STATE_FILE" ]]; then
        jq --arg s "$new_state" --arg t "$now" --arg r "$REASON" \
            '.state = $s | .entered_at = $t | .recycle_reason = $r' \
            "$STATE_FILE" > "${STATE_FILE}.tmp" && mv "${STATE_FILE}.tmp" "$STATE_FILE"
    else
        cat > "$STATE_FILE" <<STATEJSON
{"state":"$new_state","entered_at":"$now","recycle_reason":"$REASON"}
STATEJSON
    fi
}

get_state() {
    jq -r '.state // "unknown"' "$STATE_FILE" 2>/dev/null || echo "unknown"
}

get_pid() {
    jq -r '.agent_pid // 0' "$STATE_FILE" 2>/dev/null || echo 0
}

get_pane_id() {
    jq -r ".panes.${ROLE} // empty" "$PANES_FILE" 2>/dev/null
}

# Acquire exclusive lock
exec 9>"$LOCK_FILE"
if ! flock -n 9; then
    log "another recycle is already running for $ROLE"
    exit 1
fi

# Check current state
CURRENT_STATE="$(get_state)"
if [[ "$CURRENT_STATE" != "ready" && "$FORCE" != "true" ]]; then
    log "role $ROLE is in state '$CURRENT_STATE', not 'ready' — use --force to override"
    exit 1
fi

if [[ "$FORCE" == "true" && "$CURRENT_STATE" == "failed" ]]; then
    log "forcing restart from failed state"
fi

PANE_ID="$(get_pane_id)"
if [[ -z "$PANE_ID" ]]; then
    log "ERROR: no pane ID found for $ROLE in $PANES_FILE"
    exit 1
fi

# Resolve exit command from contract or defaults
EXIT_CMD="/exit"
if [[ "$ROLE" == "cx" ]]; then
    EXIT_CMD="ctrl-c"
fi

PID="$(get_pid)"

# --- STEP 1: NOTIFY ---
log "starting recycle: reason=$REASON"
log_event "notify" "starting"
update_state "exiting"

# Best-effort relay notification
if command -v relay >/dev/null 2>&1; then
    relay send all "${ROLE} is recycling, stand by" 2>/dev/null || log "relay notify failed (non-fatal)"
fi

# --- STEP 2: EXIT ---
log_event "exit" "sending"
if [[ "$EXIT_CMD" == "ctrl-c" ]]; then
    tmux send-keys -t "$PANE_ID" C-c 2>/dev/null || true
    sleep 1
    tmux send-keys -t "$PANE_ID" "exit" Enter 2>/dev/null || true
else
    # Claude-style: type the exit command
    tmux send-keys -t "$PANE_ID" "$EXIT_CMD" Enter 2>/dev/null || true
fi

# --- STEP 3: CONFIRM ---
update_state "confirming"
log_event "confirm" "polling"

# Wait for process death via PID
GRACE_PERIOD=30
FORCE_KILLED=false
if [[ "$PID" -gt 0 ]] && kill -0 "$PID" 2>/dev/null; then
    WAITED=0
    while kill -0 "$PID" 2>/dev/null && (( WAITED < GRACE_PERIOD )); do
        sleep 1
        (( WAITED++ )) || true
    done

    if kill -0 "$PID" 2>/dev/null; then
        log "process $PID still alive after ${GRACE_PERIOD}s, sending SIGKILL"
        kill -9 "$PID" 2>/dev/null || true
        sleep 1
        FORCE_KILLED=true
        if kill -0 "$PID" 2>/dev/null; then
            log "ERROR: PID $PID still alive after SIGKILL"
            update_state "failed"
            log_event "confirm" "failed"
            exit 1
        fi
    fi
else
    log "PID $PID already dead or not tracked"
fi

if $FORCE_KILLED; then
    log "force-killed (degraded)"
    log_event "confirm" "force_killed"
fi
log_event "confirm" "dead"

# --- STEP 4: PARALLEL — Final brief (best-effort, non-blocking) ---
update_state "relaunching"

# Launch final brief in background if partyctl is available
if [[ -n "$PARTYCTL" ]]; then
    (
        "$PARTYCTL" brief "$ROLE" --source final 2>&1 | while read -r line; do
            echo "$(date -u +%Y-%m-%dT%H:%M:%SZ) [recycle:$ROLE:final-brief] $line" >> "$LOG_FILE" 2>/dev/null
        done
    ) &
    BRIEF_PID=$!
    disown "$BRIEF_PID" 2>/dev/null || true
    log "final brief launched in background (pid $BRIEF_PID)"
fi

# --- STEP 5: RELAUNCH ---
log_event "relaunch" "starting"

# Get launch command from contract
# For now, use the established patterns from existing admin scripts
LAUNCH_CMD=""
case "$ROLE" in
    oc|cc)
        LAUNCH_CMD="claude --model claude-opus-4-6 --dangerously-skip-permissions"
        ;;
    cx)
        SHARE_DIR="${RELAY_SHARE_DIR}"
        INBOX_DIR="${RELAY_INBOX_DIR:-$SHARE_DIR/outbox}"
        LAUNCH_CMD="codex -a never -s workspace-write --add-dir /tmp --add-dir $SHARE_DIR --add-dir ~/.cache --add-dir $INBOX_DIR/cx --add-dir /mnt/llm-share"
        ;;
    *)
        log "ERROR: unknown role $ROLE — no launch command"
        update_state "failed"
        exit 1
        ;;
esac

# Export required env and launch in the pane
tmux send-keys -t "$PANE_ID" "export AGENT_ROLE=$ROLE && $LAUNCH_CMD" Enter 2>/dev/null || {
    log "ERROR: tmux send-keys failed for relaunch"
    update_state "failed"
    log_event "relaunch" "failed"
    exit 1
}

# Wait for the new process to appear
sleep 3
NEW_PID="$(tmux display-message -p -t "$PANE_ID" '#{pane_pid}' 2>/dev/null || echo 0)"
if [[ "$NEW_PID" -gt 0 ]]; then
    jq --argjson pid "$NEW_PID" '.agent_pid = $pid' "$STATE_FILE" > "${STATE_FILE}.tmp" && mv "${STATE_FILE}.tmp" "$STATE_FILE"
    log "new agent PID: $NEW_PID"
fi
log_event "relaunch" "started"

# --- STEP 6: HYDRATE ---
update_state "hydrating"
log_event "hydrate" "assembling"

# Tier 1 hydration: assemble from disk artifacts
# The new agent will pick up context via its startup hooks (pre-compact recovery file, /restore skill)
# For now, inject a relay message with recovery pointer
if command -v relay >/dev/null 2>&1; then
    relay send "$ROLE" "You have been recycled (context threshold). Run /restore or /rec to recover context. Previous session data is available in your transcript and beads." 2>/dev/null || log "hydration relay send failed (non-fatal)"
fi

log_event "hydrate" "injected"

# --- STEP 7: Wait for ACK (timeout-based for now) ---
# In Phase 3, partyctl watchdog will handle ACK detection via relay message
# For now, transition to ready after a reasonable wait
(
    sleep 30
    # Check if agent is responsive
    if [[ -f "$STATE_FILE" ]]; then
        CURRENT="$(jq -r '.state // ""' "$STATE_FILE" 2>/dev/null)"
        if [[ "$CURRENT" == "hydrating" ]]; then
            jq --arg t "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
                '.state = "ready" | .entered_at = $t | .recycle_reason = ""' \
                "$STATE_FILE" > "${STATE_FILE}.tmp" && mv "${STATE_FILE}.tmp" "$STATE_FILE"
            echo "$(date -u +%Y-%m-%dT%H:%M:%SZ) [recycle:$ROLE] auto-transitioned to ready after 30s" >> "$LOG_FILE" 2>/dev/null
        fi
    fi
) &
disown $! 2>/dev/null || true

log "recycle complete — agent relaunched, hydration injected, awaiting ACK"
log_event "complete" "success"

# Release flock
flock -u 9
exec 9>&-
