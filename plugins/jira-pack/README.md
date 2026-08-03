# jira-pack

Issue-tracker pack: `/jira-fix` takes a ticket link, verifies tracker access, reproduces the
issue, routes the fix to core executors, comments back with evidence, and moves the ticket to
the QA column. Opt-in — most projects don't need it, and it requires setup before first use.

## Enable per project

```jsonc
"enabledPlugins": { "jira-pack@swarmery": true }
```

## Required config

`/jira-fix` runs with `autonomy: auto` — there's no one to ask mid-run, so everything it can't
read off the ticket itself must be declared in the project's `.claude/project.json` under a
`jira` block. Missing keys are a loud stop (see `skills/jira-config/SKILL.md`), never a guess.

```json
"jira": {
  "baseUrl": "<jira-base-url>",
  "projectKey": "<PROJECT-KEY>",
  "qaStatus": "QA",
  "repro": { "setup": "npm ci", "test": "npm test" },
  "budget": { "maxFiles": 5, "maxAttempts": 3 }
}
```

- `baseUrl`, `projectKey`, `qaStatus`, `repro.test` are required. `repro.setup` is optional
  (skipped, not an error, when absent).
- `budget` defaults to `maxFiles: 5`, `maxAttempts: 3` — the same stop conditions
  `plugins/core/agents/debugger.md` already enforces, so the two never disagree.
- Schema: `overlays/_schema/project.schema.json` (`jira` property). Example:
  `overlays/example/project.json`.

## MCP provider requirement

This pack ships **no `.mcp.json`** — it deliberately doesn't bundle an Atlassian MCP server.
An Atlassian MCP provider (Jira access) must already be configured and authorized on the
machine before `/jira-fix` can do anything beyond config validation. See Phase 4 of the
implementation plan for the provider-resolution details.

## Before you trust it on a real project

**Run the first invocation on any new project with `--dry-run`.** It reproduces the issue and
shows you the proposed fix and comment without writing anything to Jira or moving the ticket.

**This agent is autonomous.** Once config is valid and you drop `--dry-run`, it comments on
real tickets and moves them across the board on its own — there is no confirmation prompt
per-action. Don't point it at a project's tracker until you've reviewed a dry run.
