Create a recovery file and update attack state before compaction. This is the relay-aware version.

## Steps

### 1. Update attack state (if active)
Check for active attacks and update `last_updated`:
```bash
for f in ~/llm-share/attacks/*.json; do
  if [ -f "$f" ] && jq -e '.status == "open" or .status == "in_progress"' "$f" > /dev/null 2>&1; then
    # Update last_updated timestamp
    jq --arg ts "$(date -u +%Y-%m-%dT%H:%M:%SZ)" '.last_updated = $ts' "$f" > "$f.tmp" && mv "$f.tmp" "$f"
    echo "Updated attack: $(jq -r .attack_id "$f")"
  fi
done
```

### 2. Create recovery file
Create `/tmp/claude_recovery_{repo}_{datetime}.md` with:

```markdown
# Claude Recovery File
**Repo**: {repo_name}
**Date**: {datetime}
**Session**: {brief description}

---

## 1. Session Summary
{What we worked on this session}

## 2. Key Findings
{Important discoveries, decisions, problems solved}

## 3. Active Attack (if any)
{Attack ID, current phase/chunk, what's in progress}

## 4. Documents Created/Updated
{List with full paths}

## 5. Deferred Tasks
{Items left for later}

## 6. Architecture Notes
{Technical context needed to continue}

## 7. Key Commands
{Useful commands for reference}

## 8. Related Documentation
- Design doc: ~/Sandbox/orchestration_communication/INSTALL_PLAN.md
- Protocol: ~/Sandbox/orchestration_communication/relay-daemon/docs/AGENT_PROTOCOL.md
- Attack state: ~/llm-share/attacks/

## 9. Open Items / Next Steps
{What to pick up next}

## 10. Code Locations
{Relevant file paths}
```

### 3. Signal relay (optional)
If relay is running, it will detect the timestamp update on attack state files.

### 4. Confirm ready for compaction
Tell the user the recovery file is ready and they can proceed with compaction.

## File locations
- Recovery files: `/tmp/claude_recovery_{repo}_{datetime}.md`
- Attack state: `~/llm-share/attacks/{attack_id}.json`
- Relay logs: `~/llm-share/relay/log/events.jsonl`
