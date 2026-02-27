#!/usr/bin/env bash
#
# admin-health-check.sh - Lightweight health check across all agent panes
#
# Heuristic checks: process alive, CX footer, error patterns, bare prompt,
# stale output. Auto-restarts CX if dead, auto-compacts CX if context <= 60%.
#
# Environment:
#   RELAY_STATE_DIR        - State directory (default: ~/llm-share/relay/state)
#   RELAY_ADMIN_ALERT_HOOK - Optional alert command
#   RELAY_CX_CMD           - CX launch command (for restart-cx)
#

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
STATE_DIR="${RELAY_STATE_DIR:-$HOME/llm-share/relay/state}"
LOG_FILE="$STATE_DIR/admin-health.log"
PANES_FILE="$STATE_DIR/panes.json"

if [[ ! -f "$PANES_FILE" ]]; then
    echo "ERROR: panes.json not found" >&2
    exit 1
fi

PANES_JSON=$(cat "$PANES_FILE")
TIMESTAMP=$(date -u +%Y-%m-%dT%H:%M:%SZ)

log_anomaly() {
    local role="$1" anomaly="$2" cmd="$3" detail="$4"
    echo "{\"timestamp\":\"$TIMESTAMP\",\"type\":\"health-anomaly\",\"role\":\"$role\",\"anomaly\":\"$anomaly\",\"cmd\":\"$cmd\",\"detail\":\"$detail\"}" >> "$LOG_FILE"
    if [[ -n "${RELAY_ADMIN_ALERT_HOOK:-}" ]]; then
        $RELAY_ADMIN_ALERT_HOOK "health-check anomaly: $role $anomaly" || true
    fi
}

# Idle detection based on JSONL mtime — agent is idle if no transcript
# activity in the last 5 minutes.
IDLE_THRESHOLD=300  # 5 minutes

