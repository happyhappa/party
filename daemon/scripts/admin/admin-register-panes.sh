#!/usr/bin/env bash
#
# admin-register-panes.sh - Discover tmux pane IDs and write pane map
#
# Enumerates panes in the party tmux session, matches @role options,
# writes panes.json atomically with version increment.
#
# Environment:
#   RELAY_STATE_DIR    - State directory (required)
#   RELAY_TMUX_SESSION - Session name (preferred)
#   TMUX_SESSION       - Session name (legacy fallback)
#

set -euo pipefail

STATE_DIR="${RELAY_STATE_DIR:?RELAY_STATE_DIR not set — must be exported by bin/party}"
PANES_FILE="$STATE_DIR/panes.json"

# Determine session name
SESSION="${RELAY_TMUX_SESSION:-${TMUX_SESSION:-$(tmux display-message -p '#{session_name}' 2>/dev/null || echo 'party')}}"

# Read current version
CURRENT_VERSION=$(jq -r '.version // 0' "$PANES_FILE" 2>/dev/null || echo "0")
NEW_VERSION=$((CURRENT_VERSION + 1))

# Enumerate panes and match @role
OC_PANE="" CC_PANE="" ADMIN_PANE="" CX_PANE=""
while read -r PANE_ID ROLE WINDOW_NAME; do
    case "$ROLE" in
        oc)    OC_PANE="$PANE_ID" ;;
        cc)    CC_PANE="$PANE_ID" ;;
        admin) ADMIN_PANE="$PANE_ID" ;;
        cx)    CX_PANE="$PANE_ID" ;;
    esac
    # Fallback: check window name for CX
    if [[ -z "$CX_PANE" && "$(echo "$WINDOW_NAME" | tr '[:upper:]' '[:lower:]')" == "cx" ]]; then
        CX_PANE="$PANE_ID"
    fi
done < <(tmux list-panes -s -t "$SESSION" -F '#{pane_id} #{@role} #{window_name}')

# Warn about missing roles
for ROLE_NAME in oc cc cx; do
    VAR="${ROLE_NAME^^}_PANE"
    eval "VAL=\$$VAR"
    if [[ -z "$VAL" ]]; then
        echo "WARNING: Could not find pane for role '$ROLE_NAME'" >&2
    fi
done

# Write panes.json atomically
TIMESTAMP=$(date -u +%Y-%m-%dT%H:%M:%SZ)
mkdir -p "$STATE_DIR"

jq -n \
    --arg oc "${OC_PANE:-}" \
    --arg cc "${CC_PANE:-}" \
    --arg admin "${ADMIN_PANE:-}" \
    --arg cx "${CX_PANE:-}" \
    --argjson version "$NEW_VERSION" \
    --arg ts "$TIMESTAMP" \
    '{
        panes: {
            oc: (if $oc == "" then null else $oc end),
            cc: (if $cc == "" then null else $cc end),
            admin: (if $admin == "" then null else $admin end),
            cx: (if $cx == "" then null else $cx end)
        },
        version: $version,
        registered_at: $ts
    }' > "$PANES_FILE.tmp"

mv "$PANES_FILE.tmp" "$PANES_FILE"

echo "Pane registration complete (version ${NEW_VERSION}):"
jq . "$PANES_FILE"
