# Comment template — `needs-info`

**Purpose**: Jira comment body for the `needs-info` verdict — the mandatory
reproduction **could not be run at all** (no environment, missing repro
steps, an unknown/undocumented service). This is the one template whose
posting is **never** followed by a transition attempt — the ticket's status
is left exactly where it was.

**Rendered and posted by**: `plugins/jira-pack/skills/jira-writeback/SKILL.md`,
with `attemptTransition: no`.
Verdict source: `plugins/jira-pack/skills/jira-triage/SKILL.md` — this is the
"could not run the reproduction" verdict, distinct from `cannot-reproduce`
("ran it, and it didn't reproduce"); see that skill's classification rule.

**Required blocks** (every one must be filled in with real content):
1. what exactly could not be run
2. which step failed
3. concrete questions, as a list
4. an explicit statement that the status was **not** changed

**Language**: the same language the ticket (`summary`/`description`) is
written in; English if that is ambiguous.

---

## Template

```markdown
Could not run the reproduction for this ticket — need more information
before this can be triaged further.

**What couldn't be run**: `<the specific command or setup step that failed to
even execute — e.g. jira.repro.setup, jira.repro.test, or a repro step named
in this ticket>`

**Where it failed**: `<the exact point of failure — missing dependency,
unknown service, absent environment variable, undocumented prerequisite>`

**Questions**
1. `<concrete question — e.g. "which environment does jira.repro.test expect
   SERVICE_X to be reachable in?">`
2. `<concrete question>`
3. `<concrete question, as many as are actually needed — no filler questions>`

**Status**: not changed. This ticket's status was left as-is; a fresh attempt
will run once the above is answered.

<!-- swarmery:jira-task-runner run=<external_id or run tag> -->
```
