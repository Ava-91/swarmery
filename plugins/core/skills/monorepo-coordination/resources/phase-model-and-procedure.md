# Phase Model and Coordination Procedure

## Repo shape -- read this first

Read `${CLAUDE_PROJECT_DIR}/.claude/project.json`:

- `repos` lists the code paths agents work in; `monorepo` (if set) names the monorepo root that contains them.
- **Multi-repo workspace** (separate git repos): coordination means sequencing MRs/PRs *across repos* -- each phase is a separate MR in a separate repo, merged in order.
- **Monorepo** (one git repo, many apps/packages): coordination means sequencing *within one repo* -- phases map to ordered PRs (or an ordered commit stack) against the same repo, plus package publish / deploy ordering. The phase model applies unchanged; only the merge mechanics differ, and "merge order" becomes "PR/commit order + deploy order."
- Terminology: "MR" means merge request / pull request -- use whichever the project's git host calls it.

## When to use

- A feature or fix requires changes in two or more of the project's repos (or two or more monorepo apps/packages that deploy independently)
- A new runtime env var is being added (sourced in the infrastructure repo, wired in the deploy/charts repo, consumed by the application)
- A device/edge protocol change requires corresponding WebSocket/SSE format changes in the main app. If the project maintains a living contract document for that boundary, it is the source of truth -- read it FIRST and update it as part of the change (see the `api-contract` skill)
- A secret rotation affects multiple services
- A Helm chart change depends on infrastructure bootstrapping that must happen first

## When NOT to use

- **Single-repo, single-package changes** -- even large refactors within one package do not need coordination. Use `refactor-plan`
- **Hotfixes that touch only one repo** -- deploy directly; no coordination needed
- **Dependency-only upgrades within one repo** -- version bumps in `package.json` or `requirements.txt` that do not affect cross-repo contracts
- **Feature-flag toggles** that change behavior without deploying new code
- **Image promotion between environments** (single image, no code changes) -- use the project's deployment workflow / infra-pack skills when enabled

## Required environment

