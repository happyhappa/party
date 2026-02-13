Provide a summary of system state. Can be invoked manually by OC or a human for a quick health check.

## Steps

### 1. Read Pane Map
```bash
echo "=== Pane Map ==="
cat ~/llm-share/relay/state/panes.json | jq .
```

### 2. Read Recent Activity
Parse the structured JSONL log. Show last checkpoint cycle, last health check, and any anomalies:
```bash
echo "=== Recent Activity ==="
if [[ -f "$PWD/state/checkpoints.log" ]]; then
  # Last checkpoint cycle
  echo "Last checkpoint:"
  grep '"type":"checkpoint-cycle"' "$PWD/state/checkpoints.log" | tail -1 | jq -r '"  \(.timestamp) cycle_id=\(.cycle_id) → \(.dispatched_to | join(","))"' 2>/dev/null || echo "  (none)"

  # Last health check
  echo "Last health check:"
  grep '"type":"health-check"' "$PWD/state/checkpoints.log" | tail -1 | jq -r '"  \(.timestamp) status=\(.status) oc=\(.results.oc) cc=\(.results.cc) cx=\(.results.cx)"' 2>/dev/null || echo "  (none)"

  # Recent anomalies (last 3)
  ANOMALIES=$(grep '"type":"health-anomaly"' "$PWD/state/checkpoints.log" | tail -3)
  if [[ -n "$ANOMALIES" ]]; then
    echo "Recent anomalies:"
    echo "$ANOMALIES" | jq -r '"  \(.timestamp) \(.role): \(.anomaly) (\(.detail))"' 2>/dev/null
  fi

  # Recent restarts
  RESTARTS=$(grep '"type":"restart-cx"' "$PWD/state/checkpoints.log" | tail -3)
  if [[ -n "$RESTARTS" ]]; then
    echo "Recent CX restarts:"
    echo "$RESTARTS" | jq -r '"  \(.timestamp) pane=\(.pane) status=\(.status)"' 2>/dev/null
  fi
else
  echo "  (no activity log found)"
fi
```

### 3. Capture Pane Snapshots
Grab the last 5 lines from each agent pane for a brief view of what each agent is doing:
```bash
echo "=== Pane Snapshots ==="
for role in oc cc cx; do
  PANE_ID=$(jq -r ".panes.$role" ~/llm-share/relay/state/panes.json)
  echo "--- $role ($PANE_ID) ---"
  if [[ "$PANE_ID" != "null" && -n "$PANE_ID" ]]; then
    tmux capture-pane -t "$PANE_ID" -p -S -5 2>/dev/null || echo "(unavailable)"
  else
    echo "(no pane registered)"
  fi
done
```

### 4. Check Relay Daemon
```bash
echo "=== Daemon Status ==="
pgrep -f relay-daemon > /dev/null && echo "Relay daemon: running" || echo "Relay daemon: NOT RUNNING"
```

### 5. Format Summary
Combine all the above into a concise summary table. Show timestamps in human-readable format (convert ISO 8601 timestamps to local time where applicable). Present the output as a single, scannable report.

## Rules

- Keep output concise -- this is for quick status checks, not deep diagnostics
- Do NOT send relay messages (this is a local query only)
- Show timestamps in human-readable format
- If panes.json is missing, report that registration has not been done and suggest running `/register-panes`
