---
name: security-audit
version: "1.0.0"
owner: "swarmery-core"
description: "Scan application code for security vulnerabilities, OWASP Top 10 compliance, or hardcoded secrets across the project's repos. NOT for CI/CD hardening or SBOM generation (use supply-chain-security)."
disable-model-invocation: true
allowed-tools: Read, Grep, Glob, Bash
color: teal
docs:
  status: reviewed
  source_sha: 7aabe12b3053
  updated: 2026-08-06
---

# Purpose

Perform a static security audit of the project's application code, configuration, and infrastructure manifests, producing a structured report where every finding carries a file:line citation, OWASP category, severity, and concrete remediation. Covers injection, auth bypass, secrets leakage, insecure configuration, and dependency CVEs; pipeline hardening, image scanning, and SBOMs belong to `supply-chain-security`.

# Rules (never violate)

1. Read-only: never modify source files, install packages, or fix findings during an audit.
2. Redact any discovered secret with `***` in the report — never print credentials in plain text.
3. Every finding needs a file:line citation, a severity (Critical/High/Medium/Low), and an OWASP category.
4. Flag Critical findings (auth bypass, RCE, secrets in git) immediately, before the full report.
5. Mark inferred or uncertain findings `[LOW-CONFIDENCE]`; do not report guesses as facts.
6. Use `npm audit --json` (never without `--json` — it may write `package-lock.json`).

# Resources

- Read `resources/owasp-checklist.md` at the start of every audit — the authoritative OWASP Top 10 per-category checklist (map items onto the project's actual stack, `project.json → stack`).
- Read `resources/audit-procedure.md` when running the audit: the 7-step procedure (secrets, injection, auth, device/realtime transport, dependencies, infrastructure), report template and length budgets, self-check, escalation, examples, failure modes.

# How to use

## What it does

Runs a read-only static security audit over application code, configuration, and deployment manifests: it walks the OWASP Top 10, hunts hardcoded secrets, injection paths, weak auth, vulnerable dependencies, and unsafe infrastructure settings, then hands you a structured report with a prioritized action plan and a scope-coverage list.

## When to use it

- Someone asks to audit, scan, or review code for security vulnerabilities.
- A new feature or module needs a security review before merge.
- A quick secrets pass on the current branch; a suspected auth bypass or injection risk; an OWASP compliance picture for a repo.

Not for: pipeline hardening/SBOMs (`supply-chain-security`), pen-testing or fuzzing, plain quality review (`code-standards`/`code-quality`), repro tests (`testing`), or applying fixes (the project's infra pack skills).

## How to invoke

```
Skill(skill: "core:security-audit")
```

State the scope (repo or module path, required) and optionally a depth: `quick` (secrets + critical injection, ≤80 lines) or `full` (all ten OWASP categories, ≤250 lines, default).

## Worked example

```
Skill(skill: "core:security-audit")
Scope: apps/<mainApp>/src/app/api/orders/
```

The skill loads the bundled OWASP checklist, checks the auth middleware on the route, looks for injection in query parameters, verifies schema validation and session checks, then reports — e.g., one High finding for a missing session check at `orders/route.ts:42` mapped to A01 Broken Access Control, with the exact code change that fixes it.

## Related

`supply-chain-security` (images/SBOMs), `testing` (repro/prevention tests), the project's infra pack skills (service, TLS, network config remediation), `troubleshooting` (live incidents).
