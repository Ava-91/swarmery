# Write-Op Policy and Ticket-to-Task Linking

## Join-key convention (tickets ↔ workspace tasks)

Workspace task cards link themselves to tickets via a **`Tickets:`** line in the task-card
`README.md` (under `working/YYYY/MM/DD/<slug>/` in the agent workspace — resolved by
`agent-work.sh` from `AGENT_PROJECT`), e.g. `Tickets: <PROJECT-KEY>-115, <PROJECT-KEY>-93`.
This is the join key in **both** directions:

- **Reporting a ticket?** Grep for an existing task dir first:
  `rg -l "<PROJECT-KEY>-115" <workspace>/working` — if one exists, mention the task slug +
  its SUMMARY.md so the user sees prior work, not a cold ticket.
- **Reporting a task?** Read its `Tickets:` line and pull those keys' live status so the
  card's status and Jira agree.

When you start work that maps to a ticket, add the `Tickets: <PROJECT-KEY>-<n>` line to the
task README (that's a workspace-file edit, not a Jira write — always fine).

## Write-op policy

Creating issues, transitioning status, adding comments, and logging worklogs are **write
operations**. Per the `rules/ASK.md` posture (default to NO; surface what/why/blast-radius
before acting), each one requires an **explicit user request in the current conversation** —
never as a side effect of a read, a report, or "while I was in there". A request to *list* or
*check* tickets never authorises a mutation.

- Don't auto-transition a ticket because a fix merged.
- Don't comment "done" on the user's behalf unprompted.
- The **one blessed pattern**: after shipping a fix, the user may ask you to drop an
  **MR-links comment** (branch/MR URLs + one-line result) on the ticket. Still surface it
  first — it's user-visible and user-requested, not silent.

Write tools (`createJiraIssue`, `transitionJiraIssue`, `addCommentToJiraIssue`,
`addWorklogToJiraIssue`) are loadable via ToolSearch the same way, but only reach for them
once the user has explicitly asked for that specific change.

**Exception — a run launched via `/jira-fix` in a project with `jira-pack` enabled.** This
read-only, always-ask policy governs ad-hoc queries in this skill only. `jira-pack`'s
`@jira-task-runner` agent carries its own, broader write mandate — comment/transition/board
mutation without a per-action confirmation prompt — but that mandate is scoped strictly to a
run explicitly started through `/jira-fix`; it never leaks into an ad-hoc "what's the status of
`<KEY>`" query answered by this skill, and this skill's always-ask posture never narrows what
`/jira-fix` is allowed to do inside its own run. See
`plugins/jira-pack/agents/jira-task-runner.md` (`## Boundary with plugins/core/skills/jira-tasks/SKILL.md`)
for the same boundary stated from the other side.

## Related

- `rules/ASK.md` — human-confirmation posture this skill defers to for any write.
- `rules/ALWAYS.md` — the `Tickets:` task-card convention and workspace layout.
- `plugins/jira-pack/` — `/jira-fix`'s end-to-end autonomous run (access preflight,
  reproduction, delegated fix, evidence comment, QA transition); see the exception above for
  how its broader write mandate relates to this skill's read-only default.
- Atlassian plugin skills (`atlassian:generate-status-report`,
  `atlassian:capture-tasks-from-meeting-notes`) — heavier report/write workflows; this skill
  is the lightweight read path.
- `jira-pack:jira-config` — prefer it when the Jira settings themselves need validating.
- `atlassian:triage-issue` — prefer it when checking a bug report for duplicates before filing.
