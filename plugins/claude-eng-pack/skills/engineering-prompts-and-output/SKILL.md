---
name: engineering-prompts-and-output
description: Use when writing or reviewing system prompts, few-shot examples, structured output via tool use, JSON schema validation with retry loops, Message Batches API processing, or multi-instance/multi-pass review pipelines such as automated PR reviewers. Also when outputs are inconsistent between runs, JSON fails validation, false positives erode reviewer trust, hallucinated fields appear, or self-review keeps missing defects.
docs:
  status: generated
  source_sha: cc1f93195328
  updated: 2026-08-06
---

# Engineering Prompts and Output

## Overview

Best-practice reference for the prompt-and-output layer: explicit criteria in system prompts, few-shot construction, schema-enforced output, validation/retry, batch processing, and review pipelines. Each reference module states the correct pattern, the named anti-patterns, and ends with an **Audit Checklist** of verifiable conditions.

## When to use

- Writing or reviewing a system prompt, few-shot set, output schema, or an automated review pipeline
- Debugging: inconsistent verdicts, invalid JSON, false-positive floods, hallucinated values, missed defects on self-review
- Deciding: severity criteria vs confidence filtering, `tool_choice` mode for extraction, retry strategy, batch vs real-time

Not for tool interface design itself (use designing-tools-and-mcp) or human-review calibration (use managing-context-reliability).

## Quick reference

| Module | Read when the question is about |
|---|---|
| `references/4-1-system-prompts-with-explicit-criteria.md` | Explicit severity criteria, the false-positive trust problem |
| `references/4-2-few-shot-prompting.md` | Constructing examples, hallucination reduction, false-positive control |
| `references/4-3-structured-output-with-tool-use.md` | The three `tool_choice` modes, what `tool_use` does not guarantee, schema design |
| `references/4-4-validation-retry-and-feedback-loops.md` | Retry-with-error-feedback, its limits, schema vs semantic errors |
| `references/4-5-batch-processing-strategies.md` | Message Batches API, result matching, SLA maths, failure handling |
| `references/4-6-multi-instance-and-multi-pass-review.md` | Why self-review fails, multi-pass architecture, confidence-based routing |

## How to audit

1. Match the pipeline under review to modules in the table; read those files.
2. Run each module's **Audit Checklist** item by item against the actual prompts/schemas/code.
3. Report every unchecked item as a finding with `file:line` evidence and the module's recommended fix.

For a fast full-domain sweep, `checklists.md` aggregates all six checklists.

## Related

Sampling and calibrating against human review: managing-context-reliability (`5-5`). Orchestrating multiple reviewer instances: auditing-agent-architecture (`1-2`, `1-3`).

# How to use

## What it does

This skill is a best-practice reference for the layer where you shape what Claude is asked and what it must return: system prompts, few-shot examples, schema-enforced output, validation and retry, batch jobs, and review pipelines. Each of its six reference modules names the correct pattern, the anti-patterns that break it, and ends with an Audit Checklist of verifiable conditions you can run against real code.

## When to use it

- You are writing or reviewing a system prompt, a few-shot set, an output schema, or an automated review pipeline such as a PR reviewer.
- The same input produces different verdicts between runs, or returned JSON keeps failing validation.
- A reviewer floods its output with false positives, invents fields that were never in the source, or misses defects when asked to check its own work.
- You are choosing between severity criteria and confidence filtering, picking a `tool_choice` mode, or deciding batch versus real-time processing.

## When not to use it

- Designing the tool interface itself — names, descriptions, input schemas — belongs to `designing-tools-and-mcp`.
- Calibrating against human reviewers or sampling their judgements belongs to `managing-context-reliability`.
- Orchestrating the agent loop, subagents, or session state belongs to `auditing-agent-architecture`.

## How to invoke

```
Skill(skill: "claude-eng-pack:engineering-prompts-and-output")
```

Invoke it before you read the prompts or schemas under review, then follow its Quick reference table to the modules that match your question.

## Inputs

- The pipeline under review — prompts, schemas, retry code, batch handling — required, since every checklist item is scored against real files.
- The specific symptom or decision you are chasing (inconsistent verdicts, invalid JSON, retry strategy) — optional, but it narrows you to one or two modules instead of all six.

## What you get back

No files are written. You get the module contents loaded into your context, and an audit procedure: match the pipeline to modules, run each module's Audit Checklist item by item, and report every unchecked item as a finding with `file:line` evidence plus the module's recommended fix. For a fast sweep of the whole domain, `checklists.md` aggregates all six checklists in one place.

## Worked example

```
Skill(skill: "claude-eng-pack:engineering-prompts-and-output")

Request: "Our automated reviewer for orders/line-items returns a different
severity for the same diff on every run, and about half its findings are
false positives. Audit the prompt and the output path."
```

You are pointed at the modules on explicit severity criteria and on structured output with tool use. Running their checklists against the real prompt shows severity is described in adjectives rather than verifiable conditions, and that `tool_use` is treated as a guarantee of a valid schema. You end up with two findings, each carrying a `file:line` reference and the module's recommended fix.

## Related

- `designing-tools-and-mcp` — when the problem is which tool Claude picks, not what it returns.
- `managing-context-reliability` — when quality drops over long sessions or you need human-review calibration.
- `auditing-agent-architecture` — when you need to orchestrate several reviewer instances rather than improve one.
