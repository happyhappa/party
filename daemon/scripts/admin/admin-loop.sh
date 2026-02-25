#!/usr/bin/env bash
#
# admin-loop.sh - Main admin scheduler loop (replaces admin LLM pane)
#
# Runs checkpoint cycles every 10min and health checks every 5min.
# Started as a background process by the party launcher.
#
# Environment:
#   RELAY_STATE_DIR          - State directory (required)
#   RELAY_CHECKPOINT_INTERVAL - Checkpoint interval in seconds (default: 600)
#   RELAY_HEALTH_CHECK_INTERVAL - Health check interval in seconds (default: 300)
#

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
STATE_DIR="${RELAY_STATE_DIR:?RELAY_STATE_DIR not set — must be exported by bin/party}"
CHECKPOINT_INTERVAL="${RELAY_CHECKPOINT_INTERVAL:-600}"
HEALTH_CHECK_INTERVAL="${RELAY_HEALTH_CHECK_INTERVAL:-300}"
SLEEP_INTERVAL=30

# Write PID file
mkdir -p "$STATE_DIR"
echo $$ > "$STATE_DIR/admin-loop.pid"

log() {
    echo "[admin-loop] $(date '+%H:%M:%S') $1"
}

cleanup() {
    log "Shutting down"
    rm -f "$STATE_DIR/admin-loop.pid"
    exit 0
}
trap cleanup EXIT SIGTERM SIGINT

LOG_DIR="${RELAY_LOG_DIR:-$HOME/llm-share/relay/log}"

# Check if relay-daemon is alive; restart if dead
check_daemon() {
    local pidfile="$STATE_DIR/relay-daemon.pid"
    if [[ ! -f "$pidfile" ]]; then
        log "WARNING: relay-daemon PID file missing"
        return 1
    fi
    local pid
    pid=$(cat "$pidfile")
    if ! kill -0 "$pid" 2>/dev/null; then
        log "WARNING: relay-daemon (pid=$pid) is dead"
        return 1
    fi
    return 0
}

restart_daemon() {
    log "Attempting relay-daemon restart..."
    relay-daemon >> "$LOG_DIR/relay.log" 2>&1 & disown
    sleep 1
    if check_daemon; then
        log "relay-daemon restarted successfully (pid=$(cat "$STATE_DIR/relay-daemon.pid"))"
    else
        log "ERROR: relay-daemon restart failed"
    fi
}

log "Started (pid=$$, checkpoint=${CHECKPOINT_INTERVAL}s, health=${HEALTH_CHECK_INTERVAL}s)"

# Initialize to now so first cycle waits a full interval
LAST_CHECKPOINT=$(date +%s)
LAST_HEALTH_CHECK=$(date +%s)

while true; do
    NOW=$(date +%s)

    # Checkpoint cycle
    if (( NOW - LAST_CHECKPOINT >= CHECKPOINT_INTERVAL )); then
        log "Running checkpoint cycle"
        "$SCRIPT_DIR/admin-checkpoint-cycle.sh" 2>&1 || log "Checkpoint cycle failed (exit $?)"
        LAST_CHECKPOINT=$(date +%s)
    fi

    # Health check + daemon watchdog
    if (( NOW - LAST_HEALTH_CHECK >= HEALTH_CHECK_INTERVAL )); then
        log "Running health check"
        "$SCRIPT_DIR/admin-health-check.sh" 2>&1 || log "Health check failed (exit $?)"
        if ! check_daemon; then
            restart_daemon
        fi
        LAST_HEALTH_CHECK=$(date +%s)
    fi

    sleep "$SLEEP_INTERVAL"
done
