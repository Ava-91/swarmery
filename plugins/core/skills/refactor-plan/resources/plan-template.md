# Refactoring plan template and worked example

## Output contract

- Format: markdown refactoring plan document with the sections shown in the template below.
- Save location: `${AGENT_WORKSPACE_ROOT}/${AGENT_PROJECT}/workspace/working/{YYYY}/{MM}/{DD}/{slug}/plan/README.md` (workspace plan standard; date = task start, leaf folder = kebab slug). NEVER save the plan inside a code repo (`docs/`, repo root, legacy `.claude-workspace/`).
- Length budget: max 200 lines for plans under 20 files affected; request phase breakdown for larger blast radii.

## Template

```markdown
## Refactoring Plan: [Title]

### Current State
[What exists now -- file paths, type names, usage counts. Every file cited was found via Grep/Glob.]

### Target State
[What it should look like after the refactor]

### Impact Analysis
| Repo | Files Affected | Type of Change | Risk |
|------|---------------|----------------|------|
| apps/<mainApp> | src/lib/db/schema.ts, 12 more | Type rename | Medium |

### Step-by-Step Plan
1. [File path]: [What to change] -- [Why this order]
2. ...

### Risk Assessment
| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| WebSocket format mismatch | Medium | High | Version the message format; deploy the producer service first |

### Rollback Plan
[Steps to undo safely -- git revert sequence, undo migration if applicable. Specific enough to execute without interpretation.]

### Effort Estimate
| Phase | Estimated Time | Risk |
|-------|----------------|------|
| 1. [Phase name] | [hours/days] | Low/Medium/High |

### Cross-Repo Coordination
[If changes span multiple repos, reference the monorepo-coordination skill for merge ordering]
```

## Worked example: renaming the `legacy_entity` table and type to `device` across the codebase

Current state:
- `apps/<mainApp>/src/lib/db/schema.ts`: table `legacy_entity`, type `LegacyEntity`, 47 references across 23 files.
- `<device>/agents/skills/`: 8 references to `legacy_entity` in Python code.
- `<infrastructure-repo>/charts/<mainApp>/values.yaml`: no references (uses generic service names).
- `<infrastructure-repo>/files/backendMigration/`: table `legacy_entity` defined in migration V003.

Step-by-step plan:
1. `<infrastructure-repo>/files/backendMigration/V047__rename_legacy_entity_to_device.sql`: `ALTER TABLE backend.legacy_entity RENAME TO device;` with column renames.
2. `apps/<mainApp>/src/lib/db/schema.ts`: update the ORM schema -- rename table and type.
3. `apps/<mainApp>/src/app/api/legacy-entities/`: rename route directory to `devices`, update handlers.
4. `apps/<mainApp>/src/**/*.ts`: update all imports and references (23 files).
5. `<device>/agents/skills/`: update Python references (8 files).
6. Update tests in both repos.

Risk: WebSocket messages use `device_id` label (already correct). API route rename breaks any external consumers of `/api/legacy-entities` -- add redirect or alias for backward compatibility.

Rollback: revert steps 6-1 in reverse order. For step 1, apply `V048__revert_device_to_legacy_entity.sql` with `ALTER TABLE backend.device RENAME TO legacy_entity;`.

## Failure modes

| Mode | Symptom | Detection | Fix |
|------|---------|-----------|-----|
| Missed reference in Grep | Type or import error after refactor | Run Grep again with broader pattern | Fix missed reference; add a broader search pattern to the plan |
| Migration conflicts with another branch | Migration version collision | Migration validation fails | Renumber the migration; coordinate with team |
| WebSocket format mismatch | Consumer receives messages with old field names | Integration test failure | Deploy producer changes first (additive); then update the consumer |
| Chart version not bumped | Dependency refresh fails or uses stale chart | The infrastructure repo's chart-sync check script fails | Bump version; run sync check |
