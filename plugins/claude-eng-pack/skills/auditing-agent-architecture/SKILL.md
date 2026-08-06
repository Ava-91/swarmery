---
name: auditing-agent-architecture
description: Use when designing or reviewing Claude-based agent systems — agentic loops and stop_reason handling, multi-agent orchestration, coordinators, subagent invocation and context passing, workflow enforcement, Agent SDK hooks (PreToolUse/PostToolUse), task decomposition, or session state and resumption. Also when an agent terminates prematurely, loops forever, repeats work, or subagents return inconsistent or incomplete results.
docs:
  status: reviewed
  source_sha: 1d201d3ba305
  updated: 2026-08-06
---

# Auditing Agent Architecture

## Overview

Best-practice reference for the architecture layer of Claude-based agents: the execution loop, orchestration topology, subagent contracts, enforcement gates, hooks, decomposition, and session state. Each reference module states the correct pattern, the named anti-patterns, and ends with an **Audit Checklist** of verifiable conditions.

## When to use

- Reviewing or designing an agent loop, coordinator, or subagent structure
- Debugging: premature termination, infinite loops, dropped tool results, stalled or repeating agents, coverage gaps between subagents
- Deciding: prompt guidance vs hard gates, hooks vs prompts, fixed pipeline vs dynamic decomposition

Not for tool schemas or MCP servers (use designing-tools-and-mcp) or context-window degradation (use managing-context-reliability).

## Quick reference

| Module | Read when the question is about |
|---|---|
| `references/1-1-agentic-loops.md` | Loop lifecycle, `stop_reason` branching, termination anti-patterns |
| `references/1-2-multi-agent-orchestration.md` | Hub-and-spoke coordination, context isolation, decomposition coverage gaps |
| `references/1-3-subagent-invocation-and-context-passing.md` | Task tool, structured metadata handoff, parallel spawning, `fork_session` |
| `references/1-4-workflow-enforcement-and-handoff.md` | Prompt guidance vs programmatic gates, prerequisite gates, handoff protocols |
| `references/1-5-agent-sdk-hooks.md` | PreToolUse/PostToolUse policy enforcement, hooks vs prompt instructions |
| `references/1-6-task-decomposition-strategies.md` | Fixed sequential vs dynamic decomposition, attention dilution |
| `references/1-7-session-state-and-resumption.md` | Session persistence, stale context, targeted re-analysis |

## How to audit

1. Match the system under review to modules in the table; read those files.
2. Run each module's **Audit Checklist** item by item against the actual code/config.
3. Report every unchecked item as a finding with `file:line` evidence and the module's recommended fix.

For a fast full-domain sweep, `checklists.md` aggregates all seven checklists.

## Related

Error propagation between agents: managing-context-reliability (`5-3`). Review pipelines built on multiple instances: engineering-prompts-and-output (`4-6`).

# How to use

## What it does

This skill gives you a best-practice reference for the architecture layer of Claude-based agent systems. It covers the execution loop, orchestration topology, subagent contracts, enforcement gates, hooks, task decomposition, and session state. Each reference module names the correct pattern and its anti-patterns, then closes with an **Audit Checklist** of conditions you can verify against real code.

## When to use it

- You are designing an agent loop, a coordinator, or a subagent structure and want the known-good shape before you write it.
- An agent terminates early, loops forever, or repeats work it already finished.
- Subagents return inconsistent or incomplete results, or two of them cover the same ground while a third gap goes unread.
- You are deciding between prompt guidance and a hard gate, hooks versus prompt instructions, or a fixed pipeline versus dynamic decomposition.

## When not to use it

- Tool names, descriptions, or input schemas — reach for `designing-tools-and-mcp`.
- Quality that degrades as the context window fills — reach for `managing-context-reliability`.
- System prompts, few-shot examples, or structured output validation — reach for `engineering-prompts-and-output`.

## How to invoke

```
Skill(skill: "claude-eng-pack:auditing-agent-architecture")
```

Invoke it, then say which system you are reviewing and what is going wrong; the skill routes you to the matching reference modules.

## Inputs

- **The system under review** — paths to the agent loop, coordinator, subagent definitions, or hook config — required for an audit, optional if you are only reading the patterns.
- **The symptom or decision** — "the loop never terminates", "hooks or prompt?" — optional, but it narrows which of the seven modules you read.

## What you get back

The skill loads a routing table mapping seven reference modules to the questions each one answers, plus `checklists.md`, which aggregates all seven Audit Checklists for a fast full-domain sweep. Nothing is written to disk. When you run an audit, each unchecked item becomes a finding reported with `file:line` evidence and the module's recommended fix.

## Worked example

```
Skill(skill: "claude-eng-pack:auditing-agent-architecture")

"Our coordinator spawns four subagents over orders/line-items. Two of
them report the same finding and nothing covers the pricing path.
Audit apps/<mainApp>/agents/."
```

The skill points you at `references/1-2-multi-agent-orchestration.md` for decomposition coverage gaps and `references/1-3-subagent-invocation-and-context-passing.md` for the handoff contract. You walk both Audit Checklists against the code and end up with a finding list — each one citing `file:line` and the fix the module prescribes for that failed condition.

## Related

- `designing-tools-and-mcp` — when the problem is which tool the agent picks, not how the agents are wired together.
- `managing-context-reliability` — for error propagation between agents and long-session degradation.
- `engineering-prompts-and-output` — for review pipelines built on multiple instances of the same prompt.
