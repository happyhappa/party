Run a lightweight health check across all agent panes. This is invoked by the relay daemon timer -- do not run interactively.

## Steps

### 1. Read pane registry

```bash
PANES_JSON=$(cat ~/llm-share/relay/state/panes.json)
```

Extract pane IDs for each role (`oc`, `cc`, `cx`). Use `jq -r '.panes.<role>'` to extract. If a pane ID is `null` or empty, skip that role's health check and mark its status as `missing`.

### 2. Capture pane state for each agent

For each role, capture the last 20 lines of output and the current running command:

```bash
PANE_ID="<pane_id from panes.json>"
TAIL=$(tmux capture-pane -t "$PANE_ID" -p -S -20 2>/dev/null || echo "CAPTURE_FAILED")
CMD=$(tmux display-message -t "$PANE_ID" -p '#{pane_current_command}' 2>/dev/null || echo "UNKNOWN")
```

Store `TAIL` and `CMD` for each role. Do NOT include the captured output in your response.

### 3. Run heuristic checks

For each agent pane, evaluate the following heuristics. No LLM assessment is needed when all pass.

**a) Process alive check:**
The `CMD` value should be a known agent process. Expected values:
- `oc`, `cc`: `claude` or `node` (Claude Code)
- `cx`: `codex` or `node` (Codex CLI)

If `CMD` is `bash`, `zsh`, or another bare shell, the agent has likely crashed.

**b) Error pattern scan:**
Check `TAIL` for repeated occurrences of: `error`, `panic`, `FATAL`, `killed`, `Traceback`, `SIGTERM`, `SIGKILL`, `OOM`.
A single occurrence may be benign; three or more of the same pattern indicates a problem.

**c) Bare prompt detection:**
If the last non-empty line of `TAIL` matches a shell prompt pattern (`$`, `%`, `>`, or contains the hostname) AND `CMD` is a shell, the agent is not running.

**d) Stale output detection:**
Compare an MD5 hash of `TAIL` against the stored hash from the previous health check:

```bash
HASH=$(echo "$TAIL" | md5sum | cut -d' ' -f1)
PREV_HASH=$(cat "$PWD/state/health-hash-${ROLE}.txt" 2>/dev/null || echo "none")
```

If `HASH` equals `PREV_HASH`, the output has not changed since the last check. This alone is not an anomaly (agent may be thinking), but combined with a bare shell prompt it confirms a stalled or dead agent.

### 4. Store hashes for next cycle

```bash
echo "$HASH" > "$PWD/state/health-hash-${ROLE}.txt"
```

### 5. Evaluate results

If ALL heuristics pass for ALL agents: log status as `healthy` and skip to step 7.

If ANY anomaly is detected: record the details. For MVP, do not call Haiku -- just log the anomaly with enough detail to diagnose:

```bash
# Example anomaly log (adjust fields to match actual findings)
echo "{\"timestamp\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\",\"type\":\"health-anomaly\",\"role\":\"$ROLE\",\"anomaly\":\"$ANOMALY_TYPE\",\"cmd\":\"$CMD\",\"detail\":\"$DETAIL\"}" >> "$PWD/state/checkpoints.log"
```

If `RELAY_ADMIN_ALERT_HOOK` is set, invoke it:

```bash
if [ -n "$RELAY_ADMIN_ALERT_HOOK" ]; then
  $RELAY_ADMIN_ALERT_HOOK "health-check anomaly: $ROLE $ANOMALY_TYPE"
fi
```

### 6. Auto-recover CX if dead

If CX fails both the process-alive check (bare shell, no codex process) AND the bare-prompt check, attempt automatic recovery. Log the action before invoking restart:

```bash
echo "{\"timestamp\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\",\"type\":\"health-anomaly\",\"role\":\"cx\",\"anomaly\":\"process_dead\",\"cmd\":\"$CX_CMD\",\"detail\":\"auto-restart triggered\"}" >> "$PWD/state/checkpoints.log"

# Invoke the restart-cx skill
/restart-cx
```

Do NOT auto-recover OC or CC — only log warnings for those and include in the ACK. OC/CC recovery requires human judgment.

### 7. Send ACK

Determine overall status: `healthy` if no anomalies, `anomaly_detected` otherwise. Build a brief summary.

```bash
STATUS="healthy"  # or "anomaly_detected"
DETAIL="all agents responsive"  # or "cx: process dead, restart triggered"
relay send oc "ACK health-check complete. Status: ${STATUS}. Details: ${DETAIL}"
```

### 8. Log completion

```bash
echo "{\"timestamp\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\",\"type\":\"health-check\",\"results\":{\"oc\":\"$OC_STATUS\",\"cc\":\"$CC_STATUS\",\"cx\":\"$CX_STATUS\"},\"status\":\"complete\"}" >> "$PWD/state/checkpoints.log"
```

## Rules

- Minimize context usage -- this skill runs frequently (every few minutes).
- Do NOT include captured pane output in your response. Only include the diagnosis.
- Heuristic checks first; LLM-based assessment is Phase 2 (not yet implemented).
- Do NOT send messages to any agent other than OC (via the ACK in step 7).
- Return silently after sending the ACK. No follow-up output.
- Keep total output under 5 lines.
