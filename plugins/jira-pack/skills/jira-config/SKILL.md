---
name: jira-config
description: "Read and validate the jira block in .claude/project.json for jira-pack: required keys, defaults, working-repo resolution. NOT for talking to Jira itself (that's jira-tasks / the Atlassian MCP tools) and NOT for Confluence."
version: "0.1.0"
owner: "swarmery-core"
---

# Purpose

`jira-pack` runs with `autonomy: auto` — nobody is watching to answer a clarifying question.
Anything the ticket text doesn't spell out has to come from `.claude/project.json`, and if it's
missing the run must fail loudly with a fix-it message, never guess. This skill is that gate:
it reads the `jira` block, validates it, resolves defaults, and resolves which repo the run
operates against — before any other jira-pack skill or agent touches a ticket.

# Source of truth

Read `${CLAUDE_PROJECT_DIR}/.claude/project.json`, key `jira`, straight from the file. Never
infer these values from a ticket, a prompt, or a prior run — the schema at
`overlays/_schema/project.schema.json` (`jira` property) is the contract; this skill enforces it
at runtime.

# Validation

Required: `jira.baseUrl`, `jira.projectKey`, `jira.qaStatus`, `jira.repro.test`.

If any of these is missing (key absent, empty string, or `repro` present without `test`):
**stop immediately.** Do not call Jira, do not fall back to a guess, do not proceed with a
partial config. Report exactly which keys are missing and hand back a ready-to-paste JSON
fragment for the consumer to drop into `.claude/project.json`, e.g.:

```
jira-config: missing required keys — jira.projectKey, jira.qaStatus

Paste this into .claude/project.json (fill in the placeholders):

  "jira": {
    "baseUrl": "<jira-base-url>",
    "projectKey": "<PROJECT-KEY>",
    "qaStatus": "QA",
    "repro": { "setup": "npm ci", "test": "npm test" },
    "budget": { "maxFiles": 5, "maxAttempts": 3 }
  }
```

Only list the keys that are actually missing — don't reprint the whole fragment as if nothing
were configured when the config is partially there.

# Working-repo resolution

Default: `${CLAUDE_PROJECT_DIR}`. If the caller passes `--repo <path>`, use that path instead,
**provided** it exists and is a git repository (`git -C <path> rev-parse --is-inside-work-tree`
or equivalent) — otherwise stop and report the bad path, don't silently fall back to the
default. The resolved root is printed in the skill's own report and is what gets threaded into
the board card's `prompt` field downstream, so the executor agent operates against the same
repo this skill validated.

**Documented limitation, not a missed case:** there is no "Jira component → repository" mapping
in this iteration. One `jira` block resolves to exactly one working repo (default-or-`--repo`);
multi-repo routing by ticket component is out of scope until a later phase adds it.

# Defaults

- `budget.maxFiles` = 5, `budget.maxAttempts` = 3 when `budget` (or either field) is absent —
  chosen to match `plugins/core/agents/debugger.md`'s own stop conditions (~5 files, 3 failed
  attempts), so the orchestrator's budget never conflicts with the executor's internal limits.
- `repro.setup` is optional. Absent means the setup step is skipped — that is expected
  behavior, not a validation failure. `repro.test` has no default; its absence is a hard stop
  (see Validation above).

# Placeholders / neutrality

Any example this skill prints uses only `<jira-base-url>` and `<PROJECT-KEY>` as placeholders.
Never print a real site host, project key, or team name — `plugins/**` must stay at zero hits
under `scripts/scan-flavor.sh` (`docs/NEUTRALITY.md`).

# Related

- `overlays/_schema/project.schema.json` — the `jira` block's JSON Schema (source of the
  required-keys list above).
- `overlays/example/project.json` — a fully-populated neutral example of the block.
- `plugins/core/skills/jira-tasks/SKILL.md` — read-only Jira queries once a run is underway;
  this skill only resolves and validates config, it never calls the Atlassian MCP tools itself.
- `plugins/core/agents/debugger.md` — source of the `budget` defaults above.
