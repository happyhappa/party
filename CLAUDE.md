## Infrastructure Rule

Do NOT edit files in `daemon/`, `daemon/scripts/`, `daemon/admin-skills/`, `bin/`, or `systemd/`.
These are owned by the `main` branch only. If you find an infra bug, report it to the user — the fix goes into main directly, never into this worktree.

Do NOT run `install.sh` from this worktree. It will publish the wrong infra binaries to `~/.local/bin`.

## Runtime Artifacts (not tracked)

`.beads/last_compact_offset_*` and `state/` are runtime artifacts — do not commit them.
