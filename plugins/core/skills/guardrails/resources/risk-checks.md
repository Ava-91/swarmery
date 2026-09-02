# Guardrails — risk matrix and per-action checks

## Impact × Reversibility → risk level

| Impact \ Reversible | Yes | No |
|---|---|---|
| Low (dev-only, additive, scoped) | Low | High |
| High (shared env, existing data, user-facing) | Medium | High |
| Critical (prod data, credentials, irreversible destruction) | Critical | Critical |

- **Low / Medium** — proceed after deterministic checks pass; record the
  rollback line.
- **High** — proceed only with an explicit, tested rollback plan and a stated
  blast radius; prefer splitting into reversible stages (expand → migrate →
  contract).
- **Critical** — never auto-approve. Escalate to the user with the assessment
  and the safest alternative.

## Per-action guidance

| Action class | Typical impact | Deterministic checks | Rollback shape |
|---|---|---|---|
| Additive migration (CREATE TABLE/COLUMN nullable) | Low | migration dry-run, typecheck, tests | `DROP TABLE/COLUMN IF EXISTS` |
| Destructive migration (DROP/NOT NULL/type change) | High–Critical | dry-run + row-count evidence + backup confirmed | expand/contract plan; restore path named |
| Bulk file delete/rename in repo | Low–High | build + tests after a scoped dry list | `git revert <sha>` |
| Deploy/infra config change | High | config linter, diff review, staged env first | previous config version redeploy |
| Force git operations | High | never on shared branches | reflog reference recorded BEFORE |
| Credential/permission changes | Critical | — | escalate; humans own these |

## Output contract

Artifact (when a task dir is in play):
`{task-dir}/phases/guardrail-{action-slug}.md` with the risk assessment, the
check table (`PASS|FAIL|SKIP` + command), and the recommendation. Final line:

```
GUARDRAIL: {APPROVED|REJECTED} | Risk: {level} | Checks: {pass/fail/skip} | Rollback: {one line}
```

Conditions attached to APPROVED are binding: an executor that cannot satisfy
a condition treats the action as REJECTED and reports back.