- `${CLAUDE_PROJECT_DIR}/.claude/project.json` -- for `repos`, `monorepo`, `mainApp`, `device`
- Access to all affected repositories (or the monorepo root)
- Ability to read MR/PR descriptions and CI pipeline status on the project's git host
- Access to cluster/deployment state for post-merge validation (the project's version-drift and staging-health commands, if defined)

## Inputs

| Input | Required | Description |
|-------|----------|-------------|
| Change description | Yes | What the logical change is (e.g., "add MAPS_API_KEY as runtime env var") |
| Affected repos/packages | Yes | Which repos or monorepo packages are touched |
| Operator steps | No | Manual actions required between merges (e.g., "run bootstrap script on cluster") |
| Existing MR references | No | Links to draft MRs/PRs already created |

## Outputs

Length budget: merge-order plan max 60 lines. Each MR description template max 20 lines. CI probe boilerplate max 15 lines per probe. Total output max 200 lines.

Four deliverables:

1. **Merge-order plan** -- numbered sequence of MRs with repo (or package), branch, phase assignment, and dependency arrows
2. **MR description template** for each MR -- containing: Depends on, Blocks, Operator steps, Failure mode if merged out of order
3. **CI probe recommendation** -- for each required operator step, a CI check that fails if the step was skipped
4. **Post-merge validation checklist** -- end-to-end checks to run after the final MR lands

<output-template>
## Coordination Plan: <change description>

### Phase Model
| Phase | Repo/Package | MR | Description | Depends on |
|-------|--------------|----|-------------|------------|
| 1 - Foundation | [repo] | [branch] | [what] | -- |
| 2 - Operator | [action] | -- | [what] | Phase 1 |
| 3 - Wire | [repo] | [branch] | [what] | Phase 2 |
| 4 - Consume | [repo] | [branch] | [what] | Phase 3 |

### MR Descriptions
[per-MR template with Depends on / Blocks / Operator steps / Failure mode]

### CI Probes
[YAML boilerplate for each required operator step]

### Post-Merge Validation
- [ ] version-drift check -- no cross-repo drift (if the project pins versions in a version-pinning repo)
- [ ] staging health check (`cloud.envAlias` environment) -- cluster state matches expectations
- [ ] [scenario-specific smoke test]
</output-template>

## Procedure

1. **Identify all affected repos/packages.** Read `project.json → repos` (and `monorepo`), then map which of them need modifications. Include the version-pinning repo if the project uses one and image digests or chart versions change. Include the device/edge repo (`project.json → device`) if protocol or edge-device changes are involved.
   Checkpoint: all affected repos/packages listed with rationale for inclusion.

2. **Determine merge order using the phase model.**
   - **Phase 1 -- Foundation.** The repo furthest from runtime (usually the infrastructure repo). Changes are additive: new secrets, new schema fields, new defaults. Must be safe to merge without consumers being aware.
   - **Phase 2 -- Operator action (if any).** Cluster-state changes required by Phase 1 (e.g., run a bootstrap script). Machine-enforce via CI probes in Phase 3.
   - **Phase 3 -- Wire consumers.** Charts and configs that reference the new foundation (the deploy/charts repo). Gated by guards that fail if Phase 2 was skipped (e.g., a require-real-secret guard in Helm, pre-flight probes in CI).
   - **Phase 4 -- Consume.** Application-level changes (the main app, the device service). By this point infrastructure and charts are ready.

   In a **monorepo**, the phases become an ordered sequence of PRs (or a stacked commit series) against the same repo: foundation packages merge and release first, consumers after. Deploy ordering still matters even when the code merges together -- if two apps deploy independently, phase the deploys.

   If the device/edge repo is involved (e.g., a protocol change), it is typically Phase 1 or Phase 4 depending on whether it produces or consumes the change. If the project maintains a living contract document for a cross-service boundary (see `api-contract`), that document is the source of truth: read it FIRST to establish the current shape, and place its update in the merge order no later than the emitter change so both sides have an agreed reference before the consumer merges. Both the emitter and the consumer are validated against that contract, not against each other's code.
   Checkpoint: merge order reviewed for circular dependencies -- if found, escalate. If a contracted boundary changed, the contract-document update is present in the merge order.

3. **Draft MR descriptions.** Each MR in the sequence states:
   ```markdown
   ## Coordination
   - **Depends on**: [repo] MR (must be merged first)
   - **Blocks**: [repo] MR (waiting on this)
   - **Operator steps**: [REQUIRED] Run `scripts/bootstrap-secret.sh` on cluster
     OR: None
   - **Failure mode if merged out of order**: [what breaks]
   ```
   Use durable descriptions (file paths, pattern names), not transient MR numbers (`!NN` / `#NN`).
   Checkpoint: every MR description contains all four fields.

4. **Generate CI probe boilerplate for required operator steps.** For every required operator step, produce a CI probe:
   ```yaml
   check-secret-bootstrapped:
     stage: pre-deploy
     script:
       - ssh ${CLUSTER_HOST} "kubectl get secret ${SECRET_NAME} -n ${NAMESPACE} -o jsonpath='{.data}' | grep -v CHANGE_ME"
     rules:
       - if: '$CI_COMMIT_BRANCH == "main"'
   ```
   (Adapt the syntax to the project's CI system -- the pattern, not the YAML dialect, is the point.)
   Rule: if you write "operator must run X before merge" in an MR description, also add a machine-check.
   Checkpoint: every required operator step has a corresponding CI probe.

5. **Define post-merge validation.** After the final MR lands:
   - Version-drift check -- if the project pins image digests/chart versions in a version-pinning repo, confirm no cross-repo drift
   - Staging health check -- the staging environment (`cloud.envAlias`) state matches expectations
   - Specific smoke test: exercise the new capability (e.g., verify maps render after an env-var change)
   Checkpoint: validation checklist covers the end-to-end scenario.

6. **Document rollback strategy.** Each MR should be individually revert-safe: `git revert` on any single MR takes the system back one step. Phase 1 changes must tolerate Phase 3 NOT being present. If reverting is expensive, prefer fix-forward via a small MR.
   Checkpoint: rollback strategy documented per phase.
