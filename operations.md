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
cd daemon
go build -o ~/.local/bin/relay-daemon ./cmd/relay
```

### Build with Custom Cache (CI/Docker)
```bash
GOCACHE=/tmp/go-build-cache go build -o ~/.local/bin/relay-daemon ./cmd/relay
```

### Run Tests
```bash
cd daemon
go test ./...
```

---

## 3. Install Steps

### Option A: Automated Install
```bash
cd daemon/scripts
./install.sh
```

Options:
- `--no-commands` - Skip Claude command installation
- `--no-systemd` - Skip systemd service creation

### Option B: Manual Install

#### 3.1 Create Directory Structure
```bash
# Relay directories are per-project under <project>/.relay/
mkdir -p <project>/.relay/{state,log,inbox/{oc,cc,cx}}
mkdir -p ~/.local/bin
```

#### 3.2 Initialize State Files
```bash
echo '{}' > <project>/.relay/state/agents.json
touch <project>/.relay/state/handoff-marker
```

#### 3.3 Install CLI Tools
```bash
cp bin/relay ~/.local/bin/relay
cp bin/party ~/.local/bin/party
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
party [session-name] [project-dir]
```

Default session name is `party`. The script:
1. Creates 3-pane tmux layout: OC (50% left) | CC (top right) / CX (bottom right)
2. Sets `AGENT_ROLE` environment variable per pane (oc, cc, cx)
3. Writes pane map to `<project>/.relay/state/panes.json`
4. Starts `partyctl watchdog` for health monitoring, continuous briefs, and recycle orchestration

### Pane Layout
```
┌────────────┬────────────────────┐
│            │     CC (50%)       │
│  OC (50%)  ├────────────────────┤
│            │     CX (50%)      │
└────────────┴────────────────────┘
```

### Watchdog
The `partyctl watchdog` background process replaces the former admin loop:
- Runs health checks for all roles on a fixed tick
- Generates continuous session briefs on the configured cadence
- Triggers recycle when thresholds are exceeded
- Logs structured output to stdout or the configured watchdog log destination
- Uses state files in `<project>/.relay/state/` to coordinate recycle and brief progress

### Watchdog Environment Variables
| Variable | Default | Description |
|----------|---------|-------------|
| `RELAY_STATE_DIR` | `<project>/.relay/state` | State directory |
| `RELAY_LOG_DIR` | `<project>/.relay/log` | Log directory |
| `RELAY_SHARE_DIR` | `<project>/.relay` | Shared runtime directory |
| `RELAY_MAIN_DIR` | `<project>` | Main worktree used to build the contract |
| `RELAY_TMUX_SESSION` | `party` | tmux session name watched by `partyctl watchdog` |
| `RELAY_ADMIN_ALERT_HOOK` | (empty) | Optional shell hook to invoke on anomaly |

---

## 5. Verification Steps

### Check Daemon Status
```bash
systemctl --user status relay-daemon
journalctl --user -u relay-daemon -f
```

### Check Pane Mapping
```bash
cat <project>/.relay/state/panes.json
```

### Test Message Sending
```bash
# From any terminal with AGENT_ROLE set
export AGENT_ROLE=oc
relay send cc "Test message from OC"
```

### Verify Agent Role Detection
In each agent pane:
```bash
echo $AGENT_ROLE
# Should output: oc, cc, or cx
```

### Check Event Log
```bash
tail -f <project>/.relay/log/events.jsonl
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
1. **Check pane mapping:**
   ```bash
   cat <project>/.relay/state/panes.json
   # Verify pane IDs match current tmux session
   tmux list-panes -a -F "#{pane_id}: #{pane_title}"
   ```

2. **Check daemon is watching correct directory:**
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
# Run the register-panes script:
admin-register-panes.sh

# Restart daemon
systemctl --user restart relay-daemon
```

### Permission Errors
```bash
# Fix state file permissions
chmod 644 <project>/.relay/state/*.json
```

### Watchdog Not Running
If health checks, briefs, or recycle actions aren't firing:
1. Check the watchdog process: `pgrep -af "partyctl watchdog"`
2. Check logs for recent watchdog output in your configured log destination
3. Restart manually: `partyctl watchdog`

---

## Quick Reference

| Command | Description |
|---------|-------------|
| `party [session] [project-dir]` | Start/attach to party session (3-pane layout) |
| `relay send <to> "msg"` | Send message to agent (oc, cc, cx, all) |
| `systemctl --user start relay-daemon` | Start daemon |
| `systemctl --user status relay-daemon` | Check daemon status |
| `journalctl --user -u relay-daemon -f` | Follow daemon logs |
| `jq . <project>/.relay/state/panes.json` | View pane mapping (v2 format) |
| `admin-register-panes.sh` | Re-discover and write pane map |
| `partyctl watchdog` | Run the party lifecycle watchdog |
