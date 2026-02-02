# Agent Protocol: Writing to Relay Outbox

This document explains how agents (OC, CC, CX) send messages to the relay by writing JSONL to their outbox files.

---

## Outbox Location

```
~/llm-share/relay/outbox/
```

Each agent writes to their own dedicated file:
- `oc.jsonl` - Orchestrator Claude messages
- `cc.jsonl` - Claude Code messages
- `cx.jsonl` - Codex messages
- `vog.jsonl` - Voice-of-God (human/external) messages

The relay reads each file and **auto-injects**:
- `from` (based on filename)
- `msg_id`
- `ts`

---

## Simplified Message Format (Recommended)

Agents only need to provide the essentials:

```json
{
  "to": "cc",
  "kind": "chat",
  "payload": "message content"
}
```

Optional fields (if needed):
- `priority` (0=urgent, 1=normal, 2=low)
- `thread_id`
- `ephemeral`
- `project_id`

The relay fills the rest.

---

## Full Envelope (for reference)

```json
{
  "msg_id": "msg-a1b2c3d4",
  "ts": "2026-01-31T13:45:00Z",
  "project_id": "leaseupcre",
  "from": "oc",
  "to": "cc",
  "kind": "chat",
  "priority": 1,
  "thread_id": "atk-x1y2z3",
  "payload": "message content",
  "ephemeral": false
}
```

---

## Field Definitions

| Field | Required | Description |
|-------|----------|-------------|
| `to` | Yes | Target endpoint: `oc`, `cc`, `cx`, or `all` (broadcast) |
| `kind` | Yes | `chat`, `command`, `event`, `ack`, `nag` |
| `payload` | Yes | The message content |
| `priority` | No | `0`=urgent, `1`=normal, `2`=low (default `1`) |
| `thread_id` | No | Correlation ID for attack flows (e.g., `atk-abc123`) |
| `ephemeral` | No | If `true`, do not sync to S3 |
| `project_id` | No | Project context (e.g., `leaseupcre`) |
| `from` | Auto | Injected by relay based on filename |
| `msg_id` | Auto | Injected by relay (`msg-` + 8 hex chars) |
| `ts` | Auto | Injected by relay (ISO8601 UTC) |

---

## Writing Rules

1. **Always append** - Never truncate or overwrite the file
2. **One JSON object per line** - JSONL format (no pretty-printing)
3. **Atomic writes recommended** - Write to temp file, then append
4. **Include newline** - Each message must end with `\n`

---

## Examples

### Claude Code (CC) → Codex (CX)

```json
{"to":"cx","kind":"command","payload":"Please implement the PropertySearch component with filters for price range and location."}
```

### Codex (CX) → Claude Code (CC)

```json
{"to":"cc","kind":"event","thread_id":"atk-abc123","payload":"Task complete: PropertySearch component implemented with price and location filters."}
```

### Orchestrator (OC) → Claude Code (CC)

```json
{"to":"cc","kind":"command","priority":0,"payload":"New priority: Fix the database connection pooling issue."}
```

### Broadcast to All Agents

```json
{"to":"all","kind":"event","payload":"Checkpoint reached. Stand by for next instructions."}
```

---

## Bash One-Liners for Testing

### Write a Test Message (Claude Code → Codex)

```bash
echo '{"to":"cx","kind":"chat","payload":"Hello from Claude Code!"}' >> ~/llm-share/relay/outbox/cc.jsonl
```

### Write with jq (Safer, Handles Escaping)

```bash
jq -nc \
  --arg payload "Task complete: implemented feature X" \
  '{to:"cx",kind:"event",priority:1,payload:$payload}' \
  >> ~/llm-share/relay/outbox/cc.jsonl
```

### Atomic Write (Recommended for Production)

```bash
OUTBOX=~/llm-share/relay/outbox
TMPFILE=$(mktemp)
jq -nc \
  --arg payload "Your message here" \
  '{to:"cx",kind:"command",priority:1,payload:$payload}' \
  > "$TMPFILE"
cat "$TMPFILE" >> "$OUTBOX/cc.jsonl"
rm "$TMPFILE"
```

---

## Reading Messages (For Reference)

```bash
tail -5 ~/llm-share/relay/outbox/cc.jsonl | jq .
```
