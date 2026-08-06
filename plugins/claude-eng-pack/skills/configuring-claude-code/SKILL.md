---
name: configuring-claude-code
description: Use when setting up or auditing Claude Code in a repository — CLAUDE.md hierarchy and imports, .claude/rules, custom slash commands and skills, path-specific rules, plan mode vs direct execution, iterative refinement workflow, or CI/CD integration with headless -p mode. Also when instructions get ignored, conventions load in the wrong scope, CLAUDE.md has grown bloated, or a CI pipeline needs non-interactive Claude runs.
docs:
  status: generated
  source_sha: 9a1c1d7a1acc
  updated: 2026-08-06
---

# Configuring Claude Code

## Overview

Best-practice reference for repository-level Claude Code setup: memory files, skills/commands, conditional convention loading, execution modes, and CI. Each reference module states the correct pattern, the named anti-patterns, and ends with an **Audit Checklist** of verifiable conditions.

## When to use

- Setting up a repo: CLAUDE.md structure, `.claude/rules/`, skills, slash commands
- Debugging: ignored instructions, conventions applied in the wrong directories, oversized memory files
- Deciding: plan mode vs direct execution, where a rule belongs, batch vs real-time review in CI

Not for prompt content of the tasks themselves (use engineering-prompts-and-output).

## Quick reference

| Module | Read when the question is about |
|---|---|
| `references/3-1-claude-md-hierarchy-scoping-and-modular-organisation.md` | Memory hierarchy, loading/conflict order, `@` imports, modular organisation |
| `references/3-2-custom-slash-commands-and-skills.md` | Skills system, scoping levels, frontmatter, skills vs CLAUDE.md |
| `references/3-3-path-specific-rules-for-conditional-convention-loading.md` | Glob-based rules vs directory/root CLAUDE.md |
| `references/3-4-plan-mode-vs-direct-execution.md` | When to plan, Explore subagent, plan-then-execute hybrid |
| `references/3-5-iterative-refinement-techniques.md` | Refinement hierarchy, batch vs sequential feedback, example-based communication |
| `references/3-6-ci-cd-integration.md` | Headless `-p` mode, structured output in CI, session isolation, batch vs real-time |

## How to audit

1. Match the repo setup under review to modules in the table; read those files.
2. Run each module's **Audit Checklist** item by item against the actual files (`CLAUDE.md`, `.claude/`, CI config).
3. Report every unchecked item as a finding with `file:line` evidence and the module's recommended fix.

For a fast full-domain sweep, `checklists.md` aggregates all six checklists.

## Related

Agent-side hooks and enforcement: auditing-agent-architecture (`1-4`, `1-5`). Batch API for CI-scale processing: engineering-prompts-and-output (`4-5`).

# How to use

## What it does

This skill is a best-practice reference for how Claude Code itself is configured in a repository: memory files, custom skills and slash commands, path-scoped conventions, execution modes, and non-interactive CI runs. Each reference module states the correct pattern, names the anti-patterns, and ends with an audit checklist you can run against real files. Use it to set a repo up properly, or to find out why an existing setup misbehaves.

## When to use it

- You are setting up a repo and need to decide what goes in `CLAUDE.md`, what goes in `.claude/rules/`, and what should be a skill or a slash command.
- Instructions are being ignored, or conventions load in directories they should not apply to.
- A memory file has grown bloated and you want to split it into imports or modules.
- A CI pipeline needs headless `-p` runs with structured output, and you must choose batch versus real-time review.

## When not to use it

- You are writing the prompt content of a task rather than the repo's configuration — reach for `engineering-prompts-and-output`.
- The problem is an agent loop, subagent orchestration, or a hook that fires at the wrong time — reach for `auditing-agent-architecture`.
- You are defining tools or wiring an MCP server — reach for `designing-tools-and-mcp`.

## How to invoke

```
Skill(skill: "claude-eng-pack:configuring-claude-code")
```

Invoke it, then name the repo or the symptom. The skill points you at the one or two reference modules that match, and you read only those.

## Inputs

- The repository under review — required; the skill reads `CLAUDE.md`, `.claude/`, and CI config directly.
- A focus area or symptom — optional, but it narrows six modules down to the one or two worth reading.

## What you get back

A list of findings. Each unchecked audit item becomes one finding with `file:line` evidence and the recommended fix from the module that raised it. Nothing is written or changed for you — the skill reports, and you decide what to apply.

## Worked example

```
Skill(skill: "claude-eng-pack:configuring-claude-code")

"Our conventions for apps/<mainApp> keep getting applied to the whole repo.
Audit the setup."
```

The skill matches this to the path-specific-rules module, reads how glob rules compare with directory-level and root memory files, then runs that module's audit checklist against your actual files. You get back findings such as a root-level rule that should be a glob-scoped rule, each with the file and line to change.

## Related

`auditing-agent-architecture` covers hooks and agent-side enforcement. `engineering-prompts-and-output` covers what you write in a task prompt and how to get structured output back.
