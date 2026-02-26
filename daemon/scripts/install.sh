#!/usr/bin/env bash
#
# install.sh - Install LLM Relay Daemon and supporting scripts
#
# Usage: ./install.sh [--no-commands] [--no-systemd]
#

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
REPO_ROOT="$(dirname "$PROJECT_DIR")"

# Guard: must be run from the main checkout, never from a worktree
# The main checkout is expected at <project>/main
MAIN_CHECKOUT="$REPO_ROOT"
if [[ "$(basename "$MAIN_CHECKOUT")" != "main" ]]; then
    echo "[install] ERROR: must be run from the main checkout (expected .../main/daemon/scripts/)." >&2
    echo "[install] ERROR: You are in: $MAIN_CHECKOUT" >&2
    echo "[install] ERROR: Running install.sh from a worktree publishes the wrong infra. Aborting." >&2
    exit 1
fi

# Configuration
CLAUDE_COMMANDS="$HOME/.claude/commands"
BIN_DIR="$HOME/.local/bin"

# Colors
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
RED='\033[0;31m'
BLUE='\033[0;34m'
NC='\033[0m'

log() { echo -e "${GREEN}[install]${NC} $1"; }
warn() { echo -e "${YELLOW}[install]${NC} $1"; }
err() { echo -e "${RED}[install]${NC} $1" >&2; }
info() { echo -e "${BLUE}[install]${NC} $1"; }

# Parse args
INSTALL_COMMANDS=true
INSTALL_SYSTEMD=true

for arg in "$@"; do
    case $arg in
        --no-commands) INSTALL_COMMANDS=false ;;
        --no-systemd) INSTALL_SYSTEMD=false ;;
        --help|-h)
            echo "Usage: $0 [--no-commands] [--no-systemd]"
            exit 0
            ;;
    esac
done

log "Installing LLM Relay Daemon"
echo ""

# 1. Create bin directory (relay directories are created per-project by bin/party)
info "Creating bin directory..."
mkdir -p "$BIN_DIR"

log "  ✓ Bin directory created"

# 2. Build relay daemon
info "Building relay daemon..."
cd "$PROJECT_DIR"
if command -v go &> /dev/null; then
    GOCACHE=/tmp/go-build-cache go build -o "$BIN_DIR/relay-daemon" ./cmd/relay
    log "  ✓ Relay daemon built: $BIN_DIR/relay-daemon"
    # Restart systemd service if it's already running
    if systemctl --user is-active relay-daemon &>/dev/null; then
        systemctl --user restart relay-daemon
        log "  ✓ Relay daemon restarted"
    fi
else
    warn "  ⚠ Go not found, skipping daemon build"
fi

# 3. Install scripts
info "Installing scripts..."
ln -sf "$MAIN_CHECKOUT/bin/party" "$BIN_DIR/party"
ln -sf "$MAIN_CHECKOUT/bin/party-stop" "$BIN_DIR/party-stop"
[[ -f "$SCRIPT_DIR/s3-sync" ]] && ln -sf "$SCRIPT_DIR/s3-sync" "$BIN_DIR/s3-sync" || true
log "  ✓ Scripts symlinked in $BIN_DIR (pointing to $MAIN_CHECKOUT)"

# 3a. Deploy wrapper scripts
for script in tmux-inject; do
    if [[ -f "$SCRIPT_DIR/$script" ]]; then
        chmod +x "$SCRIPT_DIR/$script"
        ln -sf "$SCRIPT_DIR/$script" "$BIN_DIR/$script"
        log "  ✓ $script symlinked in $BIN_DIR"
    else
        warn "  ⚠ $script not found in $SCRIPT_DIR, skipping"
    fi
done

# 3c. Deploy relay CLI and wrappers
ln -sf "$MAIN_CHECKOUT/bin/relay" "$BIN_DIR/relay"
for script in relay-cx; do
    if [[ -f "$SCRIPT_DIR/$script" ]]; then
        chmod +x "$SCRIPT_DIR/$script"
        ln -sf "$SCRIPT_DIR/$script" "$BIN_DIR/$script"
        log "  ✓ $script symlinked in $BIN_DIR"
    else
        warn "  ⚠ $script not found in $SCRIPT_DIR, skipping"
    fi
done

# 3d. Deploy pre-compact support scripts
ln -sf "$SCRIPT_DIR/party-jsonl-filter" "$BIN_DIR/party-jsonl-filter"
ln -sf "$SCRIPT_DIR/party-brief-prompt.txt" "$BIN_DIR/party-brief-prompt.txt"
log "  ✓ Pre-compact support scripts symlinked"

# 3d2. Deploy pre-compact hook
CLAUDE_HOOKS_DIR="$HOME/.claude/hooks"
mkdir -p "$CLAUDE_HOOKS_DIR"
HOOKS_DIR="$(dirname "$SCRIPT_DIR")/hooks"
if [[ -f "$HOOKS_DIR/pre-compact.sh" ]]; then
    chmod +x "$HOOKS_DIR/pre-compact.sh"
    ln -sf "$HOOKS_DIR/pre-compact.sh" "$CLAUDE_HOOKS_DIR/pre-compact.sh"
    log "  ✓ pre-compact.sh symlinked to $CLAUDE_HOOKS_DIR"
