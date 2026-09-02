# Sprint review — full audit process

## Phase 0 — Scope (always first, never skipped)

Per repo, in parallel:

```bash
SINCE="${SINCE:-14 days ago}"
git log --since="$SINCE" --oneline
git diff --stat "$(git rev-list -1 --before="$SINCE" HEAD)" HEAD
git shortlog -sn --since="$SINCE"
```

Record commit count, contributors, changed-file list, lines +/-. Skip repos
with zero commits (mark them skipped with reason). The changed-file list from
this phase scopes EVERY later step — unscoped audits produce unbounded review
lists.

## Phase 1 — Fan-out reviews (main-agent mode only)

Spawn in a single message, each briefed with the repo path, the scoped
changed-file list, "return severity + file:line findings", and "change
nothing":

- @code-reviewer — once per leading lens: quality/complexity, silent
  failures, contract alignment.
- @security-auditor — OWASP, secrets, auth regressions.

Track per-subagent status `{agent: COMPLETE | FAILED | SKIPPED}`. When running
as a subagent (cannot spawn), run the passes directly and note "fan-out
skipped — running as subagent" in the report.

## Phase 2 — Style & types (check-only)

Lint / typecheck / format-check per stack. Report drift; NEVER autofix
(`--fix`, `--write`, `make format` are all forbidden here).

## Phase 3 — Tests (sequential)

Run suites; record pass/fail/skip counts. Cross-check failures against the
Phase 0 changed files: a failure in a file changed this window is a BLOCKER;
classify a failure as pre-existing only after checking blame against the
window. A hanging suite is killed after 5 minutes and marked SKIPPED.

## Phase 4 — Aggregate report

Location: `<workspace>/<project>/workspace/working/{YYYY}/{MM}/sprint-review-{YYYY-MM-DD}.md`
(month level — not a task dir).

Structure:

```markdown
# Sprint review {date} (window: {since} → {today})

## Verdict: PASS | FAIL | PASS WITH BLOCKERS

## Blockers            ← first when any exist
- BLOCKER {file:line} — {finding} (source: {agent/check})

## Activity
| Repo | Commits | Files | +/- | Contributors |

## Findings by severity
BLOCKER / MAJOR / MINOR / INFO — each with file:line and producing check

## Checks
| Check | Result | New vs pre-existing |

## Subagents
{agent}: COMPLETE | FAILED | SKIPPED
```

Verdict rules: any BLOCKER → FAIL or PASS WITH BLOCKERS (FAIL when a blocker
is a new test failure or a P0 security finding); zero findings above MINOR →
PASS. FAIL / PASS WITH BLOCKERS are human gates before release.

## Invariants

- Zero source modifications (verify with `git status` per repo at the end).
- Every finding cites file:line and its producing check.
- Findings below 80% confidence tagged `[LOW-CONFIDENCE]`.
- P0 security findings are flagged immediately, not held for the report.
