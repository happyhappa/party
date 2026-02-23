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
LOG_FILE="$STATE_DIR/checkpoints.log"
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

# Idle detection with grace period (shared logic with checkpoint-cycle)
LAST_DISPATCH_FILE="$STATE_DIR/last-checkpoint-dispatch"
GRACE_PERIOD=120  # 2 minutes — ignore JSONL writes caused by checkpoint response

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

    TAIL=$(tmux capture-pane -t "$PANE_ID" -p -S -20 2>/dev/null || echo "CAPTURE_FAILED")
    CMD=$(tmux display-message -t "$PANE_ID" -p '#{pane_current_command}' 2>/dev/null || echo "UNKNOWN")

    STATUS[$ROLE]="healthy"

    # CX footer override — if Codex footer visible, mark healthy and skip
    if [[ "$ROLE" == "cx" ]]; then
        if echo "$TAIL" | grep -qE '(context left|\? for shortcuts)'; then
            # Check for low context — auto-compact if <= 60% and idle
            CX_CONTEXT=$(echo "$TAIL" | grep -oP '\d+(?=% context left)' | tail -1)
            CX_IDLE=$(echo "$TAIL" | grep -qE '(context left|\? for shortcuts)' && echo "true" || echo "false")
            if [[ -n "${CX_CONTEXT:-}" && "$CX_CONTEXT" -le 60 && "$CX_IDLE" == "true" ]]; then
                CX_PANE=$(echo "$PANES_JSON" | jq -r '.panes.cx')
                tmux send-keys -t "$CX_PANE" "/compact" Enter
                echo "{\"timestamp\":\"$TIMESTAMP\",\"type\":\"health-action\",\"role\":\"cx\",\"action\":\"auto_compact\",\"context_pct\":$CX_CONTEXT}" >> "$LOG_FILE"
            fi

            # Store hash and continue to next role
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

# Log completion
echo "{\"timestamp\":\"$TIMESTAMP\",\"type\":\"health-check\",\"results\":{\"oc\":\"${STATUS[oc]}\",\"cc\":\"${STATUS[cc]}\",\"cx\":\"${STATUS[cx]}\"},\"status\":\"complete\"}" >> "$LOG_FILE"