else
    warn "  ⚠ pre-compact.sh not found in $HOOKS_DIR, skipping"
fi

# 3e. Deploy admin loop scripts
ADMIN_SCRIPT_DIR="$SCRIPT_DIR/admin"
for script in admin-watchdog.sh admin-health-check.sh admin-restart-cx.sh admin-register-panes.sh; do
    if [[ -f "$ADMIN_SCRIPT_DIR/$script" ]]; then
        chmod +x "$ADMIN_SCRIPT_DIR/$script"
        ln -sf "$ADMIN_SCRIPT_DIR/$script" "$BIN_DIR/$script"
        log "  ✓ $script symlinked in $BIN_DIR"
    else
        warn "  ⚠ $script not found in $ADMIN_SCRIPT_DIR, skipping"
    fi
done

# 4. Install Claude commands
if [[ "$INSTALL_COMMANDS" == "true" ]]; then
    info "Installing Claude commands..."
    mkdir -p "$CLAUDE_COMMANDS"

    # Backup existing commands
    for cmd in rec pc attack; do
        if [[ -f "$CLAUDE_COMMANDS/$cmd.md" ]]; then
            cp "$CLAUDE_COMMANDS/$cmd.md" "$CLAUDE_COMMANDS/$cmd.md.bak"
            warn "  Backed up existing $cmd.md → $cmd.md.bak"
        fi
    done

    # Install new commands
    cp "$PROJECT_DIR/claude-commands/rec.md" "$CLAUDE_COMMANDS/rec.md"
    cp "$PROJECT_DIR/claude-commands/pc.md" "$CLAUDE_COMMANDS/pc.md"
    cp "$PROJECT_DIR/claude-commands/attack.md" "$CLAUDE_COMMANDS/attack.md"
    log "  ✓ Claude commands installed"
else
    info "Skipping Claude commands (--no-commands)"
fi

# 5. Create systemd user service
if [[ "$INSTALL_SYSTEMD" == "true" ]]; then
    info "Creating systemd user service..."
    mkdir -p "$HOME/.config/systemd/user"

    cat > "$HOME/.config/systemd/user/relay-daemon.service" << EOF
[Unit]
Description=LLM Relay Daemon
After=network.target

[Service]
Type=simple
ExecStart=$BIN_DIR/relay-daemon
Restart=on-failure
RestartSec=5
Environment=HOME=$HOME

[Install]
WantedBy=default.target
EOF

    cat > "$HOME/.config/systemd/user/s3-sync.service" << EOF
[Unit]
Description=LLM S3 Sync Daemon
After=network.target

[Service]
Type=simple
ExecStart=$BIN_DIR/s3-sync --daemon
Restart=on-failure
RestartSec=5
Environment=HOME=$HOME

[Install]
WantedBy=default.target
EOF

    systemctl --user daemon-reload
    log "  ✓ Systemd services created"
    info "  To enable: systemctl --user enable relay-daemon s3-sync"
    info "  To start:  systemctl --user start relay-daemon s3-sync"
else
    info "Skipping systemd setup (--no-systemd)"
fi

# 6. Add bin to PATH if needed
if [[ ":$PATH:" != *":$BIN_DIR:"* ]]; then
    warn "  ⚠ $BIN_DIR not in PATH"
    info "  Add to your shell profile: export PATH=\"\$PATH:$BIN_DIR\""
fi

# 7. Verify no standalone drift
info "Verifying symlinks..."
DRIFT=0
for script in party party-stop relay tmux-inject s3-sync relay-cx party-jsonl-filter party-brief-prompt.txt admin-watchdog.sh admin-health-check.sh admin-restart-cx.sh admin-register-panes.sh; do
    target="$BIN_DIR/$script"
    if [[ -f "$target" && ! -L "$target" ]]; then
        warn "  DRIFT: $target is a standalone copy, not a symlink"
        DRIFT=1
    fi
done
if [[ $DRIFT -eq 1 ]]; then
    err "  Run install.sh again to fix drifted scripts"
else
    log "  ✓ All scripts are symlinks — no drift detected"
fi

echo ""
log "Installation complete!"
echo ""
info "Next steps:"
info "  1. Start tmux layout:    party"
info "  2. Start relay daemon:   relay-daemon (or systemctl --user start relay-daemon)"
info "  3. Start S3 sync:        s3-sync --daemon (or systemctl --user start s3-sync)"
echo ""
info "Documentation:"
info "  - Protocol:  $PROJECT_DIR/docs/AGENT_PROTOCOL.md"
info "  - Design:    $(dirname "$PROJECT_DIR")/INSTALL_PLAN.md"
