---
name: guardrails
version: "1.0.0"
owner: "swarmery-core"
description: "Use this skill before executing a risky or hard-to-reverse action — migrations, deletions, deploy/config changes, force operations — to derive a risk level and an APPROVED/REJECTED call with a rollback plan. Don't use it for routine read-only work."
color: red
docs:
  status: draft
  updated: 2026-09-01
---

# Purpose

A risky action gets a structured go/no-go before it runs, derived from an
Impact × Reversibility matrix — not ad-hoc vibes. The output contract is
binary: `APPROVED` (possibly with conditions) or `REJECTED` (with the
reason) — never ambiguous.

# The check

1. **Short-circuit:** read-only actions (viewing files, running tests) are
   APPROVED immediately — no ceremony.
2. **Risk level** = Impact (Low / High / Critical) × Reversible (Yes / No):
   Low×Yes → Low; High×Yes → Medium; Low/High×No → High; Critical×anything →
   Critical. The matrix and per-action guidance: `resources/risk-checks.md`.
3. **Deterministic checks** for the affected stack (typecheck/lint/tests for
   code, dry-run/lint for config, `--dry-run` for migrations where the
   tooling offers one). A failing deterministic check is an automatic
   REJECTED.
4. **Rollback plan** with actionable commands ("`DROP TABLE IF EXISTS x;`",
   "`git revert <sha>`") — "revert the change" is not a plan.
5. **Critical never auto-approves.** Anything Critical (prod data loss
   potential, credential changes, irreversible deletes) escalates to the user
   regardless of checks.

Verdict line: `GUARDRAIL: {APPROVED|REJECTED} | Risk: {level} | Checks:
{pass/fail/skip} | Rollback: {one line}`.

# How to use

## What it does

Derives a risk level for a proposed action from an Impact × Reversibility matrix, runs the stack's deterministic checks, and emits an unambiguous APPROVED/REJECTED call with a concrete rollback plan — escalating anything Critical to the user instead of auto-approving.

## When to use it

Before migrations, bulk deletes or renames, deploy/infra config changes, force operations, or anything an orchestrator flags as hard to reverse — especially inside autonomous runs where no human watches each step.

## How to invoke

`@core:tech-lead` loads it via `skills:` and applies it before risky delegations; load it manually (`/guardrails` context or read `resources/risk-checks.md`) when about to run a risky command yourself.

## Worked example

Proposed: migration adding a `flight_logs` table. Additive CREATE TABLE → Impact Low, Reversible Yes (DROP TABLE) → Risk Low; typecheck/lint/tests pass. `GUARDRAIL: APPROVED | Risk: Low | Checks: 3 pass | Rollback: DROP TABLE IF EXISTS flight_logs;`. The same flow on `DROP COLUMN` with data present would come back High and REJECTED pending an expand/contract plan.
