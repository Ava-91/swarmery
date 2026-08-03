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

**Onboarding ritual: the first invocation on any new project is always `--dry-run`.**

```
/jira-fix <ticket-url-or-key> --dry-run
```

It runs every read this agent would run for real — config, tracker access, the ticket itself,
the reproduction command, the transition list — and shows you the proposed fix and comment
without writing anything to Jira, the `/board`, or git. Review that output before you ever
run the same ticket without `--dry-run`.

**This agent is autonomous (`autonomy: auto`).** Once `.claude/project.json`'s `jira` block is
valid and you drop `--dry-run`, it takes the following actions **without any per-action
confirmation prompt** — there is no human gate anywhere in its flow:

- posting a comment to a real Jira ticket (`addCommentToJiraIssue`);
- transitioning a real Jira ticket's status (`transitionJiraIssue`);
- creating or moving a `/board` card (`swarmery-board-card`'s `POST`/`PATCH`);
- on the `needs-fix` path only: creating an isolated git branch/worktree, `git push`, and
  opening a PR (`jira-delivery`) — gated on a green `@verification-agent` verdict, but never on
  human approval.

None of this is reversible by the agent itself — a wrongly-posted comment or transition is
corrected by a human via Jira directly, and an escalated fix attempt's branch/worktree is the
only thing this pack ever removes on its own (`jira-escalation`'s own cleanup, not a general
undo). Don't point it at a project's real tracker until you've reviewed a dry run.

See `skills/jira-access-preflight/references/setup.md` for the full walkthrough of enabling
and verifying Jira access (both supported channels) before that first `--dry-run`.
