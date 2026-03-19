#!/usr/bin/env bash
#
# DEPRECATED: Use 'partyctl watchdog' instead. This script is kept as a fallback
# for one release cycle and will be removed in a future version.
#
# admin-health-check.sh - Lightweight health check across all agent panes
#
# Heuristic checks: process alive, CX footer, error patterns, bare prompt,
# stale output. Auto-restarts CX if dead, auto-compacts CX if context >= 40% used.
# Sidecar telemetry: session restart detection, cost monitoring, model drift,
# staleness detection for CC/OC.
#
# Environment:
#   RELAY_STATE_DIR        - State directory (default: ~/llm-share/relay/state)
#   RELAY_ADMIN_ALERT_HOOK - Optional alert command
#   RELAY_CX_CMD           - CX launch command (for restart-cx)
#   EXPECTED_MODEL_OC      - Expected model for OC (default: claude-opus-4-6)
#   EXPECTED_MODEL_CC      - Expected model for CC (default: claude-opus-4-6)
#   COST_ALERT_THRESHOLD   - USD delta per check interval to trigger alert (default: 5.00)
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
    TAIL=$(tmux capture-pane -t "$PANE_ID" -p -S -20 2>/dev/null || echo "CAPTURE_FAILED")

    STATUS[$ROLE]="healthy"

    # CX: use pane-status endpoint for context detection and auto-context-cycle
    if [[ "$ROLE" == "cx" ]]; then
        CX_STATUS=$(RELAY_STATE_DIR="$STATE_DIR" relay-daemon --pane-status cx 2>/dev/null || true)
        if [[ -n "$CX_STATUS" ]]; then
            CX_CONTEXT=$(echo "$CX_STATUS" | jq -r '.panes.cx.context_pct // -1')
            CX_IDLE=$(echo "$CX_STATUS" | jq -r '.panes.cx.idle // false')

            CX_COMPACTED=$(echo "$CX_STATUS" | jq -r '.panes.cx.compacted // false')

            if [[ "$CX_CONTEXT" -gt 0 && "$CX_CONTEXT" -ge 40 && "$CX_IDLE" == "true" && "$CX_COMPACTED" != "true" ]]; then
                # Step 1: Send /compact
                relay send --from admin cx "/compact" || { log_anomaly "cx" "relay_send_failed" "" "/compact"; }
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
                    relay send --from admin cx "/rec" || { log_anomaly "cx" "relay_send_failed" "" "/rec"; }
                    echo "{\"timestamp\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\",\"type\":\"health-action\",\"role\":\"cx\",\"action\":\"auto_context_cycle_complete\"}" >> "$LOG_FILE"
                else
                    echo "{\"timestamp\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\",\"type\":\"health-action\",\"role\":\"cx\",\"action\":\"auto_compact_timeout\"}" >> "$LOG_FILE"
                fi
            fi

            # Check CX process health even when pane-status succeeds
            CX_PROC=$(echo "$CX_STATUS" | jq -r '.panes.cx.process_name // "UNKNOWN"')
            if ! echo "$CX_PROC" | grep -qE '^(codex|node)$'; then
                STATUS[cx]="dead"
                log_anomaly "cx" "process_dead" "$CX_PROC" "pane-status ok but process not codex/node"
                "$SCRIPT_DIR/admin-restart-cx.sh" || true
            fi

            # CX with pane-status detected — store hash and skip further checks
            HASH=$(echo "$TAIL" | md5sum | cut -d' ' -f1)
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
        COUNT=$(echo "$TAIL" | grep -ci "$pattern" 2>/dev/null || echo 0)
        if [[ "$COUNT" -ge 3 ]]; then
            ERROR_FOUND=true
            STATUS[$ROLE]="unhealthy"
            log_anomaly "$ROLE" "error_pattern" "$CMD" "${COUNT}x $pattern"
        fi
    done

    # Bare prompt detection
    BARE_PROMPT=false
    LAST_LINE=$(echo "$TAIL" | grep -v '^$' | tail -1)
    if echo "$LAST_LINE" | grep -qE '[$%>]' && echo "$CMD" | grep -qE '^(bash|zsh|sh|fish)$'; then
        BARE_PROMPT=true
    fi

    # Stale output detection
    HASH=$(echo "$TAIL" | md5sum | cut -d' ' -f1)
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

# --- CC/OC auto-compact via sidecar telemetry ---
# Claude auto-compacts at 85% used. We fire at 75% so we control the cycle:
# /pc (checkpoint) -> /compact -> /rec (restore). Threshold must stay below 85%.
CLAUDE_COMPACT_THRESHOLD=75  # percent USED

