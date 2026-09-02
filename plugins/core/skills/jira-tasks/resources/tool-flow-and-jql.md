# Tool Flow, JQL Recipes, and Output Shape

## Tool flow

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

## cloudId resolution

Every Jira call needs a **cloudId**. Resolve it by calling
`getAccessibleAtlassianResources` (no args) and taking the `id` of the `<jira-base-url>`
resource. If the project's `CLAUDE.md` pins a cloudId, use it directly — but **if any call
404s** (site migrated, token re-scoped), re-resolve rather than retrying a stale id.

## Read tools

- **Search / list** → `searchJiraIssuesUsingJql` (`cloudId`, `jql`, `fields`, `maxResults`).
  Request only the fields you'll print (`key,status,summary,updated,assignee`) — don't pull
  full descriptions for a list.
- **Single ticket** → `getJiraIssue` (`cloudId`, `issueIdOrKey: "<PROJECT-KEY>-115"`).

## JQL recipes

| Intent | JQL |
|--------|-----|
| My open work | `assignee = currentUser() AND project = <PROJECT-KEY> AND statusCategory != Done ORDER BY updated DESC` |
| Recently updated (last week) | `project = <PROJECT-KEY> AND updated >= -7d ORDER BY updated DESC` |
| By label | `project = <PROJECT-KEY> AND labels = <label> ORDER BY updated DESC` |
| In-progress across the team | `project = <PROJECT-KEY> AND statusCategory = "In Progress" ORDER BY updated DESC` |
| Single ticket (or use `getJiraIssue`) | `key = <PROJECT-KEY>-115` |

`currentUser()` resolves to the caller's Atlassian account — no need to hardcode an accountId.
`statusCategory != Done` is more robust than listing status names, which vary per board.

## Output shape

Render a **compact table**, most-recently-updated first — never dump raw issue JSON:

```
| Key      | Status       | Summary                                  | Updated    |
|----------|--------------|------------------------------------------|------------|
| ABC-115  | In Progress  | Image retention policy relax             | 2026-07-03 |
| ABC-93   | In Review    | Memory-leak audit                        | 2026-07-02 |
```

Truncate long summaries to one line. Link known task dirs inline. For a single ticket,
a short field list (status, assignee, labels, updated, description gist) beats the full blob.
