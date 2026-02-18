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

If `CMD` is `bash`, `zsh`, or another bare shell, the agent has **possibly** crashed — but check the Codex footer override (step 3a-cx) before concluding.

**a-cx) Codex footer override (CX only):**
Codex CLI shows a distinctive footer in its idle state. Check if `TAIL` contains `context left` or `? for shortcuts`. If either is present, CX is alive regardless of what `CMD` reports — mark CX as `healthy` and skip remaining checks for CX.

```bash
if [[ "$ROLE" == "cx" ]]; then
  if echo "$TAIL" | grep -qE '(context left|\? for shortcuts)'; then
    # Codex is running — its footer is visible
    STATUS="healthy"
    # skip remaining checks for cx
    continue
  fi
fi
```

**b) Error pattern scan:**
Check `TAIL` for repeated occurrences of: `error`, `panic`, `FATAL`, `killed`, `Traceback`, `SIGTERM`, `SIGKILL`, `OOM`.
A single occurrence may be benign; three or more of the same pattern indicates a problem.

**c) Bare prompt detection:**
If the last non-empty line of `TAIL` matches a shell prompt pattern (`$`, `%`, `>`, or contains the hostname) AND `CMD` is a shell, the agent is not running. Note: the Codex prompt `›` is NOT a shell prompt — it is handled by step 3a-cx above.

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

### 6b. Auto-compact CX if context low

If CX is healthy (passed step 3a-cx), check its context level. Parse the context percentage from the CX pane tail:

```bash
CX_CONTEXT=$(echo "$CX_TAIL" | grep -oP '\d+(?=% context left)' | tail -1)
CX_IDLE=$(echo "$CX_TAIL" | grep -q '? for shortcuts' && echo "true" || echo "false")
```

If `CX_CONTEXT` is a number, `CX_CONTEXT <= 60`, AND `CX_IDLE == "true"`: inject `/compact` into the CX pane to trigger context compaction.

```bash
if [[ -n "$CX_CONTEXT" && "$CX_CONTEXT" -le 60 && "$CX_IDLE" == "true" ]]; then
  CX_PANE=$(echo "$PANES_JSON" | jq -r '.panes.cx')
  tmux send-keys -t "$CX_PANE" "/compact" Enter
  echo "{\"timestamp\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\",\"type\":\"health-action\",\"role\":\"cx\",\"action\":\"auto_compact\",\"context_pct\":$CX_CONTEXT}" >> "$PWD/state/checkpoints.log"
fi
```

Do NOT restart CX for low context — compact is non-destructive. CX processes it when ready, session persists, pane and routing stay intact.

### 7. Log completion

```bash
echo "{\"timestamp\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\",\"type\":\"health-check\",\"results\":{\"oc\":\"$OC_STATUS\",\"cc\":\"$CC_STATUS\",\"cx\":\"$CX_STATUS\"},\"status\":\"complete\"}" >> "$PWD/state/checkpoints.log"
```

## Rules

- Minimize context usage -- this skill runs frequently (every few minutes).
- Do NOT include captured pane output in your response. Only include the diagnosis.
- Heuristic checks first; LLM-based assessment is Phase 2 (not yet implemented).
- Do NOT send relay messages to any agent. All output stays local (JSONL log only).
- Return silently after logging. No follow-up output.
- Keep total output under 5 lines.
