---
name: jira-tasks
description: "Read-only Jira for the project's tracker: my tickets, open tickets, ticket status, jira backlog, link a workspace task to a ticket. NOT for write ops (create/transition/comment/log-work) and NOT for Confluence."
version: "1.0.0"
owner: "swarmery-core"
---

# Purpose

Read Jira to answer "what am I working on", "what's the status of `<PROJECT-KEY>-115`", or to
reconcile workspace tasks against tickets. **Read-only by default** — every write (create /
transition / comment / worklog) is a separate, explicit, user-requested action (see
[Write-op policy](#write-op-policy)).

**Site and project key:** this skill uses `<jira-base-url>` (e.g. `yourteam.atlassian.net`)
and `<PROJECT-KEY>` as placeholders. Check the consumer project's `CLAUDE.md` (or
`.claude/project.json`) for the actual Jira base URL, project key, and any pinned cloudId.
If none is documented, ask the user once and suggest recording it in `CLAUDE.md`.

# Tool flow

The Atlassian MCP tools are **deferred** — their schemas are not loaded at start. Load them
before the first call, or the call fails with `InputValidationError`. **Resolve by tool name,
never by a hardcoded channel prefix**: at least two channels can expose the same tool names
under different prefixes — an officially-installed Atlassian MCP plugin (e.g. a
`mcp__plugin_atlassian_atlassian__*`-shaped prefix) or a claude.ai Atlassian/Rovo connector
(e.g. a `mcp__claude_ai_Atlassian_Rovo__*`-shaped prefix) — and which channel, if any, is live
varies by session and by machine. Hardcoding one prefix reports "no access" against a perfectly
reachable Jira the moment the *other* channel is the one that's live:

```
ToolSearch  query: "+atlassian getAccessibleAtlassianResources searchJiraIssuesUsingJql getJiraIssue"
```

Read the full tool names back from the result and use whichever prefix actually resolved for
every call in this session — if two channels resolve at once, prefer whichever one's
`getAccessibleAtlassianResources` response matches the project's `jira.baseUrl`. This is the
same provider-agnostic resolution `jira-pack`'s preflight step performs; see
`plugins/jira-pack/skills/jira-access-preflight/SKILL.md` (Steps 1-2) for the canonical
procedure this skill's narrower read-only lookup follows.

Every Jira call needs a **cloudId**. Resolve it by calling
`getAccessibleAtlassianResources` (no args) and taking the `id` of the `<jira-base-url>`
resource. If the project's `CLAUDE.md` pins a cloudId, use it directly — but **if any call
404s** (site migrated, token re-scoped), re-resolve rather than retrying a stale id.

- **Search / list** → `searchJiraIssuesUsingJql` (`cloudId`, `jql`, `fields`, `maxResults`).
  Request only the fields you'll print (`key,status,summary,updated,assignee`) — don't pull
  full descriptions for a list.
- **Single ticket** → `getJiraIssue` (`cloudId`, `issueIdOrKey: "<PROJECT-KEY>-115"`).

# JQL recipes

| Intent | JQL |
|--------|-----|
| My open work | `assignee = currentUser() AND project = <PROJECT-KEY> AND statusCategory != Done ORDER BY updated DESC` |
| Recently updated (last week) | `project = <PROJECT-KEY> AND updated >= -7d ORDER BY updated DESC` |
| By label | `project = <PROJECT-KEY> AND labels = <label> ORDER BY updated DESC` |
| In-progress across the team | `project = <PROJECT-KEY> AND statusCategory = "In Progress" ORDER BY updated DESC` |
| Single ticket (or use `getJiraIssue`) | `key = <PROJECT-KEY>-115` |

`currentUser()` resolves to the caller's Atlassian account — no need to hardcode an accountId.
`statusCategory != Done` is more robust than listing status names, which vary per board.

# Join-key convention (tickets ↔ workspace tasks)

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

# Output shape

Render a **compact table**, most-recently-updated first — never dump raw issue JSON:

```
| Key      | Status       | Summary                                  | Updated    |
|----------|--------------|------------------------------------------|------------|
| ABC-115  | In Progress  | Image retention policy relax             | 2026-07-03 |
| ABC-93   | In Review    | Memory-leak audit                        | 2026-07-02 |
```

Truncate long summaries to one line. Link known task dirs inline. For a single ticket,
a short field list (status, assignee, labels, updated, description gist) beats the full blob.

# Write-op policy

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

# Related

- `rules/ASK.md` — human-confirmation posture this skill defers to for any write.
- `rules/ALWAYS.md` — the `Tickets:` task-card convention and workspace layout.
- `plugins/jira-pack/` — `/jira-fix`'s end-to-end autonomous run (access preflight,
  reproduction, delegated fix, evidence comment, QA transition); see the exception above for
  how its broader write mandate relates to this skill's read-only default.
- Atlassian plugin skills (`atlassian:generate-status-report`,
  `atlassian:capture-tasks-from-meeting-notes`) — heavier report/write workflows; this skill
  is the lightweight read path.
