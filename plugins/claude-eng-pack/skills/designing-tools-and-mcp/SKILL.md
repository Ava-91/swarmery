---
name: designing-tools-and-mcp
description: Use when creating or reviewing tool definitions for Claude — tool names, descriptions, input schemas, structured error responses, tool_choice configuration, MCP server setup and scoping, or built-in tool usage (Grep, Glob, Read, Edit). Also when Claude picks the wrong tool, confuses similar tools, retries hopeless failures, treats empty results as errors, or degrades because too many tools are exposed. NOT for agent loops/orchestration (use auditing-agent-architecture), system prompts/schemas (use engineering-prompts-and-output), or multi-turn error loss (use managing-context-reliability).
docs:
  status: reviewed
  source_sha: 94f22ca1dec5
  updated: 2026-08-06
---

# Designing Tools and MCP

## Overview

Best-practice reference for the tool layer: how tools are named, described, split, scoped, and how their errors are structured so the model can act on them. Each reference module states the correct pattern, the named anti-patterns, and ends with an **Audit Checklist** of verifiable conditions.

## When to use

- Writing or reviewing tool schemas, descriptions, or an MCP server
- Debugging: misrouted tool calls, retry loops on permanent errors, "no results" handled as failure, tool overload
- Deciding: one tool or several, `tool_choice` mode, build vs use an existing MCP server, which built-in tool fits

Not for the agent loop itself (use auditing-agent-architecture) or output schemas for extraction (use engineering-prompts-and-output).

## Quick reference

| Module | Read when the question is about |
|---|---|
| `references/2-1-tool-interface-design.md` | Descriptions that route correctly, splitting overloaded tools, naming |
| `references/2-2-structured-error-responses.md` | Error categories, `isRetryable`, access failure vs valid empty result |
| `references/2-3-tool-distribution-and-tool-choice.md` | Tool overload, `tool_choice` config, role-scoped tool sets |
| `references/2-4-mcp-server-integration.md` | MCP scoping hierarchy, env-var expansion, resources, build-vs-use |
| `references/2-5-built-in-tools.md` | Grep vs Glob, Read/Write/Edit, incremental codebase understanding |

## How to audit

1. Match the tool surface under review to modules in the table; read those files.
2. Run each module's **Audit Checklist** item by item against the actual definitions/config.
3. Report every unchecked item as a finding with `file:line` evidence and the module's recommended fix.

For a fast full-domain sweep, `checklists.md` aggregates all five checklists.

## Related

How tool errors should propagate between agents: managing-context-reliability (`5-3`). Forcing structured output via tools: engineering-prompts-and-output (`4-3`).

# How to use

## What it does

This skill is a best-practice reference for the tool layer that Claude sees: how tools are named, described, split, and scoped, and how their errors come back so the model can act on them. It gives you the correct pattern, the named anti-patterns, and a checklist of verifiable conditions per topic. Use it to design a tool surface, or to explain why Claude keeps calling the wrong one.

## When to use it

- You are writing or reviewing tool schemas, tool descriptions, or an MCP server.
- Claude picks the wrong tool, confuses two similar tools, or degrades once many tools are exposed.
- Claude retries a permanent failure forever, or treats a valid empty result as an error.
- You are deciding: one tool or several, which `tool_choice` mode, build an MCP server or use an existing one.

## When not to use it

- The problem is the agent loop itself — stop conditions, subagents, orchestration. Use `auditing-agent-architecture`.
- You need an output schema for extraction or a system prompt rewrite. Use `engineering-prompts-and-output`.
- Errors are being lost as they pass between agents over a long session. Use `managing-context-reliability`.

## How to invoke

```
Skill(skill: "claude-eng-pack:designing-tools-and-mcp")
```

Invoke it before you read the tool definitions, then follow the module table to the two or three reference files that match the question.

## Inputs

- The tool surface under review — schema files, MCP server config, or the description text — required, since every checklist item is run against real definitions.
- The symptom you are chasing (misrouting, retry loop, tool overload) — optional, but it narrows which modules you read.

## What you get back

No files are written. You get a routing table from question to reference module, and each module ends with an Audit Checklist of conditions you can verify one by one. For a full sweep, `checklists.md` aggregates all five. Findings are reported with `file:line` evidence and the module's recommended fix.

## Worked example

```
Skill(skill: "claude-eng-pack:designing-tools-and-mcp")

"Claude keeps calling search_items when it should call get_item_by_id.
 Review orders/line-items tool definitions."
```

The skill routes you to `references/2-1-tool-interface-design.md` for description and naming, and to `2-3` if the tool count is the real cause. You run those two Audit Checklists against the definitions and end up with a list of unchecked items — each one a finding with the offending line and the fix that module prescribes.

## Related

- `auditing-agent-architecture` — prefer it when the loop, not the tool, is misbehaving.
- `engineering-prompts-and-output` — prefer it for system prompts and forcing structured output via tools.
- `managing-context-reliability` — prefer it when tool errors vanish between agents.
