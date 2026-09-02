# api-integration — procedure, checks, and worked example

## Required environment

- Runtime: `.claude/skills/api-integration/SKILL.md`
- Tools: Read, Write, Grep, Glob
- File system assumptions (verify against the live repo; paths follow the `apps/<mainApp>/` convention):
  - `apps/<mainApp>/` contains the Next.js application
  - `apps/<mainApp>/src/lib/db/index.ts` exports `getDb()` with lazy initialization
  - `apps/<mainApp>/src/lib/db/schema.ts` defines the ORM tables (schema namespace per project conventions)
  - `apps/<mainApp>/src/lib/auth.ts` exports the `auth()` function (Auth.js v5)

## Inputs

- `task: string` — description of the integration to implement
- `entity: string` — the ORM table/entity involved (e.g., "missions", "devices")
- `integration_type: "rest" | "orm" | "websocket" | "sse"` — which layer to implement

## Outputs

- Format: TypeScript source files written to the appropriate location in `apps/<mainApp>/src/`
- Length budget: each route handler file under 80 lines; each hook under 50 lines
- Template: file path + integration pattern applied + assumptions noted

## Procedure

1. **Identify integration type** — Determine whether the task requires a REST route, ORM query, WebSocket connection, or SSE endpoint.
   **Checkpoint:** Integration type confirmed before any code generation.

2. **Verify existing patterns** — Grep/Glob for existing implementations of the same integration type in `apps/<mainApp>/src/`.
   **Checkpoint:** At least one reference implementation found, or note the pattern is new.

3. **Verify the ORM schema** — Read `apps/<mainApp>/src/lib/db/schema.ts` to confirm table and column names.
   **Checkpoint:** Table exists in schema. If the table is not found or column names are ambiguous, STOP and ask the user. Do not write code based on a guessed schema.

4. **Confidence gate** — Before writing any file, confirm: (a) schema verified, (b) target file path determined, (c) integration pattern selected. If any element is uncertain, STOP and ask.
   **Checkpoint:** All three conditions met.

5. **Pre-write existence check** — Glob the target file path. If the file already exists, Read it first and present a summary of what will change. Do not overwrite without informing the user.
   **Checkpoint:** User informed of overwrites, or file confirmed new.

6. **Implement the integration** — Write the code following the patterns in `integration-patterns.md`. Apply these rules:
   - Always use `getDb()` for database access (never eager init)
   - Always check `await auth()` in authenticated route handlers and return 401 on missing session
   - Always validate external input with Zod before processing
   - Always use `export const dynamic = 'force-dynamic'` on routes that read session or environment
   - Always clean up EventSource and WebSocket connections in `useEffect` return
   - Use `getServerEnv()` for environment-specific configuration (never hardcode URLs)
   **Checkpoint:** File written.

7. **Verify the implementation** — Read the written file back and confirm it follows all six rules above.
   **Checkpoint:** File follows all rules; no obvious type errors.

8. **Post-write contract check** — Run `api-contract` skill to verify field alignment across layers (ORM schema, route handler, Zod types).
   **Checkpoint:** Field alignment verified or mismatches flagged.

## Self-check

- [ ] Every route handler uses `getDb()` (never eager DB init)
- [ ] Every authenticated route checks `await auth()` and returns 401 on missing session
- [ ] Every route reading session or env has `export const dynamic = 'force-dynamic'`
- [ ] All external data (WebSocket messages, request bodies) validated with Zod
- [ ] No hardcoded hostnames or URLs — all from `getServerEnv()` or runtime env
- [ ] All `useEffect` hooks opening EventSource/WebSocket return a cleanup function
- [ ] ORM queries use correct table/column names verified from `schema.ts`
- [ ] Confidence gate passed before any file was written

## Common mistakes

- DO NOT use eager database initialization (`export const db = drizzle(pool)`) — breaks build (no DATABASE_URL at build time)
- DO NOT hardcode edge-device hostnames like `d1.d16.local` — use runtime config via `getServerEnv()`
- DO NOT skip Zod validation on WebSocket inbound data — `JSON.parse` alone provides no type safety
- DO NOT use an empty `setTimeout(() => {}, 3000)` for reconnection — pass the actual reconnect function
- DO NOT use `next/font/google` — causes prerender failures in recent Next.js versions

## Escalation

- STOP and ask when: the ORM schema table for the target entity does not exist
- STOP and ask when: the Auth.js configuration is missing or uses an unfamiliar provider
- STOP and ask when: the task requires bidirectional WebSocket (command sending) — no established pattern
- REFUSE when: asked to hardcode environment-specific values (URLs, hostnames, API keys) into source code

## Worked example: GET /api/missions/:id

**Input:** "Add a route handler to get a single mission by ID with its assigned devices."

**Step 1-3:** Verify schema via Grep -> confirm `missions` table with id, name, type; confirm `deviceMissions` join table.

**Step 5:** Glob `apps/<mainApp>/src/app/api/missions/[id]/route.ts` -> file does not exist -> proceed.

**Step 6:** Write:
```typescript
// apps/<mainApp>/src/app/api/missions/[id]/route.ts
import { auth } from '@/lib/auth';
import { getDb } from '@/lib/db';
import { missions, deviceMissions, devices } from '@/lib/db/schema';
import { eq } from 'drizzle-orm';

export const dynamic = 'force-dynamic';

export async function GET(_req: Request, { params }: { params: Promise<{ id: string }> }) {
  const session = await auth();
  if (!session) return Response.json({ error: 'Unauthorized' }, { status: 401 });
  const { id } = await params;
  const missionId = parseInt(id, 10);
  if (isNaN(missionId)) return Response.json({ error: 'Invalid mission ID' }, { status: 400 });
  const result = await getDb().select().from(missions)
    .leftJoin(deviceMissions, eq(missions.id, deviceMissions.missionId))
    .leftJoin(devices, eq(deviceMissions.deviceId, devices.id))
    .where(eq(missions.id, missionId));
  if (result.length === 0) return Response.json({ error: 'Mission not found' }, { status: 404 });
  return Response.json(result);
}
```

## Failure modes

| Mode | Symptom | Fix |
|------|---------|-----|
| `getDb()` at build time | Build error: "DATABASE_URL is not defined" | Ensure `getDb()` is only called inside request handlers, never at module scope |
| SSE 401 loop | Browser console shows repeated 401 | Use cookie-based auth (Auth.js default) or pass token as query param |
| WS silent drop | Telemetry stops updating, no error in logs | Ensure `ws.on('close')` handler calls `setTimeout(reconnect, delay)` |

## Related skills

- `api-contract` — MANDATORY post-write: verify field alignment across ORM schema, Zod, and route handlers
- `code-standards` — defer for style and convention checks on implemented code
- `code-quality` — defer for function length, complexity, and code smell checks
- `observability` — compose when adding metrics or tracing to an endpoint
- The project's infra pack skills — defer for deployment manifests or infrastructure code
