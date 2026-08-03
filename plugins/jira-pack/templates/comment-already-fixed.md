# Comment template — `already-fixed`

**Purpose**: Jira comment body for the `already-fixed` verdict — the
reproduction runs **green**, and a commit or test was found that already
closes the exact behavior the ticket describes.

**Rendered and posted by**: `plugins/jira-pack/skills/jira-writeback/SKILL.md`.
Verdict source: `plugins/jira-pack/skills/jira-triage/SKILL.md`.

**Required blocks** (every one must be filled in with real content):
1. link to the commit/PR that closed the problem
2. a doubtful-proof run: the command that was executed, and its output
3. how to re-verify (steps a reporter or QA can follow themselves)

**Language**: the same language the ticket (`summary`/`description`) is
written in; English if that is ambiguous.

---

## Template

```markdown
Checked this against the current codebase — it's already fixed.

**Closed by**: `<commit sha / PR link that resolved this behavior>`

**Verification run**
- Command: `<exact command executed — jira.repro.test and/or the ticket's own
  more specific repro step>`
- Exit code: `0`
- Output (relevant fragment):
```
<trimmed, meaningful excerpt showing the passing run>
```

**How to re-verify**: `<numbered steps a reporter or QA can follow to confirm
independently — e.g. pull <branch>, run <command>, expect <result>>`

<!-- swarmery:jira-task-runner run=<external_id or run tag> verdict=already-fixed -->
```
