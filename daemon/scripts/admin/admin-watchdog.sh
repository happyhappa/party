#!/usr/bin/env bash
#
# admin-watchdog.sh - Admin watchdog loop (health checks + daemon restart)
#
# Runs health checks every 5min and restarts relay-daemon if dead.
# Started as a background process by the party launcher.
#
# Environment:
#   RELAY_STATE_DIR          - State directory (default: ~/llm-share/relay/state)
#   RELAY_HEALTH_CHECK_INTERVAL - Health check interval in seconds (default: 300)
#

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
STATE_DIR="${RELAY_STATE_DIR:-$HOME/llm-share/relay/state}"
HEALTH_CHECK_INTERVAL="${RELAY_HEALTH_CHECK_INTERVAL:-300}"
SLEEP_INTERVAL=30

# Write PID file
mkdir -p "$STATE_DIR"
echo $$ > "$STATE_DIR/admin-watchdog.pid"

log() {
    echo "[admin-watchdog] $(date '+%H:%M:%S') $1"
}

cleanup() {
    log "Shutting down"
    rm -f "$STATE_DIR/admin-watchdog.pid"
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
    setsid relay-daemon >> "$LOG_DIR/relay.log" 2>&1 &
    sleep 1
    if check_daemon; then
        log "relay-daemon restarted successfully (pid=$(cat "$STATE_DIR/relay-daemon.pid"))"
    else
        log "ERROR: relay-daemon restart failed"
    fi
}

log "Started (pid=$$, health=${HEALTH_CHECK_INTERVAL}s)"

LAST_HEALTH_CHECK=0
while true; do
    NOW=$(date +%s)

    # Health check + daemon watchdog
    if (( NOW - LAST_HEALTH_CHECK >= HEALTH_CHECK_INTERVAL )); then
        log "Running health check"
        "$SCRIPT_DIR/admin-health-check.sh" 2>&1 || log "Health check failed (exit $?)"
        if ! check_daemon; then
            restart_daemon
        fi
        LAST_HEALTH_CHECK=$(date +%s)
    fi

    # Write deadman heartbeat
    date +%s > "$STATE_DIR/admin-watchdog.heartbeat"

    sleep "$SLEEP_INTERVAL"
done
