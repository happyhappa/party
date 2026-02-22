## Infrastructure Rule

Files in `daemon/`, `bin/`, `systemd/` are infrastructure. You CAN edit them here — they merge to main like any other change, and `install.sh` deploys them system-wide.

Do NOT run `install.sh` from this worktree — it deploys from main after merge.

## Runtime Artifacts (not tracked)

`.beads/last_compact_offset_*` and `state/` are runtime artifacts — do not commit them.
