Restore context after compaction or session start. This is the relay-aware version.

## Steps

### 1. Check for active attacks
```bash
ls -t ~/llm-share/attacks/*.json 2>/dev/null | head -5
```
If attack files exist with `status: "open"` or `status: "in_progress"`, read and summarize:
- Attack ID and plan file
- Current phase and chunk
- What was being worked on

### 2. Read recovery file
```bash
# Get repo name
REPO=$(basename $(git rev-parse --show-toplevel 2>/dev/null) 2>/dev/null || basename $(pwd))

# Find most recent recovery file for this repo
ls -t /tmp/claude_recovery_${REPO}*.md 2>/dev/null | head -1
```
If found, read and summarize key context.

### 3. Check relay state
```bash
cat ~/llm-share/relay/state/agents.json
```
Report agent states if available.

### 4. Protocol reminder
Remind about relay communication protocol:

**To send messages to other agents:**
```bash
relay send <oc|cc|cx|all> "Your message here"
```

Check your role with `echo $AGENT_ROLE` (returns `oc`, `cc`, or `cx`).

**Full protocol doc:** `~/llm-share/relay/protocol-instructions.txt`

### 5. Offer next steps
Ask if the user wants to:
- Continue the active attack (if any)
- Review the recovery context
- Start something new
