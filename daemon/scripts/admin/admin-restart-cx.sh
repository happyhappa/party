#!/usr/bin/env bash
#
# admin-restart-cx.sh - Restart the CX (Codex) agent in its tmux pane
#
# Sends Ctrl-C, waits for shell prompt, relaunches Codex.
#
# Environment:
#   RELAY_STATE_DIR - State directory (default: ~/llm-share/relay/state)
#   RELAY_CX_CMD    - CX launch command (default: codex with standard flags)
#

set -euo pipefail

STATE_DIR="${RELAY_STATE_DIR:-$HOME/llm-share/relay/state}"
LOG_FILE="$STATE_DIR/checkpoints.log"
PANES_FILE="$STATE_DIR/panes.json"

CX_CMD="${RELAY_CX_CMD:-codex -a never -s workspace-write --add-dir /tmp --add-dir ~/llm-share --add-dir ~/.cache --add-dir ~/.local/share/relay/outbox/cx}"

# Read CX pane ID
CX_PANE=$(jq -r '.panes.cx // empty' "$PANES_FILE")
if [[ -z "$CX_PANE" ]]; then
    echo "ERROR: CX pane not registered in panes.json" >&2
    exit 1
fi

# Send Ctrl-C to kill current process
tmux send-keys -t "$CX_PANE" C-c

# Poll for shell prompt (2s intervals, 10s timeout)
wait_for_prompt() {
    for i in $(seq 1 5); do
        sleep 2
        LAST_LINE=$(tmux capture-pane -t "$CX_PANE" -p -S -1 2>/dev/null | tail -1)
        if echo "$LAST_LINE" | grep -qE '[$#>❯›⏵%]'; then
            return 0
        fi
    done
    return 1
}

if ! wait_for_prompt; then
    # Retry: send another Ctrl-C
    tmux send-keys -t "$CX_PANE" C-c
    if ! wait_for_prompt; then
        echo "WARNING: Shell prompt not detected after retry. Proceeding anyway."
    fi
fi

# Relaunch CX
tmux send-keys -t "$CX_PANE" "export AGENT_ROLE=cx && $CX_CMD" Enter

# Log the restart
echo "{\"timestamp\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\",\"type\":\"restart-cx\",\"pane\":\"$CX_PANE\",\"status\":\"relaunched\"}" >> "$LOG_FILE"
