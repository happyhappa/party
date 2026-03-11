#!/usr/bin/env bash
# Claude Code status line — mirrors p10k classic segments: dir, git, model, context
# Phase 2a: adds sidecar telemetry write for relay-daemon consumption

input=$(cat)

cwd=$(echo "$input" | jq -r '.workspace.current_dir // .cwd // empty')
model=$(echo "$input" | jq -r '.model.display_name // empty')
model_id=$(echo "$input" | jq -r '.model.id // empty')
used=$(echo "$input" | jq -r '.context_window.used_percentage // empty')
session_id=$(echo "$input" | jq -r '.session_id // empty')
cost=$(echo "$input" | jq -r '.cost.total_cost_usd // empty')
duration=$(echo "$input" | jq -r '.cost.total_duration_ms // empty')
tokens_in=$(echo "$input" | jq -r '.context_window.total_input_tokens // empty')
tokens_out=$(echo "$input" | jq -r '.context_window.total_output_tokens // empty')
lines_added=$(echo "$input" | jq -r '.cost.total_lines_added // empty')
lines_removed=$(echo "$input" | jq -r '.cost.total_lines_removed // empty')

# Shorten home directory to ~
home="$HOME"
short_cwd="${cwd/#$home/\~}"

# Git branch (skip locks, silent on failure)
git_branch=""
if git -C "$cwd" rev-parse --git-dir >/dev/null 2>&1; then
  git_branch=$(git -C "$cwd" -c core.fsmonitor= symbolic-ref --short HEAD 2>/dev/null \
               || git -C "$cwd" rev-parse --short HEAD 2>/dev/null)
fi

# Build context indicator
ctx_part=""
if [ -n "$used" ]; then
  used_int=${used%.*}
  ctx_part=" | ctx:${used_int}%"
fi

# Build git part
git_part=""
if [ -n "$git_branch" ]; then
  git_part=" | ${git_branch}"
fi

# Build model part
model_part=""
if [ -n "$model" ]; then
  model_part=" | ${model}"
fi

# Role prefix for party sessions
role_part=""
if [ -n "${AGENT_ROLE:-}" ]; then
  role_part="\033[1;35m${AGENT_ROLE}\033[0m | "
fi

printf "${role_part}\033[34m%s\033[0m\033[32m%s\033[0m\033[33m%s\033[0m\033[36m%s\033[0m" \
  "${short_cwd}" \
  "${git_part}" \
  "${model_part}" \
  "${ctx_part}"

# --- Sidecar telemetry (what relay-daemon reads) ---
if [ -n "${RELAY_STATE_DIR:-}" ] && [ -n "${AGENT_ROLE:-}" ]; then
  (
    sidecar="$RELAY_STATE_DIR/telemetry-${AGENT_ROLE}.json"
    tmp=$(mktemp "${sidecar}.XXXXXX" 2>/dev/null) || exit 0
    # Use jq for correct JSON typing (null vs "string" vs number)
    jq -n --compact-output \
      --arg role "$AGENT_ROLE" \
      --arg ts "$(date +%s)" \
      --arg ctx "${used:-}" \
      --arg mid "${model_id:-}" \
      --arg mdp "${model:-}" \
      --arg sid "${session_id:-}" \
      --arg cost "${cost:-}" \
      --arg dur "${duration:-}" \
      --arg tin "${tokens_in:-}" \
      --arg tout "${tokens_out:-}" \
      --arg ladd "${lines_added:-}" \
      --arg lrem "${lines_removed:-}" \
      '{
        role: $role,
        timestamp: ($ts | tonumber),
        context_pct: (if $ctx == "" then null else ($ctx | tonumber) end),
        model_id: (if $mid == "" then null else $mid end),
        model_display: (if $mdp == "" then null else $mdp end),
        session_id: (if $sid == "" then null else $sid end),
        cost_usd: (if $cost == "" then null else ($cost | tonumber) end),
        duration_ms: (if $dur == "" then null else ($dur | tonumber) end),
        tokens_in: (if $tin == "" then null else ($tin | tonumber) end),
        tokens_out: (if $tout == "" then null else ($tout | tonumber) end),
        lines_added: (if $ladd == "" then null else ($ladd | tonumber) end),
        lines_removed: (if $lrem == "" then null else ($lrem | tonumber) end)
      }' > "$tmp" 2>/dev/null || { rm -f "$tmp"; exit 0; }
    mv "$tmp" "$sidecar" 2>/dev/null || rm -f "$tmp"
  ) &
fi
