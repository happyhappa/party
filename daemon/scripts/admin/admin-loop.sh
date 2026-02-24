#!/usr/bin/env bash
#
# admin-loop.sh - Main admin scheduler loop (replaces admin LLM pane)
#
# Runs checkpoint cycles every 10min and health checks every 5min.
# Started as a background process by the party launcher.
#
# Environment:
#   RELAY_STATE_DIR          - State directory (default: ~/llm-share/relay/state)
#   RELAY_CHECKPOINT_INTERVAL - Checkpoint interval in seconds (default: 600)
#   RELAY_HEALTH_CHECK_INTERVAL - Health check interval in seconds (default: 300)
#

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
STATE_DIR="${RELAY_STATE_DIR:-$HOME/llm-share/relay/state}"
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

log "Started (pid=$$, checkpoint=${CHECKPOINT_INTERVAL}s, health=${HEALTH_CHECK_INTERVAL}s)"

LAST_CHECKPOINT=0
LAST_HEALTH_CHECK=0
while true; do
    NOW=$(date +%s)

    # Checkpoint cycle
    if (( NOW - LAST_CHECKPOINT >= CHECKPOINT_INTERVAL )); then
        log "Running checkpoint cycle"
        "$SCRIPT_DIR/admin-checkpoint-cycle.sh" 2>&1 || log "Checkpoint cycle failed (exit $?)"
        LAST_CHECKPOINT=$(date +%s)
    fi

    # Health check
    if (( NOW - LAST_HEALTH_CHECK >= HEALTH_CHECK_INTERVAL )); then
        log "Running health check"
        "$SCRIPT_DIR/admin-health-check.sh" 2>&1 || log "Health check failed (exit $?)"
        LAST_HEALTH_CHECK=$(date +%s)
    fi

    sleep "$SLEEP_INTERVAL"
done
