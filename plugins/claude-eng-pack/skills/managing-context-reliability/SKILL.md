---
name: managing-context-reliability
description: Use when reviewing long-running or multi-agent Claude systems for reliability — context window management and summarisation, escalation and ambiguity resolution, error propagation between agents, codebase exploration and context degradation, human review and confidence calibration, or information provenance and multi-source synthesis. Also when quality degrades over long sessions, agents silently swallow failures, mid-context details get lost, or synthesised answers lose attribution.
docs:
  status: generated
  source_sha: 4b55f984d36d
  updated: 2026-08-06
---

# Managing Context and Reliability

## Overview

Best-practice reference for the reliability layer: keeping context healthy over long sessions, escalating instead of guessing, propagating errors with structure, calibrating automation against human review, and preserving provenance. Each reference module states the correct pattern, the named anti-patterns, and ends with an **Audit Checklist** of verifiable conditions.

## When to use

- Reviewing a long-running agent, a multi-agent pipeline's failure handling, or a synthesis/research system
- Debugging: degrading answer quality over time, lost mid-context details, silently dropped errors, unattributed claims
- Deciding: summarisation strategy, escalation criteria, sampling strategy for human review, when to trust automation

Not for the loop/orchestration structure itself (use auditing-agent-architecture).

## Quick reference

| Module | Read when the question is about |
|---|---|
| `references/5-1-context-window-management.md` | Summarisation traps, lost-in-the-middle, tool-result trimming, prompt caching |
| `references/5-2-escalation-and-ambiguity-resolution.md` | Valid vs unreliable escalation triggers, explicit escalation criteria |
| `references/5-3-error-propagation-in-multi-agent-systems.md` | Structured error context, coverage annotations, local recovery |
| `references/5-4-codebase-exploration-and-context-degradation.md` | Scratchpads, subagent delegation, summary injection, `/compact`, crash recovery |
| `references/5-5-human-review-and-confidence-calibration.md` | Stratified sampling, field-level calibration, validation before automation |
| `references/5-6-information-provenance-and-multi-source-synthesis.md` | Claim-source mapping, conflicts, temporal awareness, attribution |

## How to audit

1. Match the system under review to modules in the table; read those files.
2. Run each module's **Audit Checklist** item by item against the actual code/config.
3. Report every unchecked item as a finding with `file:line` evidence and the module's recommended fix.

For a fast full-domain sweep, `checklists.md` aggregates all six checklists.

## Related

Loop and orchestration structure: auditing-agent-architecture. Review-pipeline calibration: engineering-prompts-and-output (`4-6`).

# How to use

## What it does

This skill is a reliability review reference for long-running and multi-agent systems. It covers six areas: context window management, escalation and ambiguity, error propagation between agents, codebase exploration, human review and confidence calibration, and provenance in synthesised answers. Each module names the correct pattern, the anti-patterns, and a checklist of conditions you can verify against real code.

## When to use it

- Answer quality degrades over a long session, or details in the middle of a long context get lost.
- An agent in a pipeline swallows a failure and the caller never learns the step failed.
- A synthesised or research answer makes claims you cannot trace back to a source.
- You are choosing a summarisation strategy, escalation criteria, or a sampling plan for human review.

## When not to use it

- The problem is the loop or orchestration structure itself — use `auditing-agent-architecture`.
- You are calibrating a review pipeline's false-positive rate — use `engineering-prompts-and-output`.
- You are writing tool descriptions or input schemas — use `designing-tools-and-mcp`.

## How to invoke

```
Skill(skill: "claude-eng-pack:managing-context-reliability")
```

Invoke it, then name the system and the symptom you are chasing. The skill's table maps your question to the reference modules worth reading; you read only those.

## What you get back

A quick-reference table pointing at six numbered reference modules, plus a three-step audit procedure: match the system to modules, run each module's **Audit Checklist** against the actual code and config, and report every unchecked item as a finding with `file:line` evidence and the module's recommended fix. For a full sweep, `checklists.md` aggregates all six checklists in one place.

## Worked example

```
Skill(skill: "claude-eng-pack:managing-context-reliability")

"Our review pipeline in orders/line-items runs five agents in sequence.
When agent 3 fails, the report still comes out clean. Audit the failure handling."
```

You are pointed at the error-propagation module. Its checklist asks whether failures carry structured context, whether results are annotated with what they did and did not cover, and whether recovery happens locally or is passed up. You run those items against the pipeline code and get back findings like "step 3's catch block returns an empty result with no coverage annotation — `pipeline.ts:88`", each paired with the fix the module recommends.

## Related

- `auditing-agent-architecture` — for the agentic loop, orchestration, and subagent structure rather than its reliability layer.
- `engineering-prompts-and-output` — for system prompts, structured output, and review-pipeline calibration.
