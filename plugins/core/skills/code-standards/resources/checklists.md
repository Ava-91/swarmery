# code-standards — review checklists by repository type

Map these onto the project's repo list in `.claude/project.json`; the project's
own `CLAUDE.md` overrides anything here that conflicts.

## Main web app (TypeScript / Next.js 15)

### Type safety (owned by this skill)

- [ ] No `any` types (strict mode on) — cite `file:line` per occurrence; grep
      `: any\b` or `as any\b` so string literals do not match.
- [ ] All function parameters carry type annotations.
- [ ] All function return types are declared.
- [ ] Schema validation (e.g. Zod) guards external data — request bodies, query params.
- [ ] ORM schema types are used for DB query results.

### Naming conventions

- [ ] Components: PascalCase files (`DeviceCard.tsx`).
- [ ] Hooks: `use` prefix, camelCase (`useTelemetry.ts`).
- [ ] Utility functions: camelCase (`formatCoordinates.ts`).
- [ ] File names: kebab-case (`device-card.tsx`) except components.
- [ ] Constants: UPPER_SNAKE_CASE.
- [ ] Booleans: `is` / `has` / `can` prefix.

### Next.js 15 patterns

- [ ] Server Components by default — `'use client'` only for hooks, event
      handlers, or browser APIs.
- [ ] `export const dynamic = 'force-dynamic'` on any route or page calling
      `auth()` or reading runtime env.
- [ ] Lazy DB init via `getDb()` — never `export const db = …` at module scope.
- [ ] Server env read through a typed helper (`getServerEnv()`), never
      `process.env.X` at module scope.
- [ ] No `next/font/google` — it breaks prerendering on Next.js 15.

### Client-side environment variables (12-factor rule)

`NEXT_PUBLIC_*` variables are fine in source for local development, but they must
NOT be injected via `--build-arg` at image build time: that bakes an environment
into the image and breaks build-once/deploy-anywhere.

**Prohibited** (fails review):

```yaml
# CI pipeline or Dockerfile
# --build-arg NEXT_PUBLIC_API_URL=$API_URL   <-- PROHIBITED
```

**Required** pattern for client-visible runtime config:

```tsx
// A server component or layout renders the bridge script:
<script dangerouslySetInnerHTML={{
  __html: `window.__ENV__=${JSON.stringify({
    API_URL: process.env.API_URL,
    MAPS_KEY: process.env.MAPS_KEY,
  })}`
}} />

// Client code reads it back:
const apiUrl = window.__ENV__?.API_URL;
```

One image then runs in dev, staging, and production — only the pod's environment
variables change.

## Device / edge repo (Python 3.11+)

### Type safety

- [ ] Type hints on every parameter and return type.
- [ ] `dataclass` or `TypedDict` for structured data.
- [ ] `mypy` passes without errors.

### Style

- [ ] Formatter applied (line length 100).
- [ ] Imports sorted.
- [ ] Linter passes (max complexity 10).
- [ ] No bare `except:` — always catch specific exceptions.

### Async patterns

- [ ] `async`/`await` on all I/O.
- [ ] `asyncio.to_thread()` for blocking calls (synchronous hardware/serial reads).
- [ ] Cleanup in `finally` blocks.
- [ ] `MOCK_MODE` support so the code is testable without hardware.

## Service config (infrastructure repo — runtime per `.claude/project.json` → `cloud.runtime`)

- [ ] Chart/config version bumped on any template change.
- [ ] The runtime's lint / dry-run render passes (requires Bash).
- [ ] `npm run build` passes (requires Bash).
- [ ] Defensive template nesting — nested value references guarded with `with`/`if`.
- [ ] One resource per YAML file.
- [ ] Flat value structure where possible.
- [ ] No secrets in values files — secret overrides go in `*.populated.yaml`.
- [ ] Subchart version bumps update the umbrella chart and lock file; verify by
      comparing the dependency version against the subchart's own version field.
- [ ] A `requireRealSecret`-style helper guards secrets that must never stay
      `CHANGE_ME` in production.

## 12-factor build rules (all repos)

- [ ] No `NEXT_PUBLIC_*` injected via `--build-arg`.
- [ ] No committed `.env*` files that differ per environment.
- [ ] No `ARG`/`ENV` in a Dockerfile that varies across dev/staging/prod
      (`NODE_ENV=production` excepted).
- [ ] No hardcoded URLs in source.
- [ ] No secrets in image layer history (never `--build-arg` a secret).
- [ ] Human-procedural rules backed by CI enforcement.
