---
name: deps-check
version: "1.0.0"
owner: "swarmery-core"
description: "Use this skill when auditing dependency versions, checking for outdated packages, or scanning for security vulnerabilities across the project's repos. Don't use it for upgrading packages (that requires a separate implementation task) or for deployment config template issues (use deployment)."
allowed-tools: Read, Bash, Glob, Grep
disable-model-invocation: true
color: teal
docs:
  status: reviewed
  source_sha: e6895cc4f08f
  updated: 2026-08-06
---

# Purpose

Audit dependency versions across the project's repositories (`.claude/project.json` → `repos`), producing a structured report of outdated packages, vulnerabilities, and cross-repo version mismatches. Scan-and-report only; acting on findings goes to the implementation agent. Placeholders `<mainApp>`, `<device>`, `<infrastructure-repo>` come from project.json.

# Rules (never violate)

- Read-only: never run `npm audit fix`, `npm update`, or `pip install --upgrade`.
- Every repo in scope is scanned or its failure reported in the header; capture stderr — silent failures produce incomplete reports.
- Note the `helm repo update` side effect explicitly (it mutates the local chart cache and needs network).
- Outdated is not vulnerable — report severities using the registry's scale, never a custom one.
- Report stays within 200 lines; the cross-repo mismatch section always appears, even as "None found".
- Stop and ask on: a critical CVE with no patched version, missing scan tooling, no network, or a missing dependency file.

# Resources

- Read `resources/scan-procedure.md` when running an audit — the procedure with scan commands, self-check, mistakes, escalation, failure modes.
- Read `resources/output-template.md` when compiling results — the report template and a complete worked-example audit.

# How to use

## What it does

Audits the dependency health of every repository in your project into one report: outdated packages, known vulnerabilities, and repos pinning different versions of the same shared package. It reads and reports only — never upgrading a package or touching a lockfile.

## When to use it

- A periodic security review needs a current picture of dependency risk.
- You are cutting a release and want proof no known vulnerability ships.
- A CVE advisory landed, or a shared package may have drifted between repos.

Not for performing upgrades (separate task), deployment config or chart lint issues (the project's infra pack skills), single-file `package.json` review, or offline environments.

## How to invoke

```
Skill(skill: "core:deps-check")
```

No arguments scans every repo in `.claude/project.json` → `repos`; optionally pass `repos` and `severity_threshold` (`critical`/`high`/`moderate`/`low`, default `moderate`).

## Worked example

```
Skill(skill: "core:deps-check")
> Run the monthly dependency audit across all repos.
```

The skill locates each manifest, runs the Node and Python scans in parallel, then refreshes the chart cache and checks chart versions. Back comes: 3 of 3 repos scanned, no failures, 42 npm deps with 2 outdated and 1 moderate CVE, 15 pip deps with 1 outdated, and three ranked recommendations naming exact target versions.

## Related

- `env-check` — same repo scope, for env vars; `code-quality` — reviews the upgrade PR; the project's infra pack skills — deployment config and chart authoring.
