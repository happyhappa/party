Restart the CX (Codex) agent in its tmux pane. Use this when CX is unresponsive, stuck, or needs a fresh session.

## Steps

### 1. Read CX Pane ID
```bash
CX_PANE=$(jq -r '.panes.cx' ~/llm-share/relay/state/panes.json)
echo "CX pane: $CX_PANE"

# Guard: abort if CX pane is not registered
if [[ -z "$CX_PANE" || "$CX_PANE" == "null" ]]; then
  echo "ERROR: CX pane not registered. Run /register-panes first."
  relay send oc "ACK restart-cx FAILED: CX pane not registered"
  exit 1
fi
```

### 2. Determine Project Directory
Admin runs from `~/party/{project}/admin`, so the project directory is the parent:
```bash
PROJECT_DIR=$(dirname "$PWD")
PROJECT_NAME=$(basename "$PROJECT_DIR")
echo "Project: $PROJECT_NAME (dir: $PROJECT_DIR)"
```

### 3. Send Ctrl-C to Kill Current Process
```bash
tmux send-keys -t "$CX_PANE" C-c
```

### 4. Wait for Shell Prompt
Poll every 2 seconds, up to 10 seconds, for a shell prompt to appear:
```bash
PROMPT_FOUND=false
for i in $(seq 1 5); do
  sleep 2
  LAST_LINE=$(tmux capture-pane -t "$CX_PANE" -p -S -1 | tail -1)
  if echo "$LAST_LINE" | grep -qE '[$#>❯›⏵%]'; then
    PROMPT_FOUND=true
    echo "Shell prompt detected after $((i * 2))s"
    break
  fi
done
```

If `PROMPT_FOUND` is still false after 10 seconds, send another C-c and retry the same polling loop once:
```bash
if [[ "$PROMPT_FOUND" != "true" ]]; then
  echo "No prompt detected after 10s, sending another C-c..."
  tmux send-keys -t "$CX_PANE" C-c
  for i in $(seq 1 5); do
    sleep 2
    LAST_LINE=$(tmux capture-pane -t "$CX_PANE" -p -S -1 | tail -1)
    if echo "$LAST_LINE" | grep -qE '[$#>❯›⏵%]'; then
      PROMPT_FOUND=true
      echo "Shell prompt detected on retry after $((i * 2))s"
      break
    fi
  done
fi

if [[ "$PROMPT_FOUND" != "true" ]]; then
  echo "WARNING: Shell prompt not detected after retry. Proceeding anyway."
fi
```

### 5. Relaunch CX
Send the launch command to the CX pane:
```bash
tmux send-keys -t "$CX_PANE" "cd ~/party/${PROJECT_NAME}/cx && export AGENT_ROLE=cx && codex -a never -s workspace-write --add-dir /tmp --add-dir ~/llm-share --add-dir ~/.cache" Enter
```

### 6. Log the Restart
Append a structured log entry to checkpoints.log:
```bash
mkdir -p "$PWD/state"
echo "{\"timestamp\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\",\"type\":\"restart-cx\",\"pane\":\"$CX_PANE\",\"status\":\"relaunched\"}" >> "$PWD/state/checkpoints.log"
```

### 7. Notify OC
Send a notification so OC knows CX has been restarted:
```bash
relay send oc "CX restarted in pane $CX_PANE"
```

## Rules

- Wait for a clean exit before relaunching (poll for shell prompt)
- If prompt doesn't appear after 10s, send another C-c and try again (one retry only)
- Log all actions to checkpoints.log
- Return silently after the notification -- do not elaborate or send additional messages
