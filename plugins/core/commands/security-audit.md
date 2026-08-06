---
description: Quick security audit - find common vulnerabilities and security issues
color: red
docs:
  status: reviewed
  source_sha: cf5c51719359
  updated: 2026-08-06
---

# Security Audit

Static application security audit -- secrets, injection, XSS, auth/authz, dependency CVEs, deployment/infra config, and the project's device-specific surfaces (telemetry-protocol validation, WebSocket auth, media/stream access -- see `project.json → domainTerms`) -- mapped to OWASP Top 10 with severity-ranked findings.

Follow the playbook in `skills/security-audit/SKILL.md` (auto-loaded skill `security-audit`); apply it to $ARGUMENTS if provided.

For CI/CD pipeline hardening, image scanning, or SBOMs use `supply-chain-security` instead.

# How to use

## What it does

Runs a static security audit over your code and configuration, then hands you findings ranked by severity and mapped to the OWASP Top 10. It covers hard-coded secrets, injection, XSS, authentication and authorization gaps, dependency CVEs, deployment and infrastructure config, and any device-facing surfaces your project declares — telemetry-protocol validation, WebSocket auth, media and stream access.

## When to use it

- You are about to open a pull request that touches auth, input handling, or file uploads and want a vulnerability sweep first.
- A reviewer asked whether a module leaks secrets or trusts unvalidated input.
- You inherited a service and need a severity-ranked list of what to fix before it goes live.
- Your project exposes a device or streaming surface and you want its protocol and socket auth checked.

## When not to use it

- For CI/CD pipeline hardening, image scanning, or SBOM generation, use the `supply-chain-security` skill instead.
- For a threat model of a specific feature rather than a code sweep, ask the `security-auditor` agent.
- For a general quality pass — complexity, naming, dead code — use `/code-quality`.

## How to invoke

```
/security-audit
```

Run it bare to audit the whole project, or pass a path or feature name to narrow the scope to that surface.

## Inputs

- Scope — optional. A file, directory, or feature name. Without it, the audit covers the whole project.
- `.claude/project.json` — optional. Its `domainTerms` entry tells the audit which device-specific surfaces belong to your project.

## What you get back

A findings report in the chat. Each finding names the file and line, the OWASP category it maps to, why it is exploitable, and the fix. Findings are ordered by severity so the top of the list is what to fix first. Nothing is written to disk and no code is changed — you decide what to act on.

## Worked example

```
/security-audit orders/line-items
```

The audit reads the route handlers, validation schemas, and queries under that path. You get back something like: a critical finding for a raw SQL string built from a request parameter, a high finding for an endpoint that checks authentication but never authorization, and two medium findings for error responses that echo internal stack traces. Each carries the file, the line, and the concrete change to make.

## Related

- `supply-chain-security` — prefer it for registry, image, and SBOM controls rather than application code.
- `security-auditor` agent — prefer it when you want STRIDE threat modeling on a design, not a scan of existing code.
- `code-quality` — prefer it for maintainability issues that are not security risks.
