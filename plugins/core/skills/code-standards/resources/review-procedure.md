# code-standards — procedure, severity, and report format

## Inputs

- `scope` — path to the file, module, or directory to review.
- `repo_type` — `"web-app" | "device" | "service-config" | "infrastructure"`,
  which selects the checklist in `resources/checklists.md`.

## Procedure

**Success criteria:** every applicable checklist item is verified against the
target code, every violation cites `file:line` with a severity, and the report
follows the template below.

1. **Identify the repository and rule set** — read the target path, pick the
   matching checklist. *Checkpoint: repo type confirmed.*
2. **Read target files once** — cache contents for reuse across checklist items.
   *Checkpoint: files read.*
3. **Universal checks** (3a–3c run in parallel) — no hardcoded secrets or
   credentials; no commented-out blocks over 5 lines; no hardcoded
   environment-specific URLs or hostnames. *Checkpoint: universal checks done.*
4. **Repository-specific checks** — apply the checklist; record `file:line`, the
   violating code, and the fix for each hit. Independent greps run in parallel.
   *Checkpoint: checklist verified.*
5. **12-factor rules** (when a Dockerfile or build config is in scope) — no
   `NEXT_PUBLIC_*` build args, no env-specific values baked into images, runtime
   env bridge used for client-visible config. *Checkpoint: 12-factor checked.*
6. **Assign severity** per the criteria below. *Checkpoint: all rated.*
7. **Produce the report** — fill the template; Critical and High findings carry
   before/after code. *Checkpoint: no placeholder text.*
8. **Final acceptance check** — every violation cited, rated, and fixed-by-example;
   the `STANDARDS-VIOLATIONS:` trailer is populated.

## Severity criteria

- **Critical** — blocks the build, causes data loss, or exposes secrets (eager DB
  init, secrets in image layers, a bare `except:` swallowing errors).
- **High** — breaks type safety or correctness (`any` type, missing auth check,
  missing `force-dynamic`).
- **Medium** — breaks maintainability or style conventions (wrong naming, missing
  return-type hint).
- **Low** — minor improvement (a more specific type available, TODO without a
  ticket reference).

## Output template

Markdown, capped at 300 lines. Over budget: report Critical and High in full and
summarize Medium and Low as counts.

```markdown
## Code Standards Report

**Scope:** {file/module/directory path}
**Repository:** {web-app | device | service-config | infrastructure}

| # | Severity | Location | Violation | Fix |
|---|----------|----------|-----------|-----|

**Before (violation #{n}):**
```
{violating code}
```

**After (fix #{n}):**
```
{corrected code}
```

**Summary:** {count} violations ({n} Critical, {n} High, {n} Medium, {n} Low).

STANDARDS-VIOLATIONS: {count} | CRITICAL: {n} | HIGH: {n} | MEDIUM: {n} | LOW: {n}
```

## Self-check before returning

- [ ] Every violation cites `file:line`.
- [ ] Every violation carries a severity from the criteria above.
- [ ] Critical and High findings include before/after code.
- [ ] Findings below 80% confidence are marked `[LOW-CONFIDENCE]`.
- [ ] The `NEXT_PUBLIC_*` rule is applied consistently — allowed in source,
      prohibited as a `--build-arg`.
- [ ] Generated files, vendored code, and `node_modules` are excluded.
- [ ] No placeholder text; the `STANDARDS-VIOLATIONS:` trailer is populated.
- [ ] No function-length or complexity findings — those belong to `code-quality`.

## Common mistakes to avoid

- Do not flag `NEXT_PUBLIC_*` in TypeScript source — only `--build-arg` injection
  at image-build time is prohibited.
- Do not flag `process.env.X` inside a function body; only module-scope access is.
- Do not review `node_modules/`, `.next/`, `__pycache__/`, or `*.generated.*`.
- Do not report a violation without `file:line` — it is not actionable.
- Do not emit length or complexity findings — `code-quality` owns those.
- Do not grep `: any` without word boundaries — use `: any\b` / `as any\b` so
  string literals like `"company"` do not match.

## Escalation

- **Stop and ask** when a file holds more than 20 Critical violations — that needs
  a refactoring plan, not piecemeal fixes.
- **Stop and ask** when two standards contradict each other; surface both.
- **Refuse and explain** when asked to auto-fix in place — fixes belong to
  `@implementation-agent` or the developer.

## Failure modes

- **`: any` matched in a string or comment** — detect: the hit is not in a type
  position. Fix: read surrounding lines; grep with word boundaries.
- **`force-dynamic` flagged on an intentionally static page** — detect: the page
  neither imports `auth()` nor reads runtime env. Fix: verify the import chain
  before flagging.
- **`.env.local` flagged under the NEXT_PUBLIC rule** — detect: the file is
  gitignored and dev-only. Fix: only flag committed `.env` files that differ per
  environment.

## Composition

`code-standards` and `code-quality` compose as a depth-1 fan-out under
`@code-reviewer`: both run as leaves over the SAME scope, and the reviewer
aggregates the `STANDARDS-VIOLATIONS:` and `QUALITY-SCORE:` headers. Never a
nested delegation chain or a routing handoff.

## Worked example — a route handler

**Input:** `scope: apps/<mainApp>/src/app/api/devices/route.ts`, `repo_type: web-app`

```typescript
import { auth } from '@/lib/auth';
import { getDb } from '@/lib/db';
import { devices } from '@/lib/db/schema';

export async function GET(req: Request) {
  const session = await auth();
  if (!session) return Response.json({ error: 'Unauthorized' }, { status: 401 });

  const data: any = await getDb().select().from(devices);
  return Response.json(data);
}
```

**Report:**

```markdown
## Code Standards Report

**Scope:** `apps/<mainApp>/src/app/api/devices/route.ts`
**Repository:** web-app (TypeScript / Next.js 15)

| # | Severity | Location | Violation | Fix |
|---|----------|----------|-----------|-----|
| 1 | Critical | `route.ts:1` | Missing `export const dynamic = 'force-dynamic'` — the route calls `auth()`, which reads session cookies at runtime | Add `export const dynamic = 'force-dynamic';` after the imports |
| 2 | High | `route.ts:10` | `data` annotated `any` — breaks strict type safety | Drop the `: any`; the query result is already typed |

**Summary:** 2 violations (1 Critical, 1 High). Both are single-line fixes.

STANDARDS-VIOLATIONS: 2 | CRITICAL: 1 | HIGH: 1 | MEDIUM: 0 | LOW: 0
```
