# Operations Guide: Party (Multi-Agent LLM Orchestration)

## 1. Prerequisites

### Required Software
- **Go 1.21+** - For building the relay daemon
- **tmux 2.6+** - For pane management (requires `pane-border-status` support)
- **systemd** - For service management (user services)

### Verify Prerequisites
```bash
go version          # go1.21+ required
tmux -V             # tmux 2.6+ required
systemctl --user --version  # systemd with user services
```

### Optional
- **AWS CLI** - For S3 sync functionality
- **Claude CLI** - `claude` command for OC/CC agents
- **Codex CLI** - `codex` command for CX agent

---

## 2. Build Instructions

### Build Relay Daemon
```bash
cd ~/Sandbox/personal/party/daemon
go build -o ~/.local/bin/relay-daemon ./cmd/relay
```

### Build with Custom Cache (CI/Docker)
```bash
GOCACHE=/tmp/go-build-cache go build -o ~/.local/bin/relay-daemon ./cmd/relay
```

### Run Tests
```bash
cd ~/Sandbox/personal/party/daemon
go test ./...
```

---

## 3. Install Steps

### Option A: Automated Install
```bash
cd ~/Sandbox/personal/party/daemon/scripts
./install.sh
```

Options:
- `--no-commands` - Skip Claude command installation
- `--no-systemd` - Skip systemd service creation

### Option B: Manual Install

#### 3.1 Create Directory Structure
```bash
mkdir -p ~/llm-share/{relay/{outbox/{oc,cc,cx},processed,log,state/locks},attacks,recovery,reviews,shared/{runbooks,docs}}
mkdir -p ~/.local/bin
```

#### 3.2 Initialize State Files
```bash
echo '{}' > ~/llm-share/relay/processed/offsets.json
echo '{}' > ~/llm-share/relay/state/agents.json
touch ~/llm-share/relay/state/{checkpoint.json,handoff-marker,health.json}
```

#### 3.3 Install CLI Tools
```bash
cp ~/Sandbox/personal/party/bin/relay ~/.local/bin/relay
cp ~/Sandbox/personal/party/bin/party ~/.local/bin/party
chmod +x ~/.local/bin/{relay,party}
```

#### 3.4 Install Systemd Services
```bash
mkdir -p ~/.config/systemd/user

cat > ~/.config/systemd/user/relay-daemon.service << 'EOF'
[Unit]
Description=LLM Relay Daemon
After=network.target

[Service]
Type=simple
ExecStart=%h/.local/bin/relay-daemon
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
EOF

systemctl --user daemon-reload
systemctl --user enable relay-daemon
```

#### 3.5 Add to PATH
Add to `~/.bashrc` or `~/.zshrc`:
```bash
export PATH="$PATH:$HOME/.local/bin"
```

---

## 4. Startup Procedure

### Start Relay Daemon
```bash
# Via systemd (recommended)
systemctl --user start relay-daemon

# Or manually
relay-daemon
```

### Start Party Session
```bash
party-v2 [session-name] [project-dir]
```

Default session name is `party`. The script:
1. Creates 4-pane tmux layout: OC (40% left) | CC (top right 50%) / Admin (bottom right 50%) + hidden CX window
2. Creates per-pane directories under `~/party/{project}/{oc,cc,admin,cx}/`
3. Installs admin skills from `daemon/admin-skills/` into `admin/.claude/commands/`
4. Sets `AGENT_ROLE` environment variable per pane (oc, cc, admin, cx)
5. Writes pane map (v2 format) to `~/llm-share/relay/state/panes.json`
6. Auto-launches Admin agent with `claude -c --dangerously-skip-permissions`

### Pane Layout
```
┌────────────┬────────────────────┐
│            │     CC (50%)       │
│  OC (40%)  ├────────────────────┤
│            │   Admin (50%)      │
└────────────┴────────────────────┘
+ hidden tmux window "cx" for CX agent
```

### Admin Pane
The Admin agent handles orchestration tasks injected by the relay daemon:
- **`/checkpoint-cycle`** — Dispatches coordinated checkpoints to OC, CC, CX (every 10m)
- **`/health-check`** — Heuristic health monitoring of all agent panes (every 5m)
- **`/register-panes`** — Discovers and writes tmux pane map
- **`/restart-cx`** — Restarts CX agent if detected dead
- **`/status`** — Quick system status summary

Admin is recycled by the relay daemon via Prestige (capture last-life.txt, /exit, relaunch) after a configurable number of cycles or max uptime.

