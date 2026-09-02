# What changed

<!-- One paragraph. What a reader of the changelog needs, not a diff summary. -->

Closes #

## Surface

- [ ] `plugins/**` (marketplace) — semver bumped in the pack's `plugin.json`
- [ ] `tools/swarmery/**` (control plane)
- [ ] `scripts/`, `docs/`, `site/` or CI
- [ ] `overlays/` (consumer-facing schema — a breaking change here breaks every project)

## Gates

Paste the real output. "Should pass" is not output. See
[CONTRIBUTING.md](../blob/main/CONTRIBUTING.md) for the full list and the commands.

```text
bash scripts/scan-flavor.sh                 →
bash scripts/docgen/apply-counts.sh --check →
bash scripts/docgen/check-coverage.sh       →
bash scripts/validate-agent-refs.sh         →
for t in scripts/tests/*.test.sh; …         →
```

Control-plane changes only — `cd tools/swarmery && make build && make test`:

```text
```

## Risk notes

<!-- What could this break for an existing consumer? Anything that changes a plugin's
     behaviour reaches every project on the next `/plugin update`. If a change is
     irreversible or touches a ratchet floor, say so here explicitly. -->

- Rollback:

## Screenshots

<!-- Dashboard or landing-page changes: before and after. The dashboard indexes real
     client projects — check every frame before attaching it. -->

## Checklist

- [ ] Semver bumped for every plugin I touched (and `metadata.version` if `core` moved)
- [ ] No brand or domain tokens added under `plugins/**`
- [ ] No new plan, spec or design doc committed under `tools/swarmery/docs/plan/`
- [ ] Any routing or output-contract regression fixed here has a case in `evals/`
- [ ] Conventional commit subject, one sentence
