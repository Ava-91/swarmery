---
name: supply-chain-security
description: "Audit and harden container supply-chain controls: image scanning, SBOM generation, immutable digest promotion, rollback retention, and image signing readiness. Not for application code vulnerabilities (use security-audit) or pipeline YAML structure (use deployment)."
version: "1.0.0"
owner: "swarmery-core"
docs:
  status: reviewed
  source_sha: c2261d65d4d6
  updated: 2026-08-06
---

# Purpose

Audit the container image lifecycle — build through promotion — against four baseline controls (image scanning, SBOM generation, immutable digest promotion, rollback retention) and two roadmap controls (image signing, provenance attestation), producing a gap report with current-state assessments and prioritized remediation. Application code vulnerabilities belong to `security-audit`; pipeline YAML structure to the project's infra pack skills. Done when every baseline control has a current-state description, gap assessment, and remediation, with unsafe patterns cited by file.

# Rules (never violate)

1. Read-only audit: never install or configure scanners, or edit pipeline config, without team approval.
2. Roadmap items (signing, provenance) are future work, never blockers.
3. Only `@sha256:...` is immutable — `:v1.2.3` and `:main` are mutable tags; never confuse them.
4. A scanner without a blocking severity threshold is a gap — finding a scanner is not enough.
5. CVE exemptions require a documented rationale and expiry date.
6. Container CI/CD only — SSH device deploys and Terraform state are out of scope.

# Resources

- Read `resources/gap-audit-procedure.md` when running the audit: the skill-boundary scope table, the 8-step procedure with checkpoints, the gap-report template and length budgets, self-check, mistakes, escalation, worked examples, and failure modes.

# How to use

## What it does

Audits your CI/CD pipeline's container image lifecycle against four baseline supply-chain controls plus two roadmap controls, returning a gap report that says, per control, what you have today, whether there is a gap, and what to change — every finding cited to `file:line`.

## When to use it

- Does the pipeline block a build on a critical CVE, and where does the scan run?
- Deployments reference mutable tags like `:main` and you want `@sha256:` digests.
- "Should we sign our images?" needs a readiness answer; a newly added scanner needs its threshold, ordering, and artifact storage checked.

Not for: application code vulnerabilities or `npm audit`/`pip-audit` (`security-audit`), pipeline YAML structure or the deploy itself (the project's infra pack skills), SSH-based device deploys, or package publishing.

## How to invoke

```
Skill(skill: "core:supply-chain-security")
```

State the scope (pipeline or repo) and optionally depth: `baseline` (default) or `roadmap` (adds signing/provenance). Requires read access to the CI config and any promotion files.

## Worked example

```
Skill(skill: "core:supply-chain-security")
"Is the CI pipeline for apps/<mainApp> supply-chain hardened?"
```

The skill reads the CI config: a build-and-publish job with no scan stage, no SBOM step, promotion on the mutable tag `:main`, no retention policy. The four-row gap table says: add a scanner after build with `--severity CRITICAL,HIGH --exit-code 1`, emit a CycloneDX SBOM artifact, promote by `@sha256:` digest, configure a 30-day registry cleanup policy — each cited to the proving line.

## Related

`security-audit` (application code and secrets), the project's infra pack skills (pipeline structure, promotion workflow), `monorepo-coordination` (fixes spanning repos).
