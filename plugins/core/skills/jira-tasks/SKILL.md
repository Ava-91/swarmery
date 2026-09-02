---
name: jira-tasks
description: "Read-only Jira for the project's tracker: my tickets, open tickets, ticket status, jira backlog, link a workspace task to a ticket. NOT for write ops (create/transition/comment/log-work) and NOT for Confluence."
version: "1.0.0"
owner: "swarmery-core"
docs:
  status: reviewed
  source_sha: 29018bba26b7
  updated: 2026-08-06
---

# Purpose

Read Jira to answer "what am I working on", "what's the status of `<PROJECT-KEY>-115`", or to reconcile workspace tasks against tickets. **Read-only by default** — every write is a separate, explicit, user-requested action. `<jira-base-url>` and `<PROJECT-KEY>` come from the project's `CLAUDE.md` or `.claude/project.json`; if undocumented, ask once.

# Rules

- Every write (create / transition / comment / worklog) requires an explicit user request in the current conversation — a request to list or check tickets never authorises a mutation. Sole exception: `jira-pack`'s `/jira-fix` runs carry their own write mandate.
- Resolve Atlassian MCP tools by tool name via ToolSearch, never by a hardcoded channel prefix — two channels can expose the same tools under different prefixes.
- Every Jira call needs a cloudId from `getAccessibleAtlassianResources`; on any 404, re-resolve rather than retrying a stale pinned id.
- Render compact tables (key, status, summary, updated), most-recently-updated first — never dump raw issue JSON.
- The `Tickets:` line in a workspace task-card README is the join key both directions; adding it is a workspace edit, never a Jira write.

# Resources

- Read `resources/tool-flow-and-jql.md` when making any Jira call — deferred-tool loading, cloudId resolution, read-tool signatures, JQL recipes, output shape.
- Read `resources/write-policy-and-task-links.md` when a write is requested or when linking tickets to workspace task cards — the full write-op policy, the `/jira-fix` exception, the `Tickets:` convention, related skills.

# How to use

## What it does

Answers Jira questions without changing anything: your open tickets, one ticket's live status, what moved this week — returned as a compact table instead of raw JSON. It also links tickets to workspace task cards in both directions, so a lookup surfaces prior work already on disk.

## When to use it

- You want your open tickets, a team's in-progress list, or a backlog view.
- You need the live status, assignee, and labels of one ticket by key.
- You want a task card's `Tickets:` line reconciled against real Jira status.
- Not for writes (each needs an explicit request), full autonomous runs (`/jira-fix`), or Confluence.

## How to invoke

```
Skill(skill: "core:jira-tasks")
```

Then state the question in plain words — "my open tickets", "status of `<PROJECT-KEY>-115`", "what moved this week". Base URL / project key come from project config; a pinned cloudId is re-resolved automatically on 404.

## Worked example

```
Skill(skill: "core:jira-tasks")
> what am I working on in <PROJECT-KEY>?
```

The skill loads the Atlassian tools by name, resolves the cloudId, and runs `assignee = currentUser() AND project = <PROJECT-KEY> AND statusCategory != Done ORDER BY updated DESC`. You get a four-column table of open tickets, noting any existing task card carrying one of those keys on its `Tickets:` line.
