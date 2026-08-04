# Comment template — `needs-fix` on a `class: change` ticket

**Purpose**: Jira comment body posted once requested behavior has actually been
implemented, verified, and opened as a PR — the `needs-fix` verdict's terminal
writeback for a **change** ticket (feature / task / improvement), after
`jira-delivery` has produced a red test, driven it green, and obtained a green
`@verification-agent` verdict plus a PR.

**Not the same comment as `comment-fix-summary.md`.** That template opens with
a root cause, because a defect had one. A change ticket has no root cause —
the behavior simply did not exist — and a "root cause" block invented for it
reads as fiction to whoever reviews the ticket. What replaces it is the
acceptance criteria and the test that now asserts each of them.

**Rendered and posted by**: `plugins/jira-pack/skills/jira-writeback/SKILL.md`,
called from `jira-delivery`, once a green `@verification-agent` verdict and a
PR are in hand for a `class: change` + `needs-fix` run.

**Required blocks** (every one must be filled in with real content):
1. what was implemented
2. the acceptance criteria, each paired with the test that asserts it
3. link to the PR
4. how to verify (steps for QA)
5. what was deliberately left out / risks

**The criteria-to-test pairing is not decoration.** It is the evidence that
this run did the work rather than declaring it done: every criterion
`jira-triage` extracted appears here next to a named test, and a criterion with
no test next to it is stated as *not covered* rather than quietly dropped.

**Language**: the same language the ticket (`summary`/`description`) is
written in; English if that is ambiguous.

---

## Template

```markdown
Implemented and opened for review.

**What was implemented**: `<summary of the new behavior — files/modules
touched, not a line-by-line diff>`

**Acceptance criteria → tests**
1. `<criterion 1>` → `<test name / file:line>` ✅
2. `<criterion 2>` → `<test name / file:line>` ✅
3. `<criterion 3>` → not covered by an automated test: `<why, and how it was
   verified instead>`

The test(s) above were written **before** the implementation and observed
failing against the previous code (`<command>` → `<failing assertion>`); they
pass on this branch.

**PR**: `<PR link>`

**How to verify** (for QA)
1. `<step 1>`
2. `<step 2>`
3. Expected: `<what should now happen>`

**Left out / risks**: `<anything in the ticket deliberately not covered by this
change, follow-up work needed, or risk the reviewer should weigh — "none known"
if genuinely none>`

<!-- swarmery:jira-task-runner run=<external_id or run tag> verdict=needs-fix class=change -->
```
