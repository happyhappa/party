Dispatch a coordinated checkpoint cycle to all agent panes. This is invoked by the relay daemon timer -- do not run interactively.

## Steps

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

Send a checkpoint request to each agent. OC/CC use relay (which injects slash commands). CX gets direct pane injection since Codex processes `/prompts:` commands at its prompt:

```bash
relay send oc "/checkpoint --respond $CHK_ID"
relay send cc "/checkpoint --respond $CHK_ID"
CX_PANE=$(cat ~/llm-share/relay/state/panes.json | jq -r '.panes.cx')
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

- Do NOT send any relay messages other than the checkpoint dispatches in step 3.
- Do NOT wait for agent responses -- this is fire-and-forget dispatch.
- If a `relay send` command fails for one agent, continue dispatching to the remaining agents. Note the failure in the JSONL log entry by adjusting the `dispatched_to` array.
- Return to idle immediately after completion.
- Keep total output under 3 lines to minimize context consumption.