for ROLE in oc cc; do
    SIDECAR="$STATE_DIR/telemetry-${ROLE}.json"
    [[ -f "$SIDECAR" ]] || continue

    # Read sidecar and validate JSON before extracting fields
    SIDECAR_JSON=$(cat "$SIDECAR" 2>/dev/null) || continue
    echo "$SIDECAR_JSON" | jq -e . >/dev/null 2>&1 || continue
    SIDECAR_TS=$(echo "$SIDECAR_JSON" | jq -r '.timestamp // 0')
    NOW_EPOCH=$(date +%s)
    SIDECAR_AGE=$(( NOW_EPOCH - SIDECAR_TS ))
    if (( SIDECAR_AGE > 120 || SIDECAR_AGE < 0 )); then
        continue  # stale or future — skip
    fi

    CTX_USED=$(echo "$SIDECAR_JSON" | jq -r '.context_pct // -1')
    # context_pct from sidecar is USED percentage (not remaining)
    if [[ "$CTX_USED" == "null" || "$CTX_USED" == "-1" ]]; then
        continue
    fi
    # Convert to integer (truncate decimals)
    CTX_USED_INT=${CTX_USED%.*}

    if (( CTX_USED_INT < CLAUDE_COMPACT_THRESHOLD )); then
        continue  # not at threshold yet
    fi

    # Check pane-status for idle + not already compacted
    ROLE_STATUS=$(RELAY_STATE_DIR="$STATE_DIR" relay-daemon --pane-status "$ROLE" 2>/dev/null || true)
    [[ -z "$ROLE_STATUS" ]] && continue

    ROLE_IDLE=$(echo "$ROLE_STATUS" | jq -r ".panes.${ROLE}.idle // false")
    ROLE_COMPACTED=$(echo "$ROLE_STATUS" | jq -r ".panes.${ROLE}.compacted // false")
    ROLE_COMPACT_AGO=$(echo "$ROLE_STATUS" | jq -r ".panes.${ROLE}.compacted_ago_s // -1")

    # Skip if agent is busy
    [[ "$ROLE_IDLE" != "true" ]] && continue
    # Skip if recently compacted (within 5 minutes)
    if [[ "$ROLE_COMPACTED" == "true" && "$ROLE_COMPACT_AGO" -ge 0 && "$ROLE_COMPACT_AGO" -lt 300 ]]; then
        continue
    fi

    echo "{\"timestamp\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\",\"type\":\"health-action\",\"role\":\"$ROLE\",\"action\":\"auto_compact_start\",\"context_pct_used\":$CTX_USED_INT}" >> "$LOG_FILE"

    # Step 1: Pre-compact checkpoint
    relay send --from admin "$ROLE" "/pc" || { log_anomaly "$ROLE" "relay_send_failed" "" "/pc"; continue; }
    sleep 10

    # Step 2: Compact
    relay send --from admin "$ROLE" "/compact" || { log_anomaly "$ROLE" "relay_send_failed" "" "/compact"; continue; }

    # Step 3: Poll for compaction (5s intervals, max 1min)
    COMPACT_DONE=false
    for i in $(seq 1 12); do
        sleep 5
        POLL_STATUS=$(RELAY_STATE_DIR="$STATE_DIR" relay-daemon --pane-status "$ROLE" 2>/dev/null || true)
        POLL_COMPACTED=$(echo "$POLL_STATUS" | jq -r ".panes.${ROLE}.compacted // false")
        POLL_AGO=$(echo "$POLL_STATUS" | jq -r ".panes.${ROLE}.compacted_ago_s // -1")
        if [[ "$POLL_COMPACTED" == "true" && "$POLL_AGO" -ge 0 && "$POLL_AGO" -lt 120 ]]; then
            COMPACT_DONE=true
            break
        fi
    done

    if [[ "$COMPACT_DONE" == "true" ]]; then
        # Step 4: Restore context
        sleep 2
        relay send --from admin "$ROLE" "/rec" || { log_anomaly "$ROLE" "relay_send_failed" "" "/rec"; }
        echo "{\"timestamp\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\",\"type\":\"health-action\",\"role\":\"$ROLE\",\"action\":\"auto_context_cycle_complete\"}" >> "$LOG_FILE"
    else
        echo "{\"timestamp\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\",\"type\":\"health-action\",\"role\":\"$ROLE\",\"action\":\"auto_compact_timeout\"}" >> "$LOG_FILE"
    fi
done

