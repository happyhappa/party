#!/bin/bash
# pre-compact.sh - Auto-checkpoint before compaction (RFC-002)
# Triggered before context compaction to preserve state

set -o pipefail

# Skip for Codex agents (no Claude skill support)
[[ "${AGENT_ROLE:-}" == "cx" ]] && exit 0

# Read stdin JSON (hook receives context from Claude Code)
HOOK_STDIN=""
TRANSCRIPT_PATH=""
SESSION_ID=""

HOOK_STDIN=$(cat 2>/dev/null || true)
if [[ -z "$HOOK_STDIN" ]]; then
    echo "pre-compact.sh: warning: empty stdin, skipping transcript extraction" >&2
elif ! command -v jq &>/dev/null; then
    echo "pre-compact.sh: warning: jq not found, cannot parse hook context" >&2
elif echo "$HOOK_STDIN" | jq -e . >/dev/null 2>&1; then
    TRANSCRIPT_PATH=$(echo "$HOOK_STDIN" | jq -r '.transcript_path // empty')
    SESSION_ID=$(echo "$HOOK_STDIN" | jq -r '.session_id // empty')
    if [[ -z "$TRANSCRIPT_PATH" || -z "$SESSION_ID" ]]; then
        echo "pre-compact.sh: warning: missing transcript_path or session_id in hook context" >&2
    fi
else
    echo "pre-compact.sh: warning: invalid JSON on stdin, skipping transcript extraction" >&2
fi

# Set up environment
ROLE="${AGENT_ROLE:-unknown}"
ROLE_SAFE="$(printf '%s' "${ROLE}" | tr -c '[:alnum:]._-' '_')"
[[ -z "$ROLE_SAFE" ]] && ROLE_SAFE="unknown"
REPO=$(basename "$(git rev-parse --show-toplevel 2>/dev/null)" 2>/dev/null || basename "$PWD")
export PATH="$HOME/go/bin:$HOME/.local/bin:$PATH"
# Find beads dir: use env var, then check git root
if [[ -z "${BEADS_DIR:-}" ]]; then
    GIT_ROOT=$(git rev-parse --show-toplevel 2>/dev/null || true)
    if [[ -n "$GIT_ROOT" && -d "$GIT_ROOT/.beads" ]]; then
        export BEADS_DIR="$GIT_ROOT/.beads"
    fi
fi

# CRIT-3: exclusive lock — skip if a concurrent invocation is already running for this role
# Use per-project lock if RELAY_STATE_DIR is set, fall back to /tmp
if [[ -n "${RELAY_STATE_DIR:-}" ]]; then
    LOCK_FILE="$RELAY_STATE_DIR/pre-compact-${ROLE_SAFE}.lock"
else
    LOCK_FILE="/tmp/pre-compact-${ROLE_SAFE}.lock"
fi
exec 9>"$LOCK_FILE"
if ! flock -n 9; then
    echo "pre-compact: warning: another hook invocation already running for role ${ROLE}, skipping" >&2
    exit 0
fi

# Phase 1.2: track transcript offsets across compactions (two-phase commit)
OFFSET_ROOT="${BEADS_DIR:-$HOME/.local/share/party}"
OFFSET_FILE="${OFFSET_ROOT}/last_compact_offset_${ROLE}"
LAST_OFFSET="0"

if [[ -f "$OFFSET_FILE" ]]; then
    STORED_OFFSET="$(sed -n '1p' "$OFFSET_FILE" 2>/dev/null | tr -d '[:space:]')"
    if [[ "$STORED_OFFSET" =~ ^[0-9]+$ ]]; then
        LAST_OFFSET="$STORED_OFFSET"
    else
        echo "pre-compact: warning: offset reset (corrupt file: $OFFSET_FILE)" >&2
    fi
fi

# Compute current transcript byte size if transcript_path was parsed in Phase 1.1.
CURRENT_SIZE="0"
if [[ -n "$TRANSCRIPT_PATH" && -f "$TRANSCRIPT_PATH" ]]; then
    CURRENT_SIZE="$(stat -c %s "$TRANSCRIPT_PATH" 2>/dev/null || echo 0)"
elif [[ -n "$TRANSCRIPT_PATH" && ! -f "$TRANSCRIPT_PATH" ]]; then
    echo "pre-compact: warning: transcript not found: $TRANSCRIPT_PATH" >&2
fi

if ! [[ "$CURRENT_SIZE" =~ ^[0-9]+$ ]]; then
    CURRENT_SIZE="0"
fi
if (( LAST_OFFSET > CURRENT_SIZE )); then
    LAST_OFFSET="$CURRENT_SIZE"
fi

# Expose values for slicing + worker launch in later chunks.
export OFFSET_FILE LAST_OFFSET CURRENT_SIZE TRANSCRIPT_PATH SESSION_ID

