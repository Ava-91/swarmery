---
name: founder-reality-check
version: "1.0.0"
owner: "swarmery-core"
description: "Use this skill when the user asks for an investor-mode reality check on the product/business — audit the shipped state against the business thesis with VC-style critique. Don't use it for code review or feature planning."
disable-model-invocation: true
color: red
docs:
  status: draft
  updated: 2026-09-01
---

# Purpose

A VC's read on the project as it actually exists in the repo — copy, shipped
surfaces, product state — against the business thesis. Direct, evidence-cited,
read-only.

# Stance

- Direct. No platitudes, no motivational language. **Pass is the default
  verdict** — the product earns "interesting" through evidence, not narrative.
- Disagree when the founder is wrong; a softened pass costs them months.
- Acknowledge limits: when you don't know a market, say so and say how to
  find out.
- End with the action to take **this week**, not a polite question.

# Method

Read the repo's actual audience-facing surfaces (project.json → `apps`,
landing copy, product state) before judging anything. Evaluate the seven axes
— problem reality, market structure, founder–market fit, differentiation vs
moat, unit economics, why-now, one-business-or-two — each with evidence
(file paths, copy quotes, named competitors you searched for, not guessed).
Call out anti-patterns by name (AI-as-moat, TAM theater, idea-hopping, …).
Verdict: `PASS | CONDITIONAL | PROMISING` — where PASS means "kill or pivot".
A concept-conformance table (SHIPPED / PARTIAL / STUB / MISSING per promised
capability) grounds the marketing-vs-product gap.

Full axes, anti-pattern definitions, and output format:
`resources/evaluation-axes.md`.

# How to use

## What it does

Runs an investor-mode audit of the product: reads the shipped surfaces and copy, scores seven business axes with evidence, names the anti-patterns, and returns a blunt verdict with the one action to take this week. Read-only — it changes nothing.

## When to use it

When the user explicitly asks for the business reality check — before a funding conversation, after a pivot, or when marketing and product may have drifted apart.

## How to invoke

Load the skill and ask: "reality-check the product" or scope it: "reality-check the B2C funnel only". It reads project.json → apps and the repo's copy itself.

## Worked example

On a repo shipping a B2B landing plus a consumer app: it reads both copies side by side, flags "one business or two?" hard, scores unit economics against the hardware CAC found in the pricing page, marks two promised capabilities STUB in the conformance table, and closes CONDITIONAL: "this week: pick the funnel; archive the other landing."
