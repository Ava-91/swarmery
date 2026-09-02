---
name: api-integration
description: "Implement REST route handlers, ORM queries, WebSocket telemetry, or SSE endpoints in the main web app, or consume those APIs from clients. NOT for schema-only changes or deployment/infra config."
version: "1.0.0"
owner: "swarmery-core"
allowed-tools: Read, Write, Grep, Glob
docs:
  status: reviewed
  source_sha: 608ee7571769
  updated: 2026-08-06
---

# Purpose

Produce tested integration code connecting the platform's layers: REST route handlers in the main app (project.json -> `mainApp`), ORM queries, server-side WebSocket clients for device/edge telemetry (project.json -> `device`), and SSE endpoints fanning real-time data to browsers. Generated code follows project conventions; patterns use Drizzle ORM — adapt to the project's actual ORM (project.json -> `stack.db`).

# Rules (never violate)

- Verify the real ORM schema before writing; STOP and ask if table or column names are missing or ambiguous — never code against a guessed schema.
- Always `getDb()` (lazy init), `await auth()` with 401 on authenticated routes, `dynamic = 'force-dynamic'` on session/env-reading routes, Zod on all external data.
- Never hardcode environment-specific values (URLs, hostnames, API keys) — use `getServerEnv()`; refuse if asked.
- Every `useEffect` opening an EventSource/WebSocket returns a cleanup function.
- Never overwrite an existing file without reading it and informing the user first.
- After any write, run `api-contract` to verify field alignment across layers.

# Resources

- Read `resources/integration-patterns.md` when writing code — the six reference patterns (REST handler, ORM queries, lazy DB init, WebSocket, SSE, EventSource hook).
- Read `resources/procedure-and-checks.md` when executing a task — the 8-step procedure, self-check, common mistakes, escalation, failure modes, and a full worked example.

# How to use

## What it does

Writes the integration code connecting your layers: a REST route handler, an ORM query, a server-side WebSocket client ingesting device telemetry, or an SSE endpoint fanning it out. It checks your real schema before writing, then applies project conventions throughout.

## When to use it

- A new route handler under `apps/<mainApp>/src/app/api/**/route.ts`.
- An ORM select, insert, update, or delete in a handler or Server Component.
- A WebSocket telemetry subscription, SSE endpoint, or browser `EventSource` hook.

Not for schema-only changes (`api-contract`), deploy manifests (the project's infra pack skills), style review (`code-standards`/`code-quality`), or metrics/tracing (compose with `observability`).

## How to invoke

```
Skill(skill: "core:api-integration")
```

Describe the integration in plain words: `task` (required), `entity` (required), and optionally `integration_type` (`rest`, `orm`, `websocket`, `sse`) — inferred when omitted.

## Worked example

```
Skill(skill: "core:api-integration")
"Add a route handler to get a single order by ID with its line items."
```

The skill greps the schema to confirm the `orders` and join tables exist, globs the target path to check nothing gets overwritten, then writes the handler: session check, ID parsed with 400 on bad input, a `getDb()` query with two left joins, 404 on empty result. You get the file path, the pattern applied, and an `api-contract` check confirming field alignment.

## Related

- `api-contract` — mandatory after any write; `code-standards`/`code-quality` — review existing code; `observability` — metrics and tracing.
