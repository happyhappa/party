# RFC-002: Context Capture and Recovery - Deployment Status

**Status:** ✅ OPERATIONAL (Beta)
**Date:** 2026-02-04 (last updated 2026-02-25)
**Commit:** 1547dfd (initial), 77ea8d3 (current)
**See also:** [RFC-004](/mnt/llm-share/rfcs/RFC-004-system-architecture-current-state.md) (System Architecture), [RFC-005](/mnt/llm-share/rfcs/RFC-005-design-principles-and-service-architecture.md) (Design Principles)

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
- **Admin loop:** Bash scheduler replacing LLM admin pane (RFC-004 §5, RFC-005 §2.4)
- **Relay daemon:** Pure message router (Go) — inbox watcher, tmux injector, checkpoint interception
- **Inline LLM calls:** `claude --print` and `codex review/exec` for stateless tasks (subscription, no API cost)
- **Layout:** 3-pane (OC + CC + CX) — reviewer is a service, not a pane (RFC-005 §2.2)

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
- ~~⚠️ Summary watcher not deployed~~ → DEAD CODE, being removed (summarywatcher package unused)
- ~~⚠️ CX Enter timing~~ → ✅ RESOLVED: `paste-buffer -p` adds bracketed paste boundaries (77ea8d3)
- ~~⚠️ CC relay piping~~ → ✅ RESOLVED: relay CLI now reads stdin when `-` passed as message arg (898ac64)
- ~~⚠️ Large payload injection~~ → ✅ RESOLVED: load-buffer + paste-buffer replaces send-keys for payloads (ee57e79, 77ea8d3)

---

## Next Steps

### Short Term (This Week)
- [x] Commit RFC-002 code to git
- [x] Create manual checkpoint
- [x] Verify recovery works
- [x] Push to origin
- [ ] Monitor hook execution
- [ ] Fix any critical issues

### Medium Term (2-4 Weeks)
- [ ] Complete Haiku integration
- [x] ~~Deploy summary watcher~~ → removed as dead code, replaced by inline brief service
- [ ] Deprecate legacy commands
- [ ] Full E2E test suite

### Addendum A: Admin Architecture Evolution
- [x] Phase 1: Admin pane MVP (config, timer, recycler, skills, party-v2)
- [x] Phase 2: Polish (staleness checks, structured logging, CX auto-recovery, integration tests)
- [x] Phase 3: Legacy removal (delete admin package, sessionmap, autogen; admin pane is sole path)
- [ ] Phase 4: Admin pane → admin-loop bash refactor (RFC-004 §5, plan exists)
- [ ] E2E test: Full 3-pane startup with admin-loop + relay daemon

### Recent Fixes (2026-02-17/18)
- [x] CX routing bug — zombie daemon killed, stale session-map deleted, 5 defensive fixes deployed
- [x] Idle-aware checkpoint skipping — daemon tracks JSONL mtime, skips when all agents idle, 2h backstop
- [x] CX auto-compaction — health-check parses Codex footer, injects /compact at <=60% context
- [x] checkpoint_content rejection — daemon now intercepts and writes beads directly via bd (4fb1fa7)
- [x] PATH in systemd env — daemon can now find bd binary
- [x] Adaptive health-check frequency — 5min active, 15min after 3 idle cycles

### Recent Fixes (2026-02-25)
- [x] Large payload injection — load-buffer + paste-buffer replaces send-keys (ee57e79)
- [x] Bracketed paste fix — paste-buffer -p flag for terminal app compatibility (77ea8d3)
- [x] WakePane removal — caused pane shrink accumulation on blocked retries
- [x] Per-pane mutex — sync.Map + getSendLock prevents buffer name race on concurrent sends

### Future: Role Restructuring (updated per RFC-004/005)
- [ ] OC: strict no-code orchestrator, uses background subagents for research to preserve context
- [ ] CX1: primary coder (Codex), owns its worktree
- [x] ~~CX2: dedicated code reviewer pane~~ → eliminated, review is now `codex review` inline service
- [ ] CC: repurposed as infra/testing/research/db — everything CX sandbox prevents
- [x] ~~Admin: downgrade from Sonnet to Haiku~~ → eliminated, admin is now bash loop (zero LLM cost)
- [ ] Inline services: `codex review`, `claude --print` for reviews/briefs/analysis (subscription-based)

### Long Term
- [ ] Performance optimization
- [ ] Cross-repo analytics (if needed)
- [ ] Advanced retention strategies

---

## Success Metrics

- ✅ Checkpoints created without manual intervention
- ✅ Recovery context useful after session restart
- ✅ No data loss during context compaction
- ⏳ 90%+ checkpoint success rate (monitoring needed)
- ⏳ Recovery time < 5 seconds (not measured)

---

**Conclusion:** RFC-002 is operational in beta. System working as designed. Iterate on issues as they arise.
