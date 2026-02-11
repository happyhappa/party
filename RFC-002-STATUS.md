# RFC-002: Context Capture and Recovery - Deployment Status

**Status:** ⚠️ OPERATIONAL (Beta) — stability fixes in progress
**Date:** 2026-02-06 (updated from 2026-02-04)
**Commit:** 1547dfd

---

## System State

### Deployed Components
- ✅ Beads storage initialized (.beads/ with SQLite)
- ✅ Hooks active (PreCompact, SessionEnd, SessionStart)
- ✅ Checkpoint creation working (6 checkpoints created today)
- ✅ Recovery working (tail extraction + checkpoint rendering)
- ✅ Commands available (/checkpoint, /restore, /plan, /task)

### What's Working
- Automatic checkpoints on session end
- Manual checkpoint creation via /checkpoint
- Recovery context rendering on session start
- Beads query and display (bd CLI)
- Plan/tasklet tracking

### Current Architecture
- **Hooks:** NEW system (beads-based, RFC-002)
- **Manual commands:** BOTH legacy and new coexist
- **Storage:** Beads SQLite + JSONL + Git
- **Recovery:** 3-tier (checkpoint → tail → autogen)

---

## Testing Status

### Completed
- ✅ Unit tests (17+ new tests, all passing)
- ✅ Integration tests per module
- ✅ Manual checkpoint creation verified
- ✅ Recovery rendering verified
- ✅ Hook execution verified (5 CC checkpoints)

### Deferred (Beta Approach)
- ⏳ E2E test suite - iterate as issues arise
- ⏳ Load testing - not needed for beta
- ⏳ Disaster recovery scenarios - git rollback available
- ⏳ Performance benchmarking - acceptable for beta

---

## Migration Path

### Phase 1: Hybrid State (Current)
- Hooks use RFC-002 automatically
- Legacy commands (/pc, /rec) available for fallback
- New commands (/checkpoint, /restore) available
- No conflicts (different storage paths)

### Phase 2: Deprecation (Future)
- Monitor for 2-4 weeks
- If stable, remove legacy commands
- Update documentation to RFC-002 only

### Phase 3: Stability Fixes (Current — 2026-02-06)
Fixes for OOM crashes and protocol gaps found during party session testing.
Priority order:
1. Remove redundant checkpoint ACK from skill + admin
2. Convert checkpoint_request to skill invocation (`/checkpoint --respond <chk_id>`)
3. Add relay-side checkpoint guard (suppress agent-to-agent during checkpoint window)
4. Fix pane ID reload (watch panes.json)
5. Add injection throttling (min gap between sends to OC)
6. Add payload size limits in injector
7. Route hook checkpoints through admin (single-writer consistency)
8. Tune checkpoint trigger thresholds (after above, measure)

### Rollback Plan
If critical issues found:
1. Disable hooks in ~/.claude/settings.json
2. Use legacy /pc /rec only
3. Git revert to pre-RFC-002 commit
4. Fix issues offline, redeploy when ready

---

## Known Issues

- ⚠️ beads.role config warning (cosmetic)
- ⚠️ PATH dependency in hooks (set manually)
- ⚠️ Haiku integration incomplete (autogen not active)
- ⚠️ Summary watcher not deployed (Phase 2.5 deferred)
- 🔴 **OC OOM crashes** — CC sends unsolicited status messages to OC after every checkpoint cycle (emergent behavior, not in RFC). Each relay-injected message becomes a full API turn, growing V8 heap. OC crashed 7+ times. See Stability Punchlist below.
- 🔴 **Redundant checkpoint ACK** — checkpoint_content already clears pending request in admin; the separate ACK is dead weight adding message volume
- 🔴 **No payload size limits** — injector, envelope validation, and SendToPane have no cap on message size
- 🔴 **Stale pane IDs** — relay daemon loads panes.json at startup only; pane recreation after crash requires manual daemon restart
- ⚠️ **Hooks bypass single-writer** — pre-compact.sh and session-end.sh call `bd create` directly, bypassing admin daemon
- ⚠️ **BEADS_DIR missing from daemon env** — fixed manually (added to personal-party.systemd.env) but not in default setup

---

## Next Steps

