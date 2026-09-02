---
name: api-contract
description: "Verify field-level alignment between ORM schema, Zod schemas, and route handler shapes, or that SQL migrations match ORM tables. NOT for writing new routes (use api-integration) or code style (use code-standards)."
version: "1.0.0"
owner: "swarmery-core"
allowed-tools: Read, Grep, Glob
disable-model-invocation: true
docs:
  status: reviewed
  source_sha: 863955755827
  updated: 2026-08-06
---

# Purpose

Detects field-name, type, and nullable/optional mismatches across the layers defining an API entity: the ORM schema (`apps/<mainApp>/src/lib/db/schema.ts`), the Zod schema, the route handler's JSON response, and the SQL migrations in the infrastructure repo. Examples use Drizzle and Flyway-style migrations — adapt to the project's stack (project.json -> `stack.db`). Read-only: fixes go to `@implementation-agent` or a developer.

# Rules (never violate)

- Read-only skill (Read, Grep, Glob) — never auto-fix a mismatch; refuse if asked.
- Every finding cites `file:line` and carries a severity; findings below 80% confidence are marked `[LOW-CONFIDENCE]`.
- The report contains zero placeholder text and ends with `CONTRACT-ISSUES: {entity} | CRITICAL: {n} | HIGH: {n} | MEDIUM: {n} | LOW: {n}`.
- A field name that *differs between layers* is this skill's job; a naming *convention* preference belongs to `code-standards`.
- Do not flag Zod `.optional()` vs ORM `.default()`, migration-tool bookkeeping tables, or vendored/generated files; wire-protocol fields keep UPPER_SNAKE_CASE.
- Stop and ask on an ORM type with no Zod equivalent, or >10 Critical findings in one entity.

# Resources

- Read `resources/verification-procedure.md` when running a check — the 7-step procedure, output template, severity criteria, self-check, escalation, and failure modes.
- Read `resources/worked-example-device.md` when you need a full end-to-end example with real code across all four layers.

# How to use

## What it does

Checks that one API entity means the same thing in every layer: it reads the ORM table, the Zod schema, the route handler's JSON response, and the SQL migration, then compares them field by field and reports every mismatch with a `file:line` citation, a severity, and a suggested fix. It never edits code.

## When to use it

- You added, renamed, or retyped an ORM column and want to know what drifted.
- You changed a route handler's response shape or wrote a new Zod schema.
- A SQL migration landed and the ORM table definition needs verification.

Not for writing routes (`api-integration`), convention review (`code-standards`), or complexity audits (`code-quality`).

## How to invoke

```
Skill(skill: "core:api-contract")
```

Pass `entity` (e.g. `order-line-item`) and `scope` (`single-entity` or `full-scan`); the skill locates the four sources itself — no file paths needed.

## Worked example

```
Skill(skill: "core:api-contract")
entity: order-line-item, scope: single-entity
```

The skill compares the `order_line_item` table, its Zod schema, the route handler, and the migration. It finds Zod declares `productId` while ORM and SQL use `product_id`, plus raw ORM output with no validation — three cited findings, closing with `CONTRACT-ISSUES: order-line-item | CRITICAL: 0 | HIGH: 0 | MEDIUM: 2 | LOW: 1`.

## Related

- `api-integration` — building the route; `code-standards` — conventions; `code-quality` — complexity audits.