commit_compact_offset() {
    # Two-phase semantics: call only after worker launch succeeds.
    # Uses mktemp to avoid race with concurrent compactions.
    local next_offset="$1"
    mkdir -p "$OFFSET_ROOT" 2>/dev/null || return 1
    local tmp_file
    umask 077
    tmp_file=$(mktemp "${OFFSET_FILE}.XXXXXX") || return 1
    {
        printf '%s\n' "$next_offset"
        date -Iseconds
    } > "$tmp_file" 2>/dev/null || { rm -f "$tmp_file"; return 1; }
    mv "$tmp_file" "$OFFSET_FILE"
}

SESSION_ID_SAFE="$(printf '%s' "${SESSION_ID:-unknown}" | tr -c '[:alnum:]._-' '_')"
[[ -z "$SESSION_ID_SAFE" ]] && SESSION_ID_SAFE="unknown"
LOG_FILE="/tmp/session-brief-${ROLE}-${SESSION_ID_SAFE}.log"
BRIEF_PROMPT_FILE="$HOME/.local/bin/party-brief-prompt.txt"
CHECKPOINT_BEAD_ID=""

launch_session_brief_worker() {
    local filtered_path="$1"
    local checkpoint_bead_id="$2"
    local tmp_id
    local worker_script
    local marker_file

    tmp_id="$(date +%s)-$(head -c 4 /dev/urandom | xxd -p)"
    worker_script="/tmp/session-brief-worker-${tmp_id}.sh"
    marker_file="/tmp/pre-compact-${ROLE_SAFE}-${SESSION_ID_SAFE}.ok"
    rm -f "$marker_file"  # clear any stale marker from a previous run

    cat > "$worker_script" <<'WORKER'
#!/bin/bash
set -o pipefail

ROLE="$1"
SESSION_ID_SAFE="$2"
FILTERED_PATH="$3"
CHECKPOINT_BEAD_ID="$4"
LOG_FILE="$5"
BRIEF_PROMPT_FILE="$6"
WORKER_PATH="$7"
MARKER_FILE="$8"

cleanup() {
    rm -f "$FILTERED_PATH" "$BRIEF_OUTPUT" "$WORKER_PATH"
}
trap cleanup EXIT

if [[ ! -f "$BRIEF_PROMPT_FILE" ]]; then
    echo "brief worker: prompt file missing: $BRIEF_PROMPT_FILE" >&2
    exit 1
fi

if ! command -v claude >/dev/null 2>&1; then
    echo "brief worker: claude not found" >&2
    exit 1
fi

umask 077
BRIEF_OUTPUT="$(mktemp -t "party-brief-output-${ROLE}-${SESSION_ID_SAFE}-XXXXXX")" || exit 1

BRIEF_TIMEOUT="${PARTY_BRIEF_TIMEOUT:-120}"
BRIEF_MODEL="${PARTY_BRIEF_MODEL:-sonnet}"
if ! timeout "$BRIEF_TIMEOUT" claude --model "$BRIEF_MODEL" --print -p "$(cat "$BRIEF_PROMPT_FILE")" < "$FILTERED_PATH" > "$BRIEF_OUTPUT"; then
    echo "brief worker: timeout or claude failure" >&2
    exit 1
fi

if [[ ! -s "$BRIEF_OUTPUT" ]]; then
    echo "brief worker: empty brief output" >&2
    exit 1
fi

if [[ -n "$CHECKPOINT_BEAD_ID" ]] && command -v bd >/dev/null 2>&1; then
    bd create \
      --type checkpoint \
      --title "${ROLE} session brief $(date '+%Y-%m-%d %H:%M')" \
      --label "role:${ROLE}" \
      --label "checkpoint:${CHECKPOINT_BEAD_ID}" \
      --label "source:precompact_auto" \
      --label "kind:session_brief" \
      --body "$(cat "$BRIEF_OUTPUT")" --silent >/dev/null 2>&1 || {
        echo "brief worker: bead write failed" >&2
        exit 1
      }
    # Signal success to parent via marker file (CRIT-1)
    touch "$MARKER_FILE"
fi
WORKER

    chmod +x "$worker_script" || {
        rm -f "$worker_script" "$filtered_path"
        return 1
    }

    nohup bash "$worker_script" \
      "$ROLE" \
      "$SESSION_ID_SAFE" \
      "$filtered_path" \
      "$checkpoint_bead_id" \
      "$LOG_FILE" \
      "$BRIEF_PROMPT_FILE" \
      "$worker_script" \
      "$marker_file" \
      </dev/null >>"$LOG_FILE" 2>&1 &
    local worker_pid=$!
    disown "$worker_pid" 2>/dev/null || true

    # Verify worker process launched
    if ! kill -0 "$worker_pid" 2>/dev/null; then
        rm -f "$worker_script" "$filtered_path"
        echo "pre-compact: warning: worker launch failed" >&2
        return 1
    fi

    # CRIT-1: Poll for success marker (max 30s, 1s interval)
    local poll_count=0
    local marker_seen=0
    while (( poll_count < 30 )); do
        if [[ -f "$marker_file" ]]; then
            marker_seen=1
            break
        fi
        sleep 1
        (( poll_count++ )) || true
    done

    if (( marker_seen )); then
        commit_compact_offset "$CURRENT_SIZE" || echo "pre-compact: warning: failed to commit offset" >&2
        rm -f "$marker_file"
        return 0
    else
        echo "pre-compact: warning: worker did not signal success within 30s — offset not committed" >>"$LOG_FILE"
        return 0
    fi
}

