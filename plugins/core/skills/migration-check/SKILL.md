---
name: migration-check
description: "Validate SQL migration safety, ORM-to-migration alignment, or schema drift between the DB and Zod/DTO types. NOT for deployment config (use deployment) or IaC drift (use infrastructure-as-code)."
version: "1.0.0"
owner: "swarmery-core"
disable-model-invocation: true
allowed-tools: Read, Grep, Glob, Bash
docs:
  status: reviewed
  source_sha: 5fd5a0178153
  updated: 2026-08-06
---

# Purpose

Validate migration safety and schema consistency for the project's PostgreSQL database: check migration SQL files for safety issues and verify alignment between the migration-managed schema, the main app's ORM schema (`apps/<mainApp>/src/lib/db/schema.ts`), and the Zod/DTO validation types in route handlers. Examples use Flyway-style migrations and Drizzle ORM — adapt to the project's actual tools (project.json -> `stack.db`).

# Rules

- Discover migration files from disk (`ls` the migrations directory) — never rely on a static "known migrations" table.
- Never suggest modifying an already-applied migration — create a new migration instead.
- Never approve a migration adding NOT NULL without DEFAULT on a table with existing data; large-table indexes need `CREATE INDEX CONCURRENTLY`.
- Use "Zod/DTO" terminology in reports, never "Java Entity" (unless the project actually has Java services).
- Never mix DDL and DML in one migration file.
- STOP and escalate on: `DROP TABLE`/`TRUNCATE` against a production table, an ORM schema missing or 5+ columns out of sync, or a `FAILED` (partially applied) migration.

# Resources

- Read `resources/safety-checks-and-procedure.md` when reviewing migrations — the five-step procedure, eight safety checks, inputs/outputs, self-check, escalation, failure modes, related skills.
- Read `resources/worked-examples.md` when writing the report — a full safety-review example (with idempotency fix) and the Migration Report template with the alignment table.

# How to use

## What it does

Reviews database migrations before they cause an incident: checks each SQL file for the things that break deploys (irreversibility, destructive statements, locking index builds, `NOT NULL` without default, bad naming, missing idempotency guards), then compares what migrations create against the ORM schema and Zod/DTO types. One report covers migration safety and layer drift.

## When to use it

- A new migration file needs a safety verdict before it is applied.
- You changed the schema and want the ORM definitions confirmed against the SQL.
- A release checklist calls for a pre-deploy migration review.
- Not for deploying (the project's infra pack skills), IaC drift (`infrastructure-as-code`), field-level contract tracing (`api-contract`), or pod health (`troubleshooting`).

## How to invoke

```
Skill(skill: "core:migration-check")
```

Inputs: `migration_file` (optional — omit to sweep the directory) and `scope` (`safety-check`, `schema-alignment`, or `full-audit`). Output is a markdown Migration Report — max 60 lines for `safety-check`, 120 for `full-audit`. Read-only: nothing is applied or modified.

## Worked example

```
Skill(skill: "core:migration-check")
Review V1.0.4__add_order_status.sql before I apply it — scope: safety-check
```

The skill lists the migrations directory from disk, reads the file, runs the checks, and reports: reversible YES, no destructive operations, index uses `CONCURRENTLY`, `NOT NULL` carries a `DEFAULT` — and one PARTIAL: the index is guarded with `IF NOT EXISTS` but the `ADD COLUMN` is not. You get the corrected line to paste back.
