# Enabling Jira access for jira-pack

`jira-access-preflight` (see the parent `SKILL.md`) never assumes a channel is already set up
-- it resolves whatever is live at run time. This doc is the enablement walkthrough for the
two channels observed so far, so a human can get *either one* working before the first run.

Everywhere below, `<jira-base-url>` and `<PROJECT-KEY>` are placeholders -- substitute the
project's actual Jira site and project key (from `.claude/project.json` -> `jira.baseUrl` /
`jira.projectKey`, validated by `jira-config`). Never hardcode a real site or key into
`plugins/**` -- see `docs/NEUTRALITY.md`.

## Option A -- official Atlassian MCP plugin

1. Enable the plugin for the project (consumer's own `.claude/settings.json` or
   `enabledPlugins`, per that plugin's own marketplace entry).
2. Confirm the plugin's `.mcp.json` points at its Atlassian MCP endpoint -- this is the
   plugin's own manifest, not something jira-pack ships or edits.
3. Authorize once, in an **interactive** Claude Code session: run `/mcp` and complete the
   OAuth flow for the Atlassian connection.
4. Confirm the tools are visible: `ToolSearch query: "+atlassian getAccessibleAtlassianResources"`
   should return a tool whose prefix matches this plugin's registration.

## Option B -- claude.ai Atlassian/Rovo connector

1. In claude.ai's own connector settings, enable the Atlassian/Rovo connector for the
   workspace/account this session runs under.
2. No `/mcp` step is needed for this channel -- the connector's own settings page handles
   authorization.
3. Confirm the tools are visible the same way: `ToolSearch query: "+atlassian getAccessibleAtlassianResources"`.
   If both channels are enabled, this search may return two tools with the same name under two
   different prefixes -- that's expected; `jira-access-preflight` Step 2 picks between them.

## Verifying access is live

Minimal check, either channel: resolve `getAccessibleAtlassianResources` via `ToolSearch` by
name, call it with no arguments, and confirm the response includes a resource whose URL matches
`<jira-base-url>`. If it does, the channel is authorized and scoped to the right site. This is
exactly `jira-access-preflight` Step 3 -- there is no separate "just check access" tool.

## Headless-session limitation

`/jira-fix` and other jira-pack runs launched via routines, `RemoteTrigger`, or scheduled
dispatch run **non-interactively**. Neither channel's OAuth flow can complete inside a headless
session -- there is no browser round-trip available. Authorization has to happen ahead of time,
in an interactive session (Option A's `/mcp`, or Option B's claude.ai connector settings). If a
previously-issued token expires mid-flight, a headless run cannot refresh it interactively; it
will simply hit `jira-access-preflight` and stop with the `JIRA ACCESS: FAILED` report --
harmless (no partial write happens), but the run needs a human to re-authorize before it can
proceed. Re-run the same command once access is restored.

## Common failure signatures

| Symptom | Cause | Fix |
|---|---|---|
| Every call 404s, even ones that worked in a previous run | `cloudId` went stale (site migrated, or the token was re-scoped to a different resource) | `jira-access-preflight` Step 3 already retries once automatically; if it still fails, re-authorize and let the next run re-resolve `cloudId` from scratch |
| `getAccessibleAtlassianResources` succeeds and lists sites, but none match `<jira-base-url>` | Token/connector is scoped to the wrong Atlassian site(s) -- visible sites don't include the project's own | Re-authorize the channel against the correct site, or confirm `jira.baseUrl` in `.claude/project.json` is the site you actually intended |
| Tools resolve, but every write call (`transitionJiraIssue` / `addCommentToJiraIssue`) fails with a permission error | The token can see the site but lacks project-level permissions in `<PROJECT-KEY>` | Grant the account/token the needed Jira project role; this is a Jira-side permission, not something either channel's setup can work around |
| Both channels are enabled and each resolves the same tool names under a different prefix, with two different Atlassian accounts behind them | Both the official plugin and the claude.ai connector are authorized at once, against different accounts | Expected and supported -- `jira-access-preflight` Step 2's priority rule picks one deterministically per run; disable the channel you don't want live if you'd rather avoid the ambiguity entirely |
