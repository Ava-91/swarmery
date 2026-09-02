# deps-check — scan procedure and checks

## Required environment

- Tools/libraries: `npm` (v9+), `pip` / `pip-audit`, `helm` (v3+), network access to package registries
- Allowed tools: Read, Bash, Glob, Grep
- Repos in scope: the project's repos, as listed in `.claude/project.json` → `repos` — typically:
  1. `apps/<mainApp>` (Node.js)
  2. `<device>` (Python)
  3. the infrastructure/chart repo(s)

## Inputs

- `repos: string[]` — List of repos to scan. Defaults to all repos from `project.json → repos`.
- `severity_threshold: "critical" | "high" | "moderate" | "low"` — Minimum severity to include in report. Default: `"moderate"`

## Procedure

1. **Locate dependency files** — For each repo in the input list, find the dependency manifest:
   - `apps/<mainApp>`: `package.json` + `package-lock.json`
   - `<device>`: `requirements.txt`, `requirements-dev.txt`, and `pyproject.toml` (if present)
   - infrastructure/chart repo(s): `charts/*/Chart.yaml` (subchart dependencies)

   Checkpoint: At least one dependency file found per repo. If a repo has no dependency file, log it as a scan gap.

2. **Run outdated checks** — Execute scan commands with error handling. Scans for the Node.js and Python repos are independent — run them as parallel Bash calls. The chart-repo update must complete before chart searches.
   ```bash
   # Node.js (main app) -- can run in parallel with Python scan
   cd apps/<mainApp> && npm outdated --json 2>/dev/null || echo '{"error": "npm outdated failed"}'

   # Python (device repo) -- can run in parallel with Node scan
   cd <device> && pip list --outdated --format=json 2>/dev/null || echo '[]'

   # Charts (all chart repos) -- must run after helm repo update
   helm repo update 2>/dev/null  # Side effect: updates local chart repo cache; requires network
   helm search repo <chart-name> --versions
   ```

   Checkpoint: Each scan command either produced output or an error message was captured.

3. **Run security scans** — Execute vulnerability checks (Node.js and Python scans can run in parallel):
   ```bash
   # Node.js
   cd apps/<mainApp> && npm audit --json 2>/dev/null

   # Python
   cd <device> && pip-audit --format=json 2>/dev/null || echo '{"error": "pip-audit not installed or failed"}'
   ```

   Checkpoint: Security scan output captured for each repo.

4. **Check cross-repo alignment** — Compare shared package versions across repos. For TypeScript packages used in multiple places, verify version consistency.

   Checkpoint: Any version mismatches logged.

5. **Compile report** — Assemble findings into the output template (see `report-template.md`). Sort vulnerabilities by severity (critical first). Include scan failures in the report header. Respect the 200-line length budget.

   Checkpoint: Report assembled with all sections populated (or marked "None found").

6. **Triage recommendations** — For each critical/high vulnerability, check if a patched version exists and note the upgrade path. When recommending an upgrade, consult the package CHANGELOG or migration guide for breaking changes between the current and target versions, and include a post-upgrade testing checklist (typecheck, unit tests, build) plus a rough effort estimate. Do NOT run `npm audit fix` or `pip install --upgrade` automatically.

   Checkpoint: Top 3 recommendations prioritized by severity and fix availability.

## Self-check before returning

- [ ] Every repo in the input list was scanned or its scan failure was reported
- [ ] `helm repo update` side effect was explicitly noted (it modifies the local chart repo cache)
- [ ] No `npm audit fix`, `npm update`, or `pip install --upgrade` was executed (this skill is read-only)
- [ ] Vulnerability severity is reported using the registry's severity level, not a custom scale
- [ ] Cross-repo version mismatches section is populated (even if empty with "None found")
- [ ] Scan failures (command not found, no network, missing lockfile) are listed in the report header
- [ ] Report stays within the 200-line length budget

## Common mistakes to avoid

- DO NOT run `npm audit fix` or `pip install --upgrade` — this skill audits only; upgrades require a separate task
- DO NOT assume `pip-audit` is installed — check first and report if missing
- DO NOT ignore `helm repo update` as a side effect — it mutates the local chart repo cache and requires network access
- DO NOT skip `pyproject.toml` — modern Python projects may declare dependencies there instead of `requirements.txt`
- DO NOT report `npm outdated` warnings as vulnerabilities — outdated is not the same as vulnerable
- DO NOT run scans without capturing stderr — silent failures produce incomplete reports

## What to surface to the user

- Total vulnerability count by severity level
- Any scan that failed (missing tool, no network, no lockfile)
- Cross-repo version mismatches that could cause runtime issues
- The top 3 recommended actions, prioritized by severity and fix availability

## Escalation

- Stop and ask when: A critical CVE is found with no patched version available (requires security team decision)
- Stop and ask when: `npm audit` or `pip-audit` command is not available and cannot be installed
- Stop and ask when: Network access is unavailable (all registry-based scans will fail)
- Stop and ask when: A dependency file is missing from an expected repo (may indicate repo restructuring)

## Failure modes

- Mode: `npm outdated` hangs — symptom: command does not return within 60 seconds — detect: timeout on Bash execution — fix: check network connectivity; run with `--json` flag for faster output
- Mode: `pip-audit` not installed — symptom: `command not found` error — detect: stderr captured — fix: report as scan gap in the audit report; suggest `pip install pip-audit`
- Mode: `helm repo update` fails — symptom: cannot fetch latest chart versions — detect: non-zero exit code — fix: check chart repo configuration (`helm repo list`) and network access
- Mode: Incomplete scan due to missing lockfile — symptom: `npm outdated` cannot determine wanted versions — detect: warning in npm output — fix: report as scan gap; recommend running `npm install` to generate lockfile

## Related skills

- `code-quality` — after deps-check identifies outdated packages, code-quality may review the upgrade PR
- The project's infra pack skills — deps-check should run before a deploy to catch vulnerable dependencies; defer deployment config dependency version authoring to those skills (deps-check only reports current state)
- `env-check` — shares the same repo scope (`project.json → repos`); align repo lists when auditing both dependencies and env vars
