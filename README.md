# Party - Multi-Agent LLM Orchestration System

A tmux-based system for running multiple LLM agents (Claude, Codex) in coordinated sessions with inter-agent communication via a relay daemon.

## Components

```
party/
├── bin/
│   ├── party                 # Main script - creates tmux session with agent panes
│   └── relay                 # CLI wrapper for sending messages
├── daemon/                   # Go source for relay-daemon
│   ├── cmd/relay/            # Main entry point
│   ├── internal/             # Core modules (inbox, tmux, state, etc.)
│   └── docs/                 # Protocol documentation
├── systemd/
│   └── relay-daemon.service  # Systemd user service
└── config/
    └── protocol-instructions.txt  # Agent protocol (injected on session start)
```

## Architecture

### Agents
- **OC (Orchestrator)** - Plans, coordinates, reviews (pane 0)
- **CC (Coder Claude)** - Implements features, fixes bugs (pane 1)
- **CX (Codex)** - Quick tasks, research, validation (pane 2)

### Message Flow
```
Agent writes JSONL → ~/llm-share/relay/outbox/{role}.jsonl
Relay daemon watches → parses → routes via tmux send-keys
Target agent receives message in their pane
```

### Message Format
```json
{"to":"cc","kind":"chat","payload":"Your message here"}
```
- `to`: recipient - "oc", "cc", "cx", or "all"
- `kind`: "chat", "command", or "ack"
- `payload`: message content

## Quick Install

```bash
# 1. Build and install daemon
cd daemon && go build -o ~/.local/bin/relay-daemon ./cmd/relay

# 2. Copy scripts
cp bin/party bin/relay ~/.local/bin/
chmod +x ~/.local/bin/party ~/.local/bin/relay

# 3. Install systemd service
cp systemd/relay-daemon.service ~/.config/systemd/user/
systemctl --user daemon-reload
systemctl --user enable --now relay-daemon

# 4. Create relay directories
mkdir -p ~/llm-share/relay/{outbox,state,log,processed}

# 5. Copy protocol instructions
cp config/protocol-instructions.txt ~/llm-share/relay/
```

## Usage

```bash
# Start a party session
party

# Send a message (from any agent)
relay send cc "Hello CC"
relay send all "Broadcast to everyone"

# Check daemon status
systemctl --user status relay-daemon
journalctl --user -u relay-daemon -f
```

## Context Capture Commands

Commands for managing agent context across sessions (RFC-002):

| Command | Description |
|---------|-------------|
| `/checkpoint` | Create a checkpoint bead capturing current session state |
| `/restore` | Restore context from checkpoint + session log tail |
| `/plan` | Create and manage implementation plans |
| `/task` | Create and manage tasklets within milestones |
| `/thread` | View tasklets grouped by thread |

### Checkpoint & Recovery

```bash
# Create a checkpoint before context compaction
/checkpoint

# Restore context after session restart
/restore
```

Checkpoints capture: current goal, key decisions, blockers, and next steps. The system also supports automatic pre-compact and session-end checkpoints via hooks.

### Plan Management

```bash
# Create a new plan
/plan create "RFC-002 Phase 5"

# List all plans
/plan

# Show plan details
/plan show <plan_id>

# Update plan status
/plan status <plan_id> active
```

### Task Management

```bash
# Create a tasklet in a milestone
/task create <plan_id> <milestone_num>

# List tasklets
/task list <plan_id>

# Update tasklet status
/task status <tasklet_id> done

# Assign tasklet to agent
/task assign <tasklet_id> cc
```

### Thread View

```bash
# View tasklets grouped by thread
/thread

# View threads for a specific plan
/thread <plan_id>
```

## Environment Variables

Agents receive `AGENT_ROLE` environment variable set by party script:
- `AGENT_ROLE=oc` for Orchestrator
- `AGENT_ROLE=cc` for Coder Claude
- `AGENT_ROLE=cx` for Codex

## License

MIT