### Admin Environment Variables
| Variable | Default | Description |
|----------|---------|-------------|
| `RELAY_CHECKPOINT_INTERVAL` | `10m` | Checkpoint cycle frequency |
| `RELAY_HEALTH_CHECK_INTERVAL` | `5m` | Health check frequency |
| `RELAY_ADMIN_RECYCLE_AFTER_CYCLES` | `6` | Recycle admin after N checkpoint cycles |
| `RELAY_ADMIN_MAX_UPTIME` | `2h` | Max admin uptime before forced recycle |
| `RELAY_ADMIN_ALERT_HOOK` | (empty) | Shell command to invoke on anomaly |
| `RELAY_ADMIN_RELAUNCH_CMD` | `claude --dangerously-skip-permissions` | Command to relaunch admin after recycle |

---

## 5. Verification Steps

### Check Daemon Status
```bash
systemctl --user status relay-daemon
journalctl --user -u relay-daemon -f
```

### Check Pane Mapping
```bash
cat ~/llm-share/relay/state/panes.json
```

### Test Message Sending
```bash
# From any terminal with AGENT_ROLE set
export AGENT_ROLE=oc
relay send cc "Test message from OC"

# Check outbox
ls -la ~/llm-share/relay/outbox/oc/
cat ~/llm-share/relay/outbox/oc/*.msg
```

### Verify Agent Role Detection
In each agent pane:
```bash
echo $AGENT_ROLE
# Should output: oc, cc, admin, or cx
```

### Check Event Log
```bash
tail -f ~/llm-share/relay/log/events.jsonl
```

---

## 6. Troubleshooting

### Daemon Won't Start
```bash
# Check logs
journalctl --user -u relay-daemon --no-pager -n 50

# Check if port/socket in use
lsof -i :8080  # if using HTTP

# Restart
systemctl --user restart relay-daemon
```

### Messages Not Delivered
1. **Check outbox directory exists:**
   ```bash
   ls -la ~/llm-share/relay/outbox/{oc,cc,cx}/
   ```

2. **Check pane mapping:**
   ```bash
   cat ~/llm-share/relay/state/panes.json
   # Verify pane IDs match current tmux session
   tmux list-panes -a -F "#{pane_id}: #{pane_title}"
   ```

3. **Check daemon is watching correct directory:**
   ```bash
   journalctl --user -u relay-daemon | grep -i watch
   ```

### Agent Role Not Set
```bash
# In agent pane, verify AGENT_ROLE
echo $AGENT_ROLE

# If empty, re-export
export AGENT_ROLE=cc  # or oc, cx
```

### Tmux Pane Titles Overwritten
Claude Code CLI overwrites pane titles. The system uses pane index (0=OC, 1=CC, 2=CX) and `@role` pane option as backup:
```bash
tmux display-message -p -t %0 '#{@role}'  # Should show: oc
```

### Stale Pane Mapping
If tmux session was recreated without updating panes.json:
```bash
# In the admin pane, run:
/register-panes

# Or manually regenerate:
OC_PANE=$(tmux display-message -p -t party:main.0 '#{pane_id}')
CC_PANE=$(tmux display-message -p -t party:main.1 '#{pane_id}')
ADMIN_PANE=$(tmux display-message -p -t party:main.2 '#{pane_id}')
CX_PANE=$(tmux list-panes -t party:cx -F '#{pane_id}' | head -1)

jq -n --arg oc "$OC_PANE" --arg cc "$CC_PANE" --arg admin "$ADMIN_PANE" --arg cx "$CX_PANE" \
  '{"panes":{"oc":$oc,"cc":$cc,"admin":$admin,"cx":$cx},"version":1,"registered_at":now|strftime("%Y-%m-%dT%H:%M:%SZ")}' \
  > ~/llm-share/relay/state/panes.json

# Restart daemon
systemctl --user restart relay-daemon
```

### Permission Errors
```bash
# Fix outbox permissions
chmod 755 ~/llm-share/relay/outbox/{oc,cc,cx,admin}

# Fix state file permissions
chmod 644 ~/llm-share/relay/state/*.json
```

### Admin Not Receiving Skills
If admin pane doesn't respond to `/checkpoint-cycle` or `/health-check`:
1. Check admin skills are installed: `ls ~/party/{project}/admin/.claude/commands/`
2. Check relay daemon is routing to admin: `journalctl --user -u relay-daemon | grep admin`
3. Verify panes.json has admin pane: `jq .panes.admin ~/llm-share/relay/state/panes.json`
4. Re-register panes if needed: run `/register-panes` in admin pane

---

## Quick Reference

| Command | Description |
|---------|-------------|
| `party-v2 [session] [project-dir]` | Start/attach to party session (4-pane layout) |
| `relay send <to> "msg"` | Send message to agent (oc, cc, cx, admin, all) |
| `systemctl --user start relay-daemon` | Start daemon |
| `systemctl --user status relay-daemon` | Check daemon status |
| `journalctl --user -u relay-daemon -f` | Follow daemon logs |
| `jq . ~/llm-share/relay/state/panes.json` | View pane mapping (v2 format) |
| `/status` (in admin pane) | Quick system health summary |
| `/register-panes` (in admin pane) | Re-discover and write pane map |
