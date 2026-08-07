---
description: Check dependency versions across all project repositories
color: red
docs:
  status: reviewed
  source_sha: e74d97d92726
  updated: 2026-08-06
---

# Dependencies Check

Read-only dependency audit across the project's canonical repos (`project.json → repos`) -- outdated packages, security vulnerabilities (npm audit / pip-audit), cross-repo version mismatches, and upgrade recommendations with breaking-change notes.

Follow the playbook in `skills/deps-check/SKILL.md` (auto-loaded skill `deps-check`); apply it to $ARGUMENTS if provided.

This audits only -- it never runs `npm audit fix`, `npm update`, or `pip install --upgrade`.

# How to use

## What it does

It audits the dependencies of every repository listed in your project config and tells you what is out of date, what has a known vulnerability, and where two repos pin different versions of the same package. You get upgrade recommendations with breaking-change notes, so you can decide what is safe to bump. Nothing is installed or modified — the audit is strictly read-only.

## When to use it

- Before planning an upgrade sprint, when you need to know how far behind the project has drifted.
- After a security advisory lands and you want to know which repos are exposed.
- When two repos in the same workspace behave differently and you suspect a version mismatch.
- As a periodic health check on a multi-repo workspace you do not touch every day.

## When not to use it

- To actually apply upgrades — this command never runs an update or fix command; do the bumps yourself or through your usual migration workflow.
- To audit application source code for vulnerabilities — reach for a security-audit command instead.
- To check environment variables or config drift across repos — that is a different check entirely.

## How to invoke

```
/deps-check
/deps-check apps/<mainApp>
```

Run it with no arguments to audit every canonical repo, or pass a path or package name to narrow the audit to that scope.

## Inputs

- **scope** — a repo path, directory, or package name to focus the audit on — optional. With no argument, all repos from the project's `repos` config are audited.
- **project config** — the repository list comes from your project's `project.json`; you supply nothing extra for this.

## What you get back

A report in the conversation covering outdated packages, vulnerabilities found by the package-manager audit tools, cross-repo version mismatches, and prioritised upgrade recommendations with breaking-change notes. No files are written and no dependencies change on disk.

## Worked example

```
/deps-check

→ scans each repo from project.json → repos
→ collects outdated packages and runs the audit tool per ecosystem
→ compares shared package versions across repos

You end up with: a list of vulnerable packages ranked by severity, the
packages pinned at different versions in different repos, and a suggested
upgrade order noting which bumps carry breaking changes.
```

## Related

- `/env-check` — prefer it when the question is about environment variables rather than package versions.
- `/security-audit` — prefer it when you need to find vulnerabilities in your own code, not in third-party packages.
- `/migration-check` — prefer it when the concern is database schema and migration consistency.
