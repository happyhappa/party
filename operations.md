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
party [session-name]
```

Default session name is `party`. The script:
1. Creates 3-pane tmux layout (OC | CC / CX)
2. Sets `AGENT_ROLE` environment variable per pane
3. Writes pane map to `~/llm-share/relay/state/panes.json`
4. Launches agents with configured commands

### Custom Configuration
Create `~/.config/relay/party.conf`:
```bash
# Working directories
RELAY_OC_DIR="$HOME/projects"
RELAY_CC_DIR="$HOME/current"
RELAY_CX_DIR="$HOME/current"

# Launch commands
RELAY_OC_CMD="claude -c --dangerously-skip-permissions"
RELAY_CC_CMD="claude --dangerously-skip-permissions"
RELAY_CX_CMD="codex -a never -s workspace-write --add-dir /tmp --add-dir ~/llm-share"
```

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
# Should output: oc, cc, or cx
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
# Regenerate pane map
OC_PANE=$(tmux display-message -p -t party:main.0 '#{pane_id}')
CC_PANE=$(tmux display-message -p -t party:main.1 '#{pane_id}')
CX_PANE=$(tmux display-message -p -t party:main.2 '#{pane_id}')
printf '{"oc":"%s","cc":"%s","cx":"%s"}\n' "$OC_PANE" "$CC_PANE" "$CX_PANE" > ~/llm-share/relay/state/panes.json

# Restart daemon
systemctl --user restart relay-daemon
```

### Permission Errors
```bash
# Fix outbox permissions
chmod 755 ~/llm-share/relay/outbox/{oc,cc,cx}

# Fix state file permissions
chmod 644 ~/llm-share/relay/state/*.json
```

---

## Quick Reference

| Command | Description |
|---------|-------------|
| `party` | Start/attach to party session |
| `relay send <to> "msg"` | Send message to agent |
| `systemctl --user start relay-daemon` | Start daemon |
| `systemctl --user status relay-daemon` | Check daemon status |
| `journalctl --user -u relay-daemon -f` | Follow daemon logs |
| `cat ~/llm-share/relay/state/panes.json` | View pane mapping |
