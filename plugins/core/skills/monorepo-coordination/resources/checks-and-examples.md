# Self-Check, Common Mistakes, and Coordination Examples

## Self-check

Before returning, verify every item:

- [ ] The project's repo shape was read from `project.json` (`repos` / `monorepo`) and the plan matches it
- [ ] Every affected repo/package is identified and included in the merge order
- [ ] The device/edge repo is included if protocol or edge-device changes are involved
- [ ] If a contracted cross-service boundary changed, the living contract document was read first and its update is placed in the merge order
- [ ] Merge order follows the phase model (foundation first, app last)
- [ ] Each MR description contains Depends on, Blocks, Operator steps, and Failure mode
- [ ] Every required operator step has a corresponding CI probe recommendation
- [ ] Post-merge validation checklist covers the end-to-end scenario
- [ ] Each MR is individually revert-safe
- [ ] No stale MR number references -- durable descriptions used instead of transient `!NN` / `#NN`
- [ ] Output does not exceed length budget

## Common mistakes

- DO NOT merge Phase 3 before Phase 1 -- this causes Helm render failures or missing secrets at deploy time
- DO NOT rely on MR description text alone for operator gates -- always back required steps with CI probes
- DO NOT omit the device/edge repo when protocol or firmware changes are involved -- silent message format mismatches result
- DO NOT change a contracted cross-service boundary without updating its living contract document -- it is the source of truth both sides are validated against, and a stale contract file causes silent cross-tier drift
- DO NOT use mutable image tags in cross-repo handoffs -- always reference immutable digests when one MR depends on an image built by another
- DO NOT omit "Blocks" / "Depends on" from MR descriptions -- without explicit dependency arrows, reviewers merge in arbitrary order
- DO NOT reference MR numbers as durable identifiers -- MR numbers (`!18`, `#12`) become stale; reference template files or describe the pattern inline
- DO NOT assume a monorepo needs no coordination -- one repo does not mean one deploy; independently deployed apps still need phase ordering

## Escalation

- **Circular dependency between repos/packages:** escalate -- the change may need to be restructured into additive + consuming phases
- **No CI probe possible for an operator step:** flag explicitly as a gap and recommend monitoring to detect the failure mode
- **Device firmware version mismatch:** if a protocol change requires a firmware update that cannot be tested in CI, escalate for manual test planning
- **Cross-repo drift detected by the version-drift check:** investigate which repo is behind and report before proceeding

## Examples

<example>
**Scenario: Adding a new runtime env var (MAPS_API_KEY) -- multi-repo shape**

Merge order:
1. Infrastructure repo -- add `MAPS_API_KEY` to secret bootstrap seed. (Phase 1)
2. Operator action: run `scripts/bootstrap-secret.sh` on the staging cluster. (Phase 2)
3. Deploy/charts repo -- wire `MAPS_API_KEY` in the main-app chart values + Deployment env. Add a require-real-secret guard. (Phase 3)
4. Main-app repo -- read `MAPS_API_KEY` via the server env accessor, expose to the client via a runtime bridge. (Phase 4)

CI probe for Phase 2:
```yaml
check-maps-key-bootstrapped:
  stage: pre-deploy
  script:
    - ssh ${CLUSTER_HOST} "kubectl get secret app-env -n ${NAMESPACE} -o jsonpath='{.data.MAPS_API_KEY}' | base64 -d | grep -v CHANGE_ME"
  rules:
    - if: '$CI_COMMIT_BRANCH == "main"'
```

Post-merge validation: open browser, confirm the runtime env bridge exposes `MAPS_API_KEY` and maps render.

In a **monorepo**, the same change is 2-3 ordered PRs against one repo (infra manifests → chart wiring → app consumption), with the operator action still gated between them by the same CI probe.
</example>

<example>
**Scenario: Device message format change across a contracted boundary**

First artifact to read: the living contract document for the device↔app boundary (e.g. `docs/contracts/websocket-contract.md` -- check the project's docs and the `api-contract` skill). It declares the message shapes both sides must agree on; establish the current shape here before touching either side, and update it as the first step so both sides validate against one reference.

Merge order:
1. Contract document -- declare the new `PROGRESS_UPDATE` shape (field names, types, casing convention). Source of truth both sides are validated against. (Phase 1)
2. Device repo/package -- add the new message emitter (additive, backward compatible), matching the contract. (Phase 1)
3. Main app -- add the WebSocket handler for the new message type, update the TypeScript types to match the contract. (Phase 4)
4. Deploy/charts repo -- bump chart `appVersion` if the device image digest changed. (Phase 3)
5. Version-pinning repo (if the project uses one) -- record new image digests. (Phase 4)

Post-merge validation: connect to a test device, confirm `PROGRESS_UPDATE` messages appear in the telemetry stream and match the shape declared in the contract document.
</example>

## Failure modes

| Failure | Symptom | Recovery |
|---------|---------|----------|
| Phase 3 merged before Phase 1 | Helm render fails: secret value is `CHANGE_ME` | Revert Phase 3 MR, merge Phase 1 first, run operator step, re-merge |
| Operator step skipped | Deploy succeeds but app crashes on missing secret | Run the operator step, then redeploy; add CI probe to prevent recurrence |
| Device repo omitted from plan | Silent message format mismatch: the app receives unexpected fields | Add the device MR to the sequence, coordinate the device deploy |
| Version-drift check shows drift | One repo's version file references a non-existent image digest | Identify which MR has not been merged; merge it before promoting |

## Related skills

- `api-contract` -- contract-first workflows; the living contract document pattern used at cross-service boundaries
- `refactor-plan` -- planning for changes confined to a single repo/package
- The project's deployment workflow / infra-pack skills when enabled -- CI pipeline design, Helm chart wiring (Phase 3), GitOps environment promotion, and how the version-pinning repo records each promotion step
- `troubleshooting` -- diagnostic patterns when a coordination sequence fails post-merge
- `supply-chain-security` -- digest and retention policies that affect cross-repo image references