### Short Term (This Week) — as of 2026-02-04
- [x] Commit RFC-002 code to git
- [x] Create manual checkpoint
- [x] Verify recovery works
- [x] Push to origin
- [x] Monitor hook execution — done 2026-02-05, found OOM issues
- [x] Fix any critical issues — root cause identified 2026-02-06, fixes planned (see Phase 3)

### Medium Term (2-4 Weeks)
- [ ] Complete Haiku integration
- [ ] Deploy summary watcher
- [ ] Deprecate legacy commands
- [ ] Full E2E test suite

### Long Term
- [ ] Performance optimization
- [ ] Cross-repo analytics (if needed)
- [ ] Advanced retention strategies

---

## Long-term Feature Ideas

### Stability Punchlist (from OOM root cause analysis, 2026-02-06)

OC crashed 7+ times from OOM during party sessions. Root cause: CC sends unsolicited status messages to OC after every checkpoint cycle (emergent LLM behavior, not in any RFC). Each injected message becomes a full API round-trip, growing V8 heap until OOM.

- [ ] Remove redundant checkpoint ACK (checkpoint_content already clears pending)
- [ ] Convert checkpoint_request to skill invocation (`/checkpoint --respond <chk_id>`) — skills constrain agent behavior, free-form injection invites drift
- [ ] Add relay-side checkpoint guard (suppress agent-to-agent messages during checkpoint window)
- [ ] Fix stale pane ID reload (watch panes.json or SIGHUP, currently requires daemon restart)
- [ ] Add injection throttling (min gap between sends, especially to OC as hub node)
- [ ] Add payload size limits in injector (no cap exists today)
- [ ] Route hook checkpoints through admin (pre-compact.sh and session-end.sh bypass single-writer)
- [ ] Tune checkpoint trigger thresholds (measure after above fixes)

**Design principle:** If admin sends a command expecting a specific action, pre-can it as a skill. Skills = behavioral contract. Prose = interpretation drift.

### Multi-Session and Inter-Party Communication

- [ ] Multiple tmux sessions running simultaneously (e.g., personal-party + work-party)
- [ ] Inter-party relay routing (party A agents can message party B agents)
- [ ] Non-visible pane agents — agents that communicate via relay but don't need a visible tmux pane (background workers)

### Special-Purpose Agents

- [ ] **Context Cycler** — single-job agent that on admin command performs checkpoint-compact-recover (or kill-and-relaunch) for a target agent pane. Keeps other agents' context fresh without manual intervention.
- [ ] Other repeatable-action agents (test runner, build watcher, log monitor) that admin can trigger via skill invocation

### Aggressive Sub-Agent Delegation

- [ ] Add rules to all Claude agent prompts to aggressively delegate work to sub-agents (Task tool) to preserve their own context window
- [ ] Formalize when to spawn vs when to do inline — context budget thresholds, task complexity heuristics

### Expanded Codex (CX) Fleet

- [ ] Add more Codex agents — small memory footprint (20-36MB vs 250-900MB for Claude) makes them cheap to run
- [ ] Formalize specialized CX roles: review-only, code-only, test-only, research-only
- [ ] Routing rules so OC can target the right CX by specialty

### VOG Observer Pattern (Tested, Needs Productization)

Tested successfully: a separate tmux session using the VOG role to observe the party and inject messages to OC via relay. Connected from phone terminal — clunky but functional. Key insight: the observer model didn't interfere with the party workflow but could still interject when things were stuck or OC missed something.

- [ ] Formalize VOG as an observer/supervisor role with its own relay routing
- [ ] Bridge VOG session to a messaging app (Signal, Telegram, etc.) via webhook or protocol hook — chat back and forth with the VOG agent from mobile
- [ ] VOG can read party state (attack files, metrics, pane tails) and summarize on demand
- [ ] VOG can inject messages to OC (or any agent) when human wants to steer without switching to a terminal

---

## Success Metrics

- ✅ Checkpoints created without manual intervention
- ✅ Recovery context useful after session restart
- ✅ No data loss during context compaction
- ⏳ 90%+ checkpoint success rate (monitoring needed)
- ⏳ Recovery time < 5 seconds (not measured)

---

**Conclusion:** RFC-002 core (beads, hooks, recovery) is operational. Multi-agent party sessions exposed OOM stability issues from emergent checkpoint chatter. Root cause identified, fix plan in Phase 3. Core design is sound — the relay/admin/injector pipeline works, but needs guardrails against unbounded message injection into LLM context.
