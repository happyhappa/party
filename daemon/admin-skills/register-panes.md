Discover tmux pane IDs and write the pane map for the relay daemon. This registers which tmux pane belongs to which agent role.

## Steps

### 1. Determine Session Name
Get the tmux session name. Prefer detecting from the current pane, fall back to TMUX_SESSION env var, then default to "party":
```bash
SESSION="${TMUX_SESSION:-$(tmux display-message -p '#{session_name}' 2>/dev/null || echo 'party')}"
echo "Using tmux session: $SESSION"
```

### 2. List All Panes
Enumerate every pane across all windows in the session:
```bash
tmux list-panes -s -t "$SESSION" -F '#{pane_id} #{pane_title} #{window_name}'
```

### 3. Read Current Version
Read the existing panes.json to get the current version number, then increment:
```bash
CURRENT_VERSION=$(jq -r '.version // 0' ~/llm-share/relay/state/panes.json 2>/dev/null || echo "0")
NEW_VERSION=$((CURRENT_VERSION + 1))
echo "Current version: $CURRENT_VERSION -> New version: $NEW_VERSION"
```

### 4. Build Pane Map
Map pane titles to roles. Match titles OC, CC, Admin, and CX to their pane IDs using case-insensitive comparison. Also check window names for the CX hidden window (CX sometimes runs in a named window rather than a titled pane).

For each pane in the listing:
- If the pane title matches "oc" (case-insensitive), assign its pane ID to the `oc` role
- If the pane title matches "cc" (case-insensitive), assign its pane ID to the `cc` role
- If the pane title matches "admin" (case-insensitive), assign its pane ID to the `admin` role
- If the pane title matches "cx" (case-insensitive) OR the window name matches "cx" (case-insensitive), assign its pane ID to the `cx` role

Build the mapping as shell variables:
```bash
while read -r PANE_ID PANE_TITLE WINDOW_NAME; do
  TITLE_LOWER=$(echo "$PANE_TITLE" | tr '[:upper:]' '[:lower:]')
  WINDOW_LOWER=$(echo "$WINDOW_NAME" | tr '[:upper:]' '[:lower:]')
  case "$TITLE_LOWER" in
    oc)    OC_PANE="$PANE_ID" ;;
    cc)    CC_PANE="$PANE_ID" ;;
    admin) ADMIN_PANE="$PANE_ID" ;;
    cx)    CX_PANE="$PANE_ID" ;;
  esac
  # Also check window name for CX
  if [[ -z "$CX_PANE" && "$WINDOW_LOWER" == "cx" ]]; then
    CX_PANE="$PANE_ID"
  fi
done < <(tmux list-panes -s -t "$SESSION" -F '#{pane_id} #{pane_title} #{window_name}')
```

Log warnings for any roles that could not be found:
```bash
for ROLE_VAR in OC_PANE CC_PANE ADMIN_PANE CX_PANE; do
  if [[ -z "${!ROLE_VAR}" ]]; then
    ROLE_NAME=$(echo "$ROLE_VAR" | sed 's/_PANE//' | tr '[:upper:]' '[:lower:]')
    echo "WARNING: Could not find pane for role '$ROLE_NAME'"
  fi
done
```

### 5. Write panes.json Atomically
Write to a temp file, then move into place:
```bash
TIMESTAMP=$(date -u +%Y-%m-%dT%H:%M:%SZ)
mkdir -p ~/llm-share/relay/state

# Build JSON with jq to handle null values properly (null literal, not "null" string)
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
  }' > ~/llm-share/relay/state/panes.json.tmp

mv ~/llm-share/relay/state/panes.json.tmp ~/llm-share/relay/state/panes.json
```

### 6. Confirm
Output the result:
```bash
echo "Pane registration complete (version ${NEW_VERSION}):"
jq . ~/llm-share/relay/state/panes.json
```

## Rules

- Overwrite panes.json atomically (write to tmp, mv into place)
- If a role can't be found, log a warning but still write what was found (use "null" for missing panes)
- Return silently after confirmation -- do not send relay messages or notify other agents
