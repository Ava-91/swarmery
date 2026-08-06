---
description: Check environment variables across repos - find missing, unused, or undocumented vars
color: red
docs:
  status: generated
  source_sha: 4fd6e382fc43
  updated: 2026-08-06
---

# Environment Variables Check

Static cross-repo env var audit -- missing, unused, undocumented, or inconsistently named variables plus hardcoded-secret flags, with file:line citations.

Follow the playbook in `skills/env-check/SKILL.md` (auto-loaded skill `env-check`); apply it to $ARGUMENTS if provided.

For live runtime env introspection use the platform's exec/describe tooling (e.g. `kubectl exec`, `gcloud run services describe`), not this command.

# How to use

## What it does

It audits environment variables across all the repositories in your project, without running anything. You get a static report of variables that code reads but no `.env.example` documents, variables documented but never used, names that drift between repos (`API_URL` in one, `API_BASE_URL` in another), and secrets that look hardcoded in source. Every finding comes with a `file:line` citation so you can jump straight to it.

## When to use it

- You are onboarding a new repo into the project and want to know which variables a fresh checkout actually needs before it will boot.
- A deploy failed on a missing variable and you want to find every other undocumented one in the same sweep.
- You are cleaning up config debt and need the list of variables that are documented but no longer read anywhere.
- You suspect a credential was committed inline instead of being read from the environment.

## When not to use it

- You want the values a running service currently holds — use the platform's own tooling (`kubectl exec`, `gcloud run services describe`), which reads live state this command never touches.
- You are hunting general vulnerabilities rather than config drift — `/security-audit` covers the broader class.
- You want to find flags and config keys that are referenced but dead — `/sweep-stale-flags` targets that specific shape.

## How to invoke

```
/env-check
```

Run it with no arguments to sweep every repository in the project. Pass a path or a repo name after the command to narrow the audit to one area.

## Inputs

- Scope argument — a repo name, directory, or file path to limit the audit — optional; defaults to all project repositories.

## What you get back

A report in the conversation, grouped by finding type: missing, unused, undocumented, inconsistently named, and hardcoded-secret flags. Each entry cites the `file:line` where it was found. Nothing is written to disk and no files are modified.

## Worked example

```
/env-check apps/<mainApp>

→ Missing from .env.example:  DATABASE_POOL_SIZE (src/db/client.ts:14)
→ Documented but unused:      LEGACY_CACHE_TTL (.env.example:22)
→ Naming inconsistency:       API_URL here vs API_BASE_URL in the worker repo
→ Possible hardcoded secret:  src/config/mail.ts:31
```

You end up with a short punch list: four items to fix, each with the exact file and line to open.

## Related

- `/security-audit` — prefer it when you want a broad vulnerability pass rather than a config-only audit.
- `/sweep-stale-flags` — prefer it for feature flags and config keys that code references but settings never define.
- `/deps-check` — the same cross-repo sweep shape, applied to dependency versions instead of environment variables.
