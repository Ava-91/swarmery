---
name: jira-config
description: "Read and validate the jira block in .claude/project.json for jira-pack: required keys, defaults, working-repo resolution. NOT for talking to Jira itself (that's jira-tasks / the Atlassian MCP tools) and NOT for Confluence."
version: "0.1.0"
owner: "swarmery-core"
docs:
  status: reviewed
  source_sha: 0b1d68abed0c
  updated: 2026-08-06
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

# How to use

## What it does

This skill reads the `jira` block from your project's `.claude/project.json`, checks that everything a run needs is actually there, and works out which repository the run will operate against. Because jira-pack runs unattended, nothing can be guessed at runtime — so this gate either hands back a validated config or stops the run with a message telling you exactly which keys to add.

## When to use it

- Before any other jira-pack skill or agent touches a ticket, so a run never starts on a half-configured project.
- When a ticket run failed with a config complaint and you want the precise list of missing keys plus a paste-ready fix.
- When you are setting up jira-pack in a new project and want to confirm the `jira` block is complete.
- When you need to know which repository a run will work in, including a `--repo <path>` override.

## When not to use it

- To read or search tickets — use the `jira-tasks` skill, which handles read-only tracker queries.
- To verify that tracker access and credentials actually work — that is the `jira-access-preflight` skill.
- To classify a ticket as a defect or a change — that is the `jira-triage` skill.
- For Confluence pages of any kind; this skill only touches the local config file.

## How to invoke

```
Skill(skill: "jira-pack:jira-config")
```

Call it first in a jira-pack run. It reads the config file itself, so you do not pass the values in.

## Inputs

- `--repo <path>` — an alternative working repository root — optional; must exist and be a git repository, otherwise the run stops.
- `.claude/project.json` — the config file it reads, with a `jira` block holding `baseUrl`, `projectKey`, `qaStatus`, and `repro.test` as required keys — required.

## What you get back

A short report naming the resolved working-repo root and the effective settings, including defaults it filled in (`budget.maxFiles` = 5, `budget.maxAttempts` = 3 when absent; a missing `repro.setup` simply skips the setup step). If a required key is missing, you instead get a hard stop listing only the keys that are actually absent, plus a JSON fragment with neutral placeholders to paste into your config. The resolved repo root is what downstream jira-pack steps operate against.

## Worked example

```
Skill(skill: "jira-pack:jira-config")

→ jira-config: missing required keys — jira.projectKey, jira.qaStatus

  Paste this into .claude/project.json (fill in the placeholders):

    "jira": {
      "projectKey": "<PROJECT-KEY>",
      "qaStatus": "QA"
    }
```

You add the two keys, run it again, and it reports a valid config with the working repo resolved to your project root.

## Related

- `jira-access-preflight` — run it right after this one to confirm the tracker itself is reachable.
- `jira-triage` — prefer it once config is valid and you need the ticket classified.
- `jira-tasks` — prefer it for read-only ticket queries during a run.
