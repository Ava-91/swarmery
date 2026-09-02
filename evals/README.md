# swarmery evals

Golden regression tests for critical core agents (promptfoo). Editing
`plugins/core/agents/*.md` must not silently break routing, output contracts,
or vendor-neutrality — these tests make such breakage fail loudly.

```bash
cd evals
export ANTHROPIC_API_KEY=…       # runner (haiku) + judge (sonnet)
npx promptfoo@latest eval        # run the suite
npx promptfoo@latest view        # inspect results in the browser
```

## What's covered (core 3.0)

- **tech-lead** — routing sanity (schema work → architect, bug → debugger first),
  the reviewer-before-commit gate, read-only investigation mode, consulting
  `project.json`/CLAUDE.md instead of assuming a stack, and the 7-cell ledger row.
- **planner** — plan-format contract: `phase-N-<slug>.md` naming, the parseable
  sequencing table, the `## Completion Report` stub, ask-user-first discipline.
- **verification-agent / security-auditor** — the machine verdict grammar
  (`VERDICT: PASS | FAIL | INCONCLUSIVE`) plus honest NOT RUN reporting and
  OWASP finding quality.
- **implementation-agent** — leaf-executor scope discipline and the
  tick-then-Completion-Report progress contract.
- **git-commit skill** — conventional format with scopes from `project.json → commitScopes`.
- **guardrails skill** — APPROVED/REJECTED contract, read-only short-circuit,
  critical actions never auto-approved.
- **jira-task-runner** (jira-pack) — no tracker writes on failed runs;
  needs-info vs cannot-reproduce.

## Growing the corpus

Every real routing bug or contract regression should become a test case here
(same philosophy as unit tests). Prefer `contains`/`regex` for hard contracts and
`llm-rubric` for judgment calls.

## CI

Not wired into CI yet — the suite needs an API key and costs tokens. Wire it as a
manual/nightly job once the corpus stabilizes; the structural checks in
`.github/workflows/ci.yml` (JSON, bash -n, frontmatter, neutrality scan) stay on every push.