# --- CC/OC sidecar telemetry checks (3b: session restart, 3c: cost, 3d: model drift, 3e: staleness) ---
EXPECTED_MODEL_OC="${EXPECTED_MODEL_OC:-claude-opus-4-6}"
EXPECTED_MODEL_CC="${EXPECTED_MODEL_CC:-claude-opus-4-6}"
COST_ALERT_THRESHOLD="${COST_ALERT_THRESHOLD:-5.00}"

for ROLE in oc cc; do
    SIDECAR="$STATE_DIR/telemetry-${ROLE}.json"
    [[ -f "$SIDECAR" ]] || continue

    SC_JSON=$(cat "$SIDECAR" 2>/dev/null) || continue
    echo "$SC_JSON" | jq -e . >/dev/null 2>&1 || continue
    SC_TS=$(echo "$SC_JSON" | jq -r '.timestamp // 0')
    SC_AGE=$(( $(date +%s) - SC_TS ))
    # Skip all sidecar checks if data is too old
    if (( SC_AGE > 120 || SC_AGE < 0 )); then
        # 3e: telemetry staleness — agent may be active but sidecar is old
        if (( SC_AGE > 600 )) && ! is_agent_idle "$ROLE"; then
            log_anomaly "$ROLE" "telemetry_stale" "" "sidecar ${SC_AGE}s old, agent not idle — possible hang"
        fi
        continue
    fi

    # 3b: Session restart detection
    SC_SESSION=$(echo "$SC_JSON" | jq -r '.session_id // empty')
    if [[ -n "$SC_SESSION" ]]; then
        SESSION_FILE="$STATE_DIR/session-id-${ROLE}.txt"
        if [[ -f "$SESSION_FILE" ]]; then
            PREV_SESSION=$(cat "$SESSION_FILE" 2>/dev/null)
            if [[ "$SC_SESSION" != "$PREV_SESSION" ]]; then
                echo "{\"timestamp\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\",\"type\":\"health-action\",\"role\":\"$ROLE\",\"action\":\"session_restart_detected\",\"old_session\":\"$PREV_SESSION\",\"new_session\":\"$SC_SESSION\"}" >> "$LOG_FILE"
                relay send --from admin "$ROLE" "/rec" || { log_anomaly "$ROLE" "relay_send_failed" "" "/rec after restart"; }
                echo "$SC_SESSION" > "$SESSION_FILE"
            fi
        else
            echo "$SC_SESSION" > "$SESSION_FILE"
        fi
    fi

    # 3c: Cost monitoring
    SC_COST=$(echo "$SC_JSON" | jq -r '.cost_usd // empty')
    if [[ -n "$SC_COST" && "$SC_COST" != "null" ]]; then
        COST_FILE="$STATE_DIR/last-cost-${ROLE}.txt"
        if [[ -f "$COST_FILE" ]]; then
            PREV_COST=$(cat "$COST_FILE" 2>/dev/null || echo "0")
            # Validate both values are numeric before passing to awk
            if [[ "$SC_COST" =~ ^[0-9.]+$ && "$PREV_COST" =~ ^[0-9.]+$ ]]; then
                COST_DELTA=$(awk "BEGIN { printf \"%.2f\", $SC_COST - $PREV_COST }")
                IS_SPIKE=$(awk "BEGIN { print ($COST_DELTA > $COST_ALERT_THRESHOLD) ? \"yes\" : \"no\" }")
                if [[ "$IS_SPIKE" == "yes" ]]; then
                    log_anomaly "$ROLE" "cost_spike" "" "delta \$${COST_DELTA} (threshold \$${COST_ALERT_THRESHOLD}), total \$${SC_COST}"
                fi
            fi
        fi
        echo "$SC_COST" > "$COST_FILE"
    fi

    # 3d: Model drift detection
    SC_MODEL=$(echo "$SC_JSON" | jq -r '.model_id // empty')
    if [[ -n "$SC_MODEL" && "$SC_MODEL" != "null" ]]; then
        EXPECTED_VAR="EXPECTED_MODEL_${ROLE^^}"
        EXPECTED_MODEL="${!EXPECTED_VAR}"
        if [[ "$SC_MODEL" != "$EXPECTED_MODEL" ]]; then
            log_anomaly "$ROLE" "model_drift" "" "expected=$EXPECTED_MODEL actual=$SC_MODEL"
        fi
    fi
done

# Log completion
echo "{\"timestamp\":\"$TIMESTAMP\",\"type\":\"health-check\",\"results\":{\"oc\":\"${STATUS[oc]}\",\"cc\":\"${STATUS[cc]}\",\"cx\":\"${STATUS[cx]}\"},\"status\":\"complete\"}" >> "$LOG_FILE"
