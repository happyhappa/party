#!/usr/bin/env bash
#
# DEPRECATED: Use 'partyctl watchdog' instead. This script is kept as a fallback
# for one release cycle and will be removed in a future version.
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

check_relay_health() {
    local pidfile="$STATE_DIR/relay-daemon.pid"
    [[ -f "$pidfile" ]] || return 1
    local pid
    pid=$(cat "$pidfile")
    kill -0 "$pid" 2>/dev/null || return 1
    RELAY_STATE_DIR="$STATE_DIR" relay-daemon --pane-status oc >/dev/null 2>&1 || return 1
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

# Grace period — let relay-daemon finish starting before first health check
sleep 5

LAST_HEALTH_CHECK=0
while true; do
    NOW=$(date +%s)

    # Relay health precondition
    if ! check_relay_health; then
        log "WARNING: relay unhealthy, attempting restart"
        RELAY_RECOVERED=false
        for attempt in 1 2 3; do
            restart_daemon
            sleep 10
            if check_relay_health; then
                RELAY_RECOVERED=true
                break
            fi
            log "WARNING: relay restart attempt $attempt failed"
        done
        if [[ "$RELAY_RECOVERED" != "true" ]]; then
            log "ERROR: relay unrecoverable after 3 attempts — alerting user"
            echo "[ALERT] relay-daemon unrecoverable at $(date)" >> "$STATE_DIR/relay-alerts.log"
            sleep "$SLEEP_INTERVAL"
            continue
        fi
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

    # Write deadman heartbeat
    date +%s > "$STATE_DIR/admin-watchdog.heartbeat"

    # Check for post-compact recovery (CC/OC only — CX handled by health check)
    if command -v relay-daemon >/dev/null 2>&1; then
        for role in oc cc; do
            STATUS=$(relay-daemon --pane-status "$role" 2>/dev/null || true)
            COMPACTED=$(echo "$STATUS" | jq -r ".panes.${role}.compacted // false" 2>/dev/null || echo "false")
            COMPACTED=${COMPACTED:-false}
            if [[ "$COMPACTED" == "true" ]]; then
                MARKER="$STATE_DIR/compacted-seen-$role"
                if [[ ! -f "$MARKER" ]]; then
                    # First time seeing compacted state — send /rec via relay
                    if relay send --from admin "$role" "/rec"; then
                        touch "$MARKER"
                        log "Sent /rec to $role (post-compact recovery)"
                    else
                        log "WARNING: failed to send /rec to $role"
                    fi
                fi
            elif [[ "$COMPACTED" == "false" ]]; then
                # Explicitly not compacted — clear marker so we catch next compact
                rm -f "$STATE_DIR/compacted-seen-$role"
            fi
        done
    fi

    sleep "$SLEEP_INTERVAL"
done
