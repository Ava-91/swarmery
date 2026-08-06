---
description: Check database migrations and schema consistency
color: red
docs:
  status: generated
  source_sha: dfab6adee002
  updated: 2026-08-06
---

# Migration Check

Database migration safety review (reversibility, data safety, index concurrency, sequential ordering) plus schema alignment between the migrations directory, the ORM schema in the main app (`apps/<mainApp>`), and Zod/DTO types — exact paths per the project's `CLAUDE.md`.

Follow the playbook in `skills/migration-check/SKILL.md` (auto-loaded skill `migration-check`); apply it to $ARGUMENTS if provided.

# How to use

## What it does

Reviews your database migrations for safety and checks that the schema agrees with itself across layers. It looks for irreversible steps, data loss, index builds that lock tables, and out-of-order migration files. It then compares the migrations directory against the ORM schema in your main app and the Zod/DTO types that clients rely on, so a column that exists in one place but not the others gets flagged before it reaches review.

## When to use it

- Before opening a pull request that adds or edits a migration file.
- After changing an ORM model, to confirm a matching migration and matching types exist.
- When a runtime error suggests the database and the application types have drifted apart.
- During review of someone else's migration, to get a second opinion on reversibility and locking.

## When not to use it

- To write a new migration or schema — use the `database-designer` agent.
- To actually run or roll back a migration — this command reads and reports, it does not execute.
- To audit application code for vulnerabilities — use `/security-audit`.

## How to invoke

```
/migration-check
/migration-check apps/<mainApp>/prisma/migrations/20260115_add_order_status
```

Run it with no arguments to review the whole migration set, or pass a path to narrow the review to one migration, one directory, or one schema file.

## Inputs

- Argument — a file or directory path to scope the check — optional. With no argument, the full migration history and schema are reviewed.
- Project `CLAUDE.md` — supplies the exact migration, ORM, and type paths for your repository — required, read automatically.

## What you get back

A report in the conversation, not a file. It covers the safety review (reversibility, data safety, index concurrency, ordering) and the schema alignment across the three layers, with the offending file and line called out for each problem. Nothing is written or migrated as a side effect.

## Worked example

```
/migration-check apps/<mainApp>/prisma/migrations/20260115_add_order_status
```

The command reads the migration, the ORM schema, and the `orders/line-items` DTO and Zod types. It reports that the new `status` column is `NOT NULL` without a default, which will fail on a non-empty table, that the migration has no down step, and that the field is present in the ORM schema but missing from the Zod schema clients validate against. You end up with three specific fixes to make before pushing.

## Related

- `database-designer` — prefer it when you need the migration and schema written, not reviewed.
- `migration-agent` — prefer it for a deeper, agent-driven safety pass on Prisma SQL and schema files.
- `/security-audit` — prefer it when the concern is application vulnerabilities rather than schema shape.
