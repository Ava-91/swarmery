---
description: Find feature flags, env vars, and config keys referenced in code but absent from env-check / settings — dead toggles that pretend to gate behaviour
color: yellow
docs:
  status: generated
  source_sha: 20462a0637b4
  updated: 2026-08-06
---

# Sweep Stale Flags (Dynamic Workflow)

Scope hint: $ARGUMENTS (optional — restrict to one repo, e.g. the main app or the device repo; see `project.json → mainApp` / `device`)

## When to use

Periodic hygiene sweep across the codebase to find:
- Feature flags read in code but never set in any deployment environment
- Env vars referenced in `process.env.X` / `os.getenv('X')` but absent from `env.example` / `values.*.yaml`
- Config keys that exist in `values.yaml` but no code reads them (rot)

This is breadth-first work — independent files, no mid-run user input needed. Perfect for Dynamic Workflow substrate.

For a targeted check on one variable, use the `env-check` skill directly.

## Pre-flight (mandatory)

- [ ] Phase 1 user-only gaps RESOLVED
- [ ] No code changes intended — discovery + report only
- [ ] Scope is closed (single repo or "all project repos" — `project.json → repos`)

## Instructions to @tech-lead

Generate a Dynamic Workflow that:

1. **Discovery stage** (parallel per repo) — extract:
   - Code references: `grep -rE "process\.env\.[A-Z_]+|os\.getenv\(['\"]?[A-Z_]+"` per repo
   - Code references for feature flags: `grep -rE "growthbook\.feature\(|isEnabled\(['\"]"` (or whatever flag library is in use)
   - Declared env vars: parse `env.example`, `.env.example`, all `values*.yaml`, all `Chart.yaml` defaults
   - Declared flags: parse `growthbook.json` or equivalent
2. **Cross-reference stage** (sequential reduce) — compute three sets:
   - **Stale reads** = used in code, absent everywhere → likely dead code
   - **Orphan declarations** = declared, never read → likely dead config
   - **Schema drift** = declared in some envs but missing in others
3. **Artifact** — `sweep-stale-flags-{date}.md` in `${AGENT_WORKSPACE_ROOT}/${AGENT_PROJECT}/workspace/working/`.

## Categories

1. **Stale env-var reads** — code reads `process.env.X` / `os.getenv('X')` but no env declares X
2. **Stale flag reads** — code calls `isEnabled('foo')` but no flag config declares `foo`
3. **Orphan declarations** — declared in `values.yaml` / `env.example` but no code reads
4. **Schema drift** — declared in `values.<envAlias>.yaml` but missing in `values.prod.yaml` (or vice versa) — high incident risk
5. **Sensitive-name without secret** — variable name contains `KEY|SECRET|TOKEN|PASSWORD` but declared with a plaintext default (should source from the cloud provider's secret manager)

## Stop conditions

- All repos in scope discovered → emit reduced report
- Discovery returned <10 references → ESCALATE (likely scoping error)
- A repo failed checkout / read → continue with remaining, mark in report

## Output format

```markdown
# Stale Flag/Env Sweep — {date}

**Repos scanned:** N
**Stale reads:** X (high incident risk)
**Orphan declarations:** Y (config rot)
**Schema drift:** Z (deploy risk)

## Stale reads (dead code or undeclared env)
- `<mainApp>/src/.../file.ts:line` — reads `X_FLAG`, declared nowhere

## Orphan declarations (delete from values?)
- `values.<envAlias>.yaml:line` — `X_KEY`, no code reads

## Schema drift (urgent)
- `Y_TOKEN` declared in <envAlias> only; prod will fail

## Action plan
- Immediate: schema drift (deploy blockers)
- This week: stale reads in hot paths
- This month: orphan declarations
```

---

Now sweep: $ARGUMENTS

# How to use

## What it does

This command hunts for toggles that only pretend to work. It sweeps your repos for feature flags, environment variables, and config keys, then compares what the code reads against what the deployment config actually declares. You get back a report of the mismatches: reads with no declaration, declarations nobody reads, and keys present in one environment but missing from another.

## When to use it

- You want a periodic hygiene pass over configuration across one repo or every repo in the project.
- You suspect dead feature flags — code branches gated on a flag that no environment sets.
- A deploy failed on a missing variable and you want to find the rest of the drift before the next one.
- You are cleaning up a values file and need to know which keys are safe to delete.

## When not to use it

- Checking a single variable — use the `env-check` skill, which answers that directly.
- Fixing what the sweep found — this run is discovery only and writes no code; hand the report to an executor agent afterwards.
- Auditing secret handling in depth — reach for a security audit instead; this only flags sensitive-looking names with plaintext defaults.

## How to invoke

```
/sweep-stale-flags
```

Run it with no arguments to sweep every repo in the project. Add a scope hint to restrict the sweep to one repo, for example `/sweep-stale-flags apps/<mainApp>`.

## Inputs

- **Scope hint** — a repo name or path limiting the sweep — optional; defaults to all repos listed in the project config.
- **Project config** — the repo list and app names the sweep reads from — required, already present in your project setup.

## What you get back

A dated markdown report written to your private workspace working directory. It opens with counts of repos scanned, stale reads, orphan declarations, and schema drift, then lists each finding with a file path and line number. It closes with an action plan ordered by urgency: schema drift first as a deploy blocker, then stale reads, then orphan declarations. Nothing in your repos is modified.

## Worked example

```
/sweep-stale-flags apps/<mainApp>

→ scans the app repo for process.env reads and flag-library calls
→ parses env.example and every values*.yaml for declarations
→ cross-references the two sets

Report: 1 repo scanned, 4 stale reads, 11 orphan declarations, 2 schema drift
  orders/line-items/src/pricing.ts:42 — reads BULK_DISCOUNT_FLAG, declared nowhere
  values.<envAlias>.yaml:18 — RETRY_LIMIT declared, no code reads it
  API_TOKEN declared in <envAlias> only; the production deploy will fail
```

## Related

- `env-check` — prefer it when you already know which variable you care about.
- `deps-check` — the same hygiene idea applied to dependency versions instead of config keys.
- `code-quality` — use it when the rot you are chasing is in the code itself, not in configuration.
