Start or resume a coordinated attack using the 3-tier orchestration system (OC/CC/CX).

## Overview
An "attack" is a planned, multi-phase implementation task coordinated between:
- **OC (Orchestrator Claude)** - Plans and coordinates, assigns work
- **CC (Coder Claude)** - Implementation, testing, subagents
- **CX (Codex)** - Ephemeral task worker for specific chunks

## Starting a New Attack

### 1. Create attack plan
Work with the user to define:
- Goal and scope
- Phases (sequential milestones)
- Chunks within each phase (parallelizable units)
- Assignment: which chunks go to CC vs CX

### 2. Initialize attack state
Create attack state file:
```bash
ATTACK_ID="atk-$(openssl rand -hex 4)"
cat > ~/llm-share/attacks/${ATTACK_ID}.json << 'EOF'
{
  "attack_id": "ATTACK_ID_HERE",
  "plan_file": "/path/to/plan.md",
  "status": "open",
  "started_at": "TIMESTAMP",
  "last_updated": "TIMESTAMP",
  "current_phase": 1,
  "current_chunk": 1,
  "phases": [
    {
      "phase": 1,
      "description": "Phase 1 description",
      "status": "pending",
      "chunks": [
        {"chunk": 1, "assignee": "cc", "status": "pending", "description": "..."},
        {"chunk": 2, "assignee": "cx", "status": "pending", "description": "..."}
      ]
    }
  ],
  "events": []
}
EOF
```

### 3. Assign first chunk
Send task to appropriate agent via relay:
```bash
jq -nc \
  --arg msg_id "msg-$(openssl rand -hex 4)" \
  --arg ts "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  --arg thread "ATTACK_ID" \
  --arg payload "Task assignment details here" \
  '{msg_id:$msg_id,ts:$ts,project_id:"leaseupcre",from:"oc",to:"cc",kind:"command",priority:1,thread_id:$thread,payload:$payload,ephemeral:false}' \
  >> ~/llm-share/relay/inbox/oc.jsonl
```

## Resuming an Attack

### 1. Check attack state
```bash
cat ~/llm-share/attacks/*.json | jq 'select(.status == "open" or .status == "in_progress")'
```

### 2. Identify current position
- Which phase?
- Which chunk?
- What's blocking progress?

### 3. Update state and continue
Update `last_updated` timestamp and proceed with next chunk.

## Updating Attack State

When a chunk completes:
```bash
# Update the attack file with new status
# Add event to events array
# Increment current_chunk or current_phase as needed
# Update last_updated timestamp
```

## Attack Lifecycle

```
open → in_progress → complete
                  ↘ aborted (if cancelled)
                  ↘ stalled (if stuck, relay will nag)
```

## Relay Integration

The relay daemon watches `~/llm-share/attacks/*.json`:
- If `status=open` and `last_updated` > 5min stale → sends nag
- Nags every 5min for up to 30min
- Logs all state changes to `~/llm-share/relay/log/events.jsonl`

## Files
- Attack state: `~/llm-share/attacks/{attack_id}.json`
- Relay inbox: `~/llm-share/relay/inbox/{agent}.jsonl`
- Event log: `~/llm-share/relay/log/events.jsonl`
- Protocol doc: `~/Sandbox/orchestration_communication/relay-daemon/docs/AGENT_PROTOCOL.md`