is_agent_idle() {
    local role="$1"
    local project_dir
    project_dir=$(jq -r ".${role} // empty" "$STATE_DIR/project-dirs.json" 2>/dev/null)
    [[ -z "$project_dir" ]] && return 1  # can't determine, assume active

    local latest_jsonl
    latest_jsonl=$(ls -t "$project_dir"/*.jsonl 2>/dev/null | head -1)
    [[ -z "$latest_jsonl" ]] && return 1

    local mtime now
    mtime=$(stat -c %Y "$latest_jsonl" 2>/dev/null || echo 0)
    now=$(date +%s)

    if (( now - mtime >= IDLE_THRESHOLD )); then
        return 0  # idle
    fi

    return 1  # active
}

# Deadman heartbeat check — verify admin-watchdog is progressing
HEARTBEAT_FILE="$STATE_DIR/admin-watchdog.heartbeat"
if [[ -f "$HEARTBEAT_FILE" ]]; then
    HEARTBEAT_EPOCH=$(cat "$HEARTBEAT_FILE" 2>/dev/null | tr -d '[:space:]')
    if [[ "$HEARTBEAT_EPOCH" =~ ^[0-9]+$ ]]; then
        HEARTBEAT_AGE=$(( $(date +%s) - HEARTBEAT_EPOCH ))
        if (( HEARTBEAT_AGE > 300 )); then
            log_anomaly "admin" "heartbeat_stale" "admin-watchdog" "heartbeat ${HEARTBEAT_AGE}s old (>300s critical)"
        elif (( HEARTBEAT_AGE > 120 )); then
            echo "[admin-health] WARNING: admin-watchdog heartbeat is ${HEARTBEAT_AGE}s stale (>120s)" >&2
        fi
    fi
fi

declare -A STATUS

for ROLE in oc cc cx; do
    PANE_ID=$(echo "$PANES_JSON" | jq -r ".panes.$ROLE // empty")
    if [[ -z "$PANE_ID" ]]; then
        STATUS[$ROLE]="missing"
        continue
    fi

    # Skip detailed health checks for idle OC/CC
    if [[ "$ROLE" == "oc" || "$ROLE" == "cc" ]] && is_agent_idle "$ROLE"; then
        STATUS[$ROLE]="idle"
        continue
    fi

    PANE_STATUS=$(RELAY_STATE_DIR="$STATE_DIR" relay-daemon --pane-status "$ROLE" 2>/dev/null | jq -r ".panes.$ROLE" 2>/dev/null || echo "CAPTURE_FAILED")
    CMD=$(echo "$PANE_STATUS" | jq -r '.process_name // "UNKNOWN"' 2>/dev/null || echo "UNKNOWN")

    STATUS[$ROLE]="healthy"

    # CX: use pane-status endpoint for context detection and auto-context-cycle
    if [[ "$ROLE" == "cx" ]]; then
        CX_STATUS=$(relay-daemon --pane-status cx 2>/dev/null || true)
        if [[ -n "$CX_STATUS" ]]; then
            CX_CONTEXT=$(echo "$CX_STATUS" | jq -r '.panes.cx.context_pct // -1')
            CX_IDLE=$(echo "$CX_STATUS" | jq -r '.panes.cx.idle // false')

            CX_COMPACTED=$(echo "$CX_STATUS" | jq -r '.panes.cx.compacted // false')

            if [[ "$CX_CONTEXT" -gt 0 && "$CX_CONTEXT" -le 60 && "$CX_IDLE" == "true" && "$CX_COMPACTED" != "true" ]]; then
                # Step 1: Send /compact
                relay send --from admin cx "/compact"
                echo "{\"timestamp\":\"$TIMESTAMP\",\"type\":\"health-action\",\"role\":\"cx\",\"action\":\"auto_compact_start\",\"context_pct\":$CX_CONTEXT}" >> "$LOG_FILE"

                # Step 2: Poll for fresh compaction (5s intervals, max 1min)
                COMPACT_DONE=false
                for i in $(seq 1 12); do
                    sleep 5
                    POLL_STATUS=$(relay-daemon --pane-status cx 2>/dev/null || true)
                    POLL_COMPACTED=$(echo "$POLL_STATUS" | jq -r '.panes.cx.compacted // false')
                    POLL_AGO=$(echo "$POLL_STATUS" | jq -r '.panes.cx.compacted_ago_s // -1')
                    if [[ "$POLL_COMPACTED" == "true" && "$POLL_AGO" -ge 0 && "$POLL_AGO" -lt 120 ]]; then
                        COMPACT_DONE=true
                        break
                    fi
                done

                if [[ "$COMPACT_DONE" == "true" ]]; then
                    # Step 3: Send /rec to restore context
                    sleep 2
                    relay send --from admin cx "/rec"
                    echo "{\"timestamp\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\",\"type\":\"health-action\",\"role\":\"cx\",\"action\":\"auto_context_cycle_complete\"}" >> "$LOG_FILE"
                else
                    echo "{\"timestamp\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\",\"type\":\"health-action\",\"role\":\"cx\",\"action\":\"auto_compact_timeout\"}" >> "$LOG_FILE"
                fi
            fi

            # CX with pane-status detected — store hash and skip further checks
            HASH=$(echo "$PANE_STATUS" | md5sum | cut -d' ' -f1)
            echo "$HASH" > "$STATE_DIR/health-hash-cx.txt"
            continue
        fi
    fi

    # Process alive check
    PROCESS_OK=true
    case "$ROLE" in
        oc|cc)
            if ! echo "$CMD" | grep -qE '^(claude|node)$'; then
                PROCESS_OK=false
            fi
            ;;
        cx)
            if ! echo "$CMD" | grep -qE '^(codex|node)$'; then
                PROCESS_OK=false
            fi
            ;;
    esac

    # Error pattern scan (3+ occurrences of same pattern = problem)
    ERROR_FOUND=false
    for pattern in error panic FATAL killed Traceback SIGTERM SIGKILL OOM; do
        COUNT=$(echo "$PANE_STATUS" | grep -ci "$pattern" 2>/dev/null || echo 0)
        if [[ "$COUNT" -ge 3 ]]; then
            ERROR_FOUND=true
            STATUS[$ROLE]="unhealthy"
            log_anomaly "$ROLE" "error_pattern" "$CMD" "${COUNT}x $pattern"
        fi
    done

    # Bare prompt detection
    BARE_PROMPT=false
    LAST_LINE=$(echo "$PANE_STATUS" | grep -v '^$' | tail -1)
    if echo "$LAST_LINE" | grep -qE '[$%>]' && echo "$CMD" | grep -qE '^(bash|zsh|sh|fish)$'; then
        BARE_PROMPT=true
    fi

    # Stale output detection
    HASH=$(echo "$PANE_STATUS" | md5sum | cut -d' ' -f1)
    PREV_HASH=$(cat "$STATE_DIR/health-hash-${ROLE}.txt" 2>/dev/null || echo "none")
    echo "$HASH" > "$STATE_DIR/health-hash-${ROLE}.txt"

    STALE=false
    if [[ "$HASH" == "$PREV_HASH" ]]; then
        STALE=true
    fi

    # Evaluate combined signals
    if [[ "$PROCESS_OK" == "false" && "$BARE_PROMPT" == "true" ]]; then
        STATUS[$ROLE]="dead"
        log_anomaly "$ROLE" "process_dead" "$CMD" "bare prompt + no agent process"

        # Auto-recover CX only
        if [[ "$ROLE" == "cx" ]]; then
            echo "{\"timestamp\":\"$TIMESTAMP\",\"type\":\"health-anomaly\",\"role\":\"cx\",\"anomaly\":\"process_dead\",\"cmd\":\"$CMD\",\"detail\":\"auto-restart triggered\"}" >> "$LOG_FILE"
            "$SCRIPT_DIR/admin-restart-cx.sh" || true
        fi
    elif [[ "$PROCESS_OK" == "false" ]]; then
        STATUS[$ROLE]="warning"
        log_anomaly "$ROLE" "process_suspect" "$CMD" "unexpected process: $CMD"
    elif [[ "$BARE_PROMPT" == "true" && "$STALE" == "true" ]]; then
        STATUS[$ROLE]="warning"
        log_anomaly "$ROLE" "stale_prompt" "$CMD" "bare prompt + stale output"
    fi
done

# Log completion
echo "{\"timestamp\":\"$TIMESTAMP\",\"type\":\"health-check\",\"results\":{\"oc\":\"${STATUS[oc]}\",\"cc\":\"${STATUS[cc]}\",\"cx\":\"${STATUS[cx]}\"},\"status\":\"complete\"}" >> "$LOG_FILE"
