---
name: env-check
version: "1.0.0"
owner: "swarmery-core"
description: "Use this skill when a task involves adding, removing, or renaming environment variables across the project's repos OR verifying env var documentation before a release. Don't use it for runtime env introspection in a live cluster (that requires exec access to the running service)."
disable-model-invocation: true
allowed-tools: Read, Grep, Glob
color: teal
docs:
  status: reviewed
  source_sha: 1cd8c6fbf3fd
  updated: 2026-08-06
---

# Purpose

Audit environment variables across all of the project's repositories (`.claude/project.json` → `repos`) to find missing, unused, undocumented, or inconsistent env vars and flag security issues. Produces a structured markdown report with file:line citations. Static analysis only — no write access, no live-cluster introspection.

# Rules (never violate)

- NEVER print actual secret values — flag only presence and `file:line` location.
- Every finding carries a `file:line` citation; the report stays within 150 lines and states a confidence level (HIGH, or MEDIUM when dynamic access patterns were seen).
- Exclude `node_modules/`, `.next/`, `__pycache__/`, `venv/`, and test fixtures — fixture placeholders are not "missing" vars.
- `process.env[dynamicKey]` / `os.environ[...]` bracket access is a low-confidence finding, never a definitive one.
- `NEXT_PUBLIC_*` vars are client-exposed, never treated as server-side.
- Stop and ask on: an apparent-secret `*.populated.yaml` outside `.gitignore`, more than 5 undocumented in-use vars, or dynamic access above 30% of usage.

# Resources

- Read `resources/audit-procedure.md` when running an audit — the 6-step procedure with per-stack grep patterns, known-variable baseline, self-check, common mistakes, escalation, failure modes.
- Read `resources/output-template.md` when compiling results — the report template and a full cross-repo worked example.

# How to use

## What it does

Audits environment variables across every repository in your project. It reads code, config, and example files without writing anything, then reports which variables are used but undocumented, documented but unused, named inconsistently across repos, or look like hardcoded secrets — each finding with a `file:line` citation.

## When to use it

- You added, removed, or renamed an env var and want every repo to agree on name and default.
- You are cutting a release and need proof all required variables appear in `env.example`.
- You are reviewing a PR touching service config, `env.example`, or a typed env accessor.
- A deployment failed and you suspect a missing or misspelled variable.

Not for values inside a running pod (needs exec access), adding secrets to a cloud secret manager (`gcp-cicd-auth`), wiring CI/CD pipeline variables (the project's infra pack skills), or package audits (`deps-check`).

## How to invoke

```
Skill(skill: "core:env-check")
```

Invoke directly or ask in plain language; optionally pass `repos` (defaults to the full project list) and `focus` (a variable name or prefix such as `NEXT_PUBLIC_`), plus a report path if you want it saved.

## Worked example

```
Skill(skill: "core:env-check")
Focus: BACKEND_API_URL
```

The skill greps the device repo and the infrastructure repo in parallel: the code default `http://localhost:3000` at `src/agents/main_agent.py:12`, the chart value `http://<mainApp>:3000` at `values.yaml:18`. Naming is consistent and differing defaults are expected (local dev vs in-cluster DNS), so the one real finding is the missing `env.example` entry, cited in the report.

## Related

- `deps-check` — same repo scope, for packages; `gcp-cicd-auth` — cloud credential variables; the project's infra pack skills — pipeline variables matching app expectations; `security-audit` — broader vulnerability sweep.
