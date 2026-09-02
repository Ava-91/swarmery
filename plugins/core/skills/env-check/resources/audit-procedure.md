# env-check — audit procedure and checks

## Required environment

- Tools/libraries: Read, Grep, Glob (no write access)
- Repos in scope: the project's canonical repo list from `.claude/project.json` → `repos`, plus the device/edge repo (`device`) if the project has one. Typical shape: the main app, other apps, the infrastructure repo, and the device repo.

## Inputs

- `repos: string[]` — list of repository root paths to audit (default: the project's full repo list above)
- `focus: string` (optional) — specific env var name or prefix to audit (e.g., `NEXT_PUBLIC_`, `MOCK_MODE`)

## Procedure

1. **Glob for env-related files** — Use patterns `**/.env*`, `**/env.example`, `**/values*.yaml`, `**/*.populated.yaml`, `**/env/server.ts`, `**/env/client.ts`.
   Glob operations across different repos are independent — run all repo scans as parallel tool calls.
   Checkpoint: at least one env file found per repo.

2. **Grep for env var usage per repo** (run all repo greps in parallel):
   - Python repos (e.g., the device/edge repo): `os.environ.get(`, `os.getenv(` in `src/`
   - Node/Next.js apps (e.g., the main app): `process.env.` in `src/`, `NEXT_PUBLIC_` in `src/` (incl. access via `getServerEnv()` / `clientEnv` helpers)
   - Infrastructure / service config repos: `env:` sections and `{{ .Values.* }}` references in `templates/`, keys in `values.yaml`
   - Exclude `node_modules/`, `.next/`, `__pycache__/`, `venv/`
   Checkpoint: count of unique var names per repo.

3. **Cross-reference** — Compare defined vars vs used vars vs documented vars. Documentation sources: `env.example`, plus README.md / CLAUDE.md / setup guides mentions. Flag: (a) used but not in `env.example`, (b) in `env.example` but never used, (c) inconsistent naming across repos for the same logical var.
   Checkpoint: cross-reference table populated.

4. **Security check** — Flag hardcoded strings that look like secrets (API keys, passwords, tokens) in source code. Verify sensitive vars use the runtime's secret manager (not committed `values.yaml`). Verify `*.populated.yaml` files are in `.gitignore`. NEVER print actual secret values in the report — only flag presence and file:line location.
   Checkpoint: security findings logged.

5. **Detect dynamic access** — Grep for `process.env[` (bracket notation) and `os.environ[` patterns. Mark these as low-confidence findings since the var name cannot be resolved statically.
   Checkpoint: dynamic access patterns counted.

6. **Compile report** — Assemble findings into the output template (see `output-template.md`). Every finding must include `file:line` citation. Respect the 150-line length budget.
   Checkpoint: report complete, all sections present.

## Known variables (reference baseline)

Build the expectation set from the project itself — the `env.example` files in each repo, the project's `CLAUDE.md`, and any setup guides. Typical shapes to expect (always verify against current code, never assume exhaustive):

- **Device/edge repo:** mock/simulation toggles, device identity, backend API URL, WebSocket/HTTP ports
- **Main web app:** `NEXT_PUBLIC_*` client keys, `DATABASE_URL`, auth provider vars (`NEXTAUTH_*` / `AUTH_*`), cache/queue URLs
- **Service-config values keys:** replica counts, `image.tag`, ingress settings, DNS suffixes

## Self-check before returning

- [ ] Every finding includes a `file:line` citation
- [ ] No actual secret values appear anywhere in the report
- [ ] Dynamic env access patterns flagged as low-confidence, not reported as definitive
- [ ] `node_modules/`, `.next/`, `__pycache__/`, test fixtures excluded from scan
- [ ] Cross-repo consistency checked (same logical var has same name across repos)
- [ ] Report includes confidence level (HIGH if no dynamic patterns, MEDIUM otherwise)
- [ ] Report stays within the 150-line length budget

## Common mistakes to avoid

- DO NOT print secret values in the report — only flag file:line locations where secrets appear
- DO NOT report env vars found only in test fixtures or mock files as "missing" — test code intentionally uses placeholders
- DO NOT scan `node_modules/`, `.next/`, or other generated directories — they contain false positives
- DO NOT treat `process.env[dynamicKey]` as a specific missing var — flag as low-confidence dynamic access
- DO NOT assume `NEXT_PUBLIC_*` vars are server-side — they are client-exposed and have different security implications

## What to surface to the user

- File paths and line numbers for every finding
- Which repos were scanned and which were skipped (with reason)
- Any env vars that exist in one repo but are missing from the corresponding service-config `values.yaml`
- Any `*.populated.yaml` files that are NOT in `.gitignore`

## Escalation

- Stop and ask when: a `*.populated.yaml` file containing apparent secrets is NOT in `.gitignore` (potential secret leak)
- Stop and ask when: more than 5 env vars are used in code but absent from all documentation (may indicate a documentation backlog vs actual bugs)
- Stop and ask when: dynamic env access patterns account for >30% of env var usage (static analysis unreliable)

## Failure modes

- **False positives from generated code**: symptom: hundreds of env var "findings" from `.next/` or `node_modules/` -> detect: finding count >50 for a single repo -> fix: verify exclusion patterns are applied, re-run scan
- **Dynamic access missed**: symptom: report says "all vars documented" but deployment fails with missing var -> detect: check for `process.env[` bracket patterns -> fix: grep for bracket notation, add to low-confidence section
- **Secret value leaked in report**: symptom: actual API key visible in report output -> detect: grep report text for patterns like `sk-`, `AIza`, base64 strings -> fix: immediately delete the report, re-run with value-scrubbing check, notify user

## Related skills

- `gcp-cicd-auth` — defer to this skill for GCP-specific credential variables and Workload Identity Federation setup
- The project's infra pack skills — compose when CI/CD pipeline variables need to match application env vars, or when verifying that runtime values match application expectations
- `deps-check` — shares the same canonical repo scope; align repo lists when auditing both env vars and dependencies
