# Comment template — `needs-fix` (posted after Phase 7 delivery)

**Purpose**: Jira comment body posted once a real fix has been delegated,
verified, and opened as a PR — the `needs-fix` verdict's terminal writeback,
after `jira-delivery` (Phase 7) has produced a green
`@verification-agent` verdict and a PR.

**Rendered and posted by**: `plugins/jira-pack/skills/jira-writeback/SKILL.md`,
called from Phase 7's `jira-delivery` skill (not implemented in this phase —
this template exists now so Phase 7 has it ready to render into).

**Required blocks** (every one must be filled in with real content):
1. what was wrong (root cause)
2. what was changed
3. link to the PR
4. how to verify (steps for QA)
5. limitations / risks

**Language**: the same language the ticket (`summary`/`description`) is
written in; English if that is ambiguous.

---

## Template

```markdown
Fix implemented and opened for review.

**Root cause**: `<one-paragraph explanation of what was actually wrong>`

**What changed**: `<summary of the code change — files/modules touched, not a
line-by-line diff>`

**PR**: `<PR link>`

**How to verify** (for QA)
1. `<step 1>`
2. `<step 2>`
3. Expected: `<what should now happen>`

**Limitations / risks**: `<anything not covered by this fix, follow-up work
needed, or risk the reviewer should weigh — "none known" if genuinely none>`

<!-- swarmery:jira-task-runner run=<external_id or run tag> verdict=needs-fix -->
```