# Generate checkpoint ID
CHK_ID="chk-$(date +%s)-$(head -c 4 /dev/urandom | xxd -p)"
TIMESTAMP=$(date '+%Y-%m-%d %H:%M')

# Get session log info for wisp pointer
SESSION_LOG=$(ls -t ~/.claude/projects/*/*.jsonl 2>/dev/null | head -1)
SESSION_BYTES=$(stat -c %s "$SESSION_LOG" 2>/dev/null || echo "0")

# Create pre-compact checkpoint bead if bd is available
if command -v bd &> /dev/null; then
    CHECKPOINT_BEAD_ID="$(bd create \
      --type checkpoint \
      --title "${ROLE} pre-compact ${TIMESTAMP}" \
      --label "role:${ROLE}" \
      --label "chk_id:${CHK_ID}" \
      --label "source:pre_compact" \
      --label "repo:${REPO}" \
      --body "$(cat <<EOF
# Pre-Compact Checkpoint

**Generated:** ${TIMESTAMP}
**Role:** ${ROLE}
**Checkpoint ID:** ${CHK_ID}
**Trigger:** pre_compact

## Context
Automatic checkpoint created before context compaction.
Session log captured: ${SESSION_LOG} [bytes 0-${SESSION_BYTES}]

---
*Auto-generated by pre-compact hook*
EOF
)" 2>/dev/null | head -1 | tr -d '[:space:]')"

    if [[ -z "$CHECKPOINT_BEAD_ID" ]]; then
        echo "pre-compact: warning: checkpoint bead id missing from bd create output" >>"$LOG_FILE"
    fi
fi

# Phase 1.4: detached async brief generation via filtered transcript slice.
if [[ -z "$CHECKPOINT_BEAD_ID" ]]; then
    echo "pre-compact: warning: skipping worker launch — empty checkpoint bead id" >>"$LOG_FILE"
elif [[ -n "$TRANSCRIPT_PATH" && -n "$SESSION_ID" && -f "$TRANSCRIPT_PATH" ]]; then
    if (( CURRENT_SIZE > LAST_OFFSET )); then
        if command -v party-jsonl-filter >/dev/null 2>&1; then
            umask 077
            RAW_SLICE="$(mktemp -t "party-brief-raw-${ROLE}-${SESSION_ID_SAFE}-XXXXXX")"
            umask 077
            FILTERED_SLICE="$(mktemp -t "party-brief-filtered-${ROLE}-${SESSION_ID_SAFE}-XXXXXX")"
            BYTES_TO_READ=$((CURRENT_SIZE - LAST_OFFSET))

            if dd if="$TRANSCRIPT_PATH" of="$RAW_SLICE" bs=1 skip="$LAST_OFFSET" count="$BYTES_TO_READ" status=none 2>/dev/null; then
                if party-jsonl-filter < "$RAW_SLICE" > "$FILTERED_SLICE"; then
                    if [[ -s "$FILTERED_SLICE" ]]; then
                        launch_session_brief_worker "$FILTERED_SLICE" "$CHECKPOINT_BEAD_ID" || true
                    else
                        echo "pre-compact: empty filter output" >>"$LOG_FILE"
                        rm -f "$FILTERED_SLICE"
                    fi
                else
                    echo "pre-compact: filter step failed" >>"$LOG_FILE"
                    rm -f "$FILTERED_SLICE"
                fi
            else
                echo "pre-compact: failed to slice transcript bytes" >>"$LOG_FILE"
                rm -f "$FILTERED_SLICE"
            fi

            rm -f "$RAW_SLICE"
        else
            echo "pre-compact: warning: party-jsonl-filter not found" >&2
        fi
    else
        echo "pre-compact: no new transcript bytes since last compaction" >>"$LOG_FILE"
    fi
fi

# Release flock (CRIT-3): commit-or-skip outcome finalized
flock -u 9
exec 9>&-

# Output system reminder for recovery
cat << 'EOF'
<system-reminder>
Pre-compact checkpoint created. Context will be compacted.
After compaction, run /restore to recover context.
</system-reminder>
EOF

exit 0
