#!/bin/bash
# continuous-brief-loop.sh — Interim continuous brief generator (Phase 2)
#
# Runs every BRIEF_CADENCE seconds, checking each active role's JSONL
# transcript for new data. If delta >= BRIEF_MIN_DELTA, generates a brief
# via partyctl brief. Retains last 3 continuous briefs per role.
#
# Usage: continuous-brief-loop.sh [--once] [--role ROLE]
#   --once: run one cycle then exit (for testing)
#   --role: limit to a specific role (default: all roles in contract)
#
# Environment:
#   RELAY_STATE_DIR — required
#   BRIEF_CADENCE   — seconds between cycles (default: 300 = 5m)
#   BRIEF_MIN_DELTA — minimum raw JSONL bytes to trigger brief (default: 10240)
#   PARTYCTL_BIN    — path to partyctl binary (default: find on PATH)

set -euo pipefail

: "${RELAY_STATE_DIR:?RELAY_STATE_DIR is required}"

BRIEF_CADENCE="${BRIEF_CADENCE:-300}"
BRIEF_MIN_DELTA="${BRIEF_MIN_DELTA:-10240}"
PARTYCTL="${PARTYCTL_BIN:-$(command -v partyctl 2>/dev/null || echo "")}"
LOG_TAG="continuous-brief"
RUN_ONCE=false
FILTER_ROLE=""

while [[ $# -gt 0 ]]; do
    case "$1" in
        --once) RUN_ONCE=true; shift ;;
        --role) FILTER_ROLE="$2"; shift 2 ;;
        *) echo "Unknown arg: $1" >&2; exit 1 ;;
    esac
done

log() {
    echo "$(date -u +%Y-%m-%dT%H:%M:%SZ) [$LOG_TAG] $*" >&2
}

# Find all active roles by scanning recycle state files
get_active_roles() {
    local roles=()
    for state_file in "$RELAY_STATE_DIR"/recycle-*.json; do
        [[ -f "$state_file" ]] || continue
        local role
        role="$(basename "$state_file" | sed 's/^recycle-//;s/\.json$//')"
        [[ -z "$role" ]] && continue

        # Only process roles in "ready" state
        local state
        state="$(jq -r '.state // "unknown"' "$state_file" 2>/dev/null)"
        if [[ "$state" == "ready" ]]; then
            roles+=("$role")
        fi
    done
    echo "${roles[@]}"
}

# Get transcript path and current offset for a role
get_role_info() {
    local role="$1"
    local state_file="$RELAY_STATE_DIR/recycle-${role}.json"
    [[ -f "$state_file" ]] || return 1

    local transcript_path last_offset
    transcript_path="$(jq -r '.transcript_path // empty' "$state_file" 2>/dev/null)"
    last_offset="$(jq -r '.last_brief_offset // 0' "$state_file" 2>/dev/null)"

    echo "$transcript_path $last_offset"
}

# Check if a role has enough new data for a brief
check_delta() {
    local transcript_path="$1"
    local last_offset="$2"

    [[ -f "$transcript_path" ]] || return 1

    local current_size
    current_size="$(stat -c %s "$transcript_path" 2>/dev/null || echo 0)"
    local delta=$((current_size - last_offset))

    if (( delta >= BRIEF_MIN_DELTA )); then
        echo "$current_size"
        return 0
    fi
    return 1
}

# Run one brief generation cycle
run_cycle() {
    local roles
    if [[ -n "$FILTER_ROLE" ]]; then
        roles=("$FILTER_ROLE")
    else
        read -ra roles <<< "$(get_active_roles)"
    fi

    if [[ ${#roles[@]} -eq 0 ]]; then
        log "no active roles found"
        return
    fi

    for role in "${roles[@]}"; do
        local info
        info="$(get_role_info "$role" 2>/dev/null)" || {
            log "$role: no state file, skipping"
            continue
        }
        read -r transcript_path last_offset <<< "$info"

        if [[ -z "$transcript_path" ]]; then
            log "$role: no transcript path in state, skipping"
            continue
        fi

        local current_size
        current_size="$(check_delta "$transcript_path" "$last_offset" 2>/dev/null)" || {
            log "$role: delta < $BRIEF_MIN_DELTA bytes, skipping"
            continue
        }

        log "$role: generating brief (offset $last_offset → $current_size)"

        if [[ -n "$PARTYCTL" ]]; then
            if "$PARTYCTL" brief "$role" 2>&1 | while read -r line; do log "$role: $line"; done; then
                log "$role: brief generated successfully"
            else
                log "$role: brief generation failed (non-fatal)"
            fi
        else
            log "$role: partyctl not found, skipping brief generation"
        fi
    done
}

# Main loop
if $RUN_ONCE; then
    run_cycle
    exit 0
fi

log "starting continuous brief loop (cadence=${BRIEF_CADENCE}s, min_delta=${BRIEF_MIN_DELTA})"
while true; do
    run_cycle
    sleep "$BRIEF_CADENCE"
done
