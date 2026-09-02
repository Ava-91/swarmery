# Refactor-plan procedure

Placeholders: `<mainApp>` = `project.json → mainApp`; `<device>` = `project.json → device` (the device/edge repo, if the project has one).

## Inputs

- **Refactoring goal**: what to change and why (e.g., "rename the `legacy_entity` table and all references to `new_entity`").
- **Scope boundary**: which repos are in scope. Defaults to all project repos (`project.json → repos`).
- **Constraints**: deadlines, feature-freeze windows, or areas that must not be touched.

## Required environment

- Read access to all potentially affected repos (`.claude/project.json` → `repos`).
- Tools: Read, Grep, Glob (search for references across the codebase).

## Steps

1. **Reason through the blast radius** -- Before running any Grep/Glob searches, reason through which repos are likely affected based on the refactoring goal. Identify the entity type (type, table, message format, file layout) and its likely cross-repo footprint. This prevents unnecessary broad searches and focuses tool calls.
   **Checkpoint:** Entity type identified; likely affected repos listed before any search runs.

2. **Analyze current state** -- Read the target code. Use Grep to find all references to the symbol/pattern being refactored across all project repos (application, device, and infrastructure). Record file paths and usage counts.
   **Checkpoint:** Grep/Glob results reviewed; file count confirmed. Every cited file is a real result, not a guess.

3. **Map the blast radius** -- For each reference found:
   - Is it a type definition, usage, import, or test?
   - Does it cross a service boundary (e.g., a WebSocket message field name used by both the producer service and the consumer app)?
   - Does it affect database schema (requires a migration in the project's migrations directory)?
   - Does it affect deployment values or chart templates?
   **Checkpoint:** Blast radius under 50 files. If over 50, escalate to the user before continuing.

4. **Determine execution order** -- Apply these rules:
   - Types and interfaces first, then implementations, then tests.
   - If the refactor spans repos, follow the monorepo coordination phase model (foundation -> wire -> consume).
   - Database schema changes (migrations) land before application code that depends on the new schema.
   - Prefer a gradual, backward-compatible strategy (Strangler Fig: new implementation alongside old, deprecation warnings, feature flag, then cleanup) over a big-bang rewrite whenever the change crosses a service boundary or API/WebSocket contract.
   **Checkpoint:** Execution order documented with dependency justification for each ordering decision.

5. **Assess risks** -- For each identified risk:
   - What is the failure mode if this step goes wrong?
   - What is the mitigation (feature flag, backward-compatible intermediate step, versioned message format)?
   **Checkpoint:** At least one risk entry per affected repo.

6. **Define rollback** -- For each step, describe how to undo it:
   - Single-repo changes: `git revert` the commit (cite the step number to revert).
   - Database changes: an undo migration or forward migration that restores the old state (provide the SQL).
   - Cross-repo changes: revert in reverse merge order.
   **Checkpoint:** Rollback plan is specific -- includes git commands, migration file names, or revert order.

7. **Write the plan document** -- Use the output template (`resources/plan-template.md`). Include file paths with line numbers where possible. Cite every Grep/Glob result.
   **Checkpoint:** Plan follows the template; all sections present.

## Self-check

- [ ] Every file in the impact analysis was found via Grep/Glob, not guessed
- [ ] The plan covers all project repos in every language of the stack, not just one language
- [ ] No references to languages or frameworks the project does not use (check `project.json → stack`)
- [ ] Database schema changes include a migration step with the path to the project's migrations directory
- [ ] Cross-repo refactors reference the `monorepo-coordination` skill for merge ordering
- [ ] Deployment config changes include a chart/config version bump step
- [ ] The rollback plan is specific (not just "revert the changes" -- includes commit references or migration file names)
- [ ] Risk assessment includes at least one entry per repo affected
- [ ] Blast radius was confirmed under 50 files, or user was consulted for larger scopes
- [ ] Reasoning about likely affected repos preceded the first Grep/Glob call

## Common mistakes

- **Assuming the wrong stack.** Check `project.json → stack` and the project's `CLAUDE.md` for the actual languages and frameworks; do not plan for services or languages the project does not have.
- **Skipping database migration steps.** Schema changes require proper migrations, never manual DDL. Place migration files in the project's migrations directory (see the project's `CLAUDE.md`).
- **Refactoring across repos without a merge order.** Cross-repo changes follow the monorepo coordination protocol. A rename that touches the producer and consumer services simultaneously risks breaking the message contract between them.
- **Breaking the WebSocket/SSE message format without versioning.** If a field name changes in a streamed message, both producer and consumer must be updated. Use a versioned message format or backward-compatible additive changes.
- **Forgetting deployment config version bumps.** Any change to chart templates requires bumping the chart version and refreshing dependencies.
- **Planning a refactor without checking test coverage first.** If the target code has no tests, the plan includes a "write tests for current behavior" step before refactoring.
- **Writing vague rollback plans.** "Revert the changes" is not a rollback plan. Specify which commits to revert, in which order, and which migrations to undo.

## Escalation

- **Blast radius exceeds 50 files**: the refactor may need to be broken into phases. Surface the scope to the user for approval before continuing.
- **Database schema change with no existing migration pattern**: flag it -- the user may need to create the migration manually or consult the infrastructure repo maintainer.
- **WebSocket/SSE format change without a versioning mechanism**: escalate -- breaking a real-time contract requires coordination between the producer and consumer teams.
- **Refactor touches code with zero test coverage**: recommend writing characterization tests before refactoring; flag as a risk.

## What to surface

- Total blast radius: N files across M repos.
- The highest-risk step in the plan and its mitigation.
- Whether the refactor requires a database migration.
- Whether the refactor spans repos and requires monorepo coordination.
- Estimated execution order and any steps that block others.

## Related skills

- `monorepo-coordination` -- merge ordering when the refactor spans repos.
- `functional-design` -- execute pure-function refactoring directly (this skill plans, that skill executes).
- `code-standards` -- coding standards to apply during the refactor.
- `migration-check` -- verifying database migration compatibility.
- The project's infra pack skills -- deployment config changes required by the refactor, and release/image rollback flows (as opposed to code rollback, which this plan covers).
