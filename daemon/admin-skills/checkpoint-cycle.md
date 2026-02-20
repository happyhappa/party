Dispatch a coordinated checkpoint cycle to all agent panes. This is invoked by the relay daemon timer -- do not run interactively.

## Steps

### 0. Guard: skip if a checkpoint was dispatched recently

Check the last dispatch time. If a checkpoint was dispatched within the last 8 minutes, skip this cycle entirely to prevent stacking:

```bash
LAST_DISPATCH=$(grep '"type":"checkpoint-cycle"' "$PWD/state/checkpoints.log" 2>/dev/null | tail -1 | jq -r '.timestamp // empty')
if [[ -n "$LAST_DISPATCH" ]]; then
  LAST_EPOCH=$(date -d "$LAST_DISPATCH" +%s 2>/dev/null || echo 0)
  NOW_EPOCH=$(date +%s)
  AGE=$(( NOW_EPOCH - LAST_EPOCH ))
  if [[ $AGE -lt 480 ]]; then
    echo "Checkpoint dispatched ${AGE}s ago, skipping."
    exit 0
  fi
fi
```

### 1. Read pane registry

```bash
cat ~/llm-share/relay/state/panes.json
```

Confirm that pane IDs exist for `oc`, `cc`, and `cx`. If any are missing, note them but continue with the available agents.

### 2. Generate cycle nonce

```bash
CHK_ID="chk-$(date +%s)-$(head -c 4 /dev/urandom | xxd -p)"
```

### 3. Dispatch checkpoint requests

Send a checkpoint request to each agent via direct tmux injection. Slash commands are never sent via relay — relay is for inter-agent conversation only.

```bash
PANES_JSON=$(cat ~/llm-share/relay/state/panes.json)
OC_PANE=$(echo "$PANES_JSON" | jq -r '.panes.oc')
CC_PANE=$(echo "$PANES_JSON" | jq -r '.panes.cc')
CX_PANE=$(echo "$PANES_JSON" | jq -r '.panes.cx')
tmux send-keys -t "$OC_PANE" "/checkpoint --respond $CHK_ID" Enter
tmux send-keys -t "$CC_PANE" "/checkpoint --respond $CHK_ID" Enter
tmux send-keys -t "$CX_PANE" "/prompts:checkpoint $CHK_ID" Enter
```

### 4. Log the dispatch

Append a JSONL entry to the admin state directory:

```bash
echo "{\"timestamp\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\",\"type\":\"checkpoint-cycle\",\"cycle_id\":\"$CHK_ID\",\"dispatched_to\":[\"oc\",\"cc\",\"cx\"],\"status\":\"dispatched\"}" >> "$PWD/state/checkpoints.log"
```

### 5. Return silently

Output a single confirmation line, then stop. Do not produce further output.

```
Checkpoint cycle $CHK_ID dispatched.
```

## Rules

- Do NOT send any relay messages -- checkpoint dispatches use direct tmux injection, not relay.
- Do NOT wait for agent responses -- this is fire-and-forget dispatch.
- If a `tmux send-keys` command fails for one agent, continue dispatching to the remaining agents. Note the failure in the JSONL log entry by adjusting the `dispatched_to` array.
- Return to idle immediately after completion.
- Keep total output under 3 lines to minimize context consumption.
