# Migrating to core 3.0

Core 3.0 is a breaking release: the agent roster shrank from 42 agents
(~94k words of 2025-style rule-machine prompts) to **13 judgment-style
agents**, skills moved to progressive disclosure (short `SKILL.md` +
`resources/`), 8 duplicate commands were folded into their same-named skills,
and two hooks were retired in favor of native Claude Code behavior. Consumers
adopt via `/plugin update` as usual.

## Why

The September 2026 audit found the 2.x core built on 2025-era prompt rules
that current models no longer need — fixed phase machines, mandatory
checklists, "minimum N queries" — plus concrete breakage: `permissionMode:`
frontmatter (ignored by Claude Code on plugin subagents), pinned retired
model IDs, and references to docs and skills that didn't exist. 3.0 fixes the
breakage, cuts each agent to ≤500 words of judgment and contracts, and adds a
CI gate (`scripts/validate-agent-refs.sh`) so the dead-reference class cannot
return.

## Removed agents and where their work went

| 2.x agent | 3.0 home |
|---|---|
| full-stack-feature | `@tech-lead` |
| task-planner, implementation-planner, task-decomposer, prompting-agent | `@planner` |
| architecture-designer, api-designer, database-designer | `@architect` |
| migration-agent, migration-helper | `@architect` (design/safety) + `@implementation-agent` (execution); `migration-check` skill |
| tech-researcher, context-gatherer, downstream-analyzer | `@researcher` |
| react-specialist, ui-designer | `@ui-developer` |
| build-error-resolver, ci-incident-responder, performance-monitor, performance-optimizer | `@debugger` |
| quality-checker, code-auditor, plan-reviewer, contract-validator, silent-failure-hunter | `@code-reviewer` (new, read-only) |
| summary-generator, task-documenter, post-task-completion, retrospective-agent | `session-closeout` skill |
| guardrail-checker | `guardrails` skill |
| commit-message | `git-commit` skill |
| pr-generator | `pr-generator` skill |
| sprint-review | `sprint-review` skill |
| founder-reality-check | `founder-reality-check` skill |
| sre-orchestrator | `sre-operations` skill |

If your project depends on a removed agent by name, pin the old definition
project-locally: copy it from the 2.23.2 tag into your `.claude/agents/` —
project-local components override plugin ones by design.

## Contract changes

- **Verdicts.** Verdict-emitting agents (`code-reviewer`, `security-auditor`,
  `verification-agent`, `test-runner`) now end with the machine grammar
  `VERDICT: PASS | FAIL | INCONCLUSIVE` — the line the control plane's verify
  engine parses. The 2.x bespoke tokens (`VERIFICATION:`, `CONTRACTS:`,
  `## Verdict:`) are gone.
- **Restricted agents, named honestly.** `code-reviewer` and
  `security-auditor` are **read-only**: no write tools and no `Bash`, so they
  cannot mutate state at all. They review what they are given plus the tree via
  Read/Glob/Grep, and defer build/test/scanner runs to `@verification-agent` or
  the caller. `researcher`, `test-runner` and `verification-agent` are
  **no-edit**: no write tools, but unscoped `Bash`, because running suites and
  builds is their job. An unscoped `Bash` is a write channel — if you need a
  hard boundary, scope or deny `Bash` in your project's own `permissions`.
  Note that `tools:` frontmatter **cannot** express scoping: it accepts exact
  tool names, `mcp__*` patterns and `Agent(...)` only, so a `Bash(git diff:*)`
  entry there is not a narrow grant — it is not a grant at all. The
  reference-integrity gate now fails on that shape.
- **Frontmatter.** `permissionMode`, `autonomy`, `owner`, `version` removed
  everywhere (the first was always ignored on plugin subagents; consumers
  grant allowances via `permissions.allow` — see
  `overlays/example/settings.snippet.json`). `model:` values are aliases
  (`opus`/`sonnet`/`haiku`) only. Read-only agents carry `tools:` allowlists.
  All of this is CI-enforced by the new reference-integrity gate.
- **Plan format** is unchanged (README sequencing table, `phase-N-<slug>.md`,
  checkbox ticks, `## Completion Report`) and now lives in the
  `workspace-plans` skill; `@planner` produces it, `@implementation-agent`
  executes it.
- **Retrospectives**: the `session-closeout` skill fixes a 2.x bug — the
  retro template's "Improvement Recommendations" heading was never matched by
  the ingester, which wants `## Process Improvements`.

## Minimum Claude Code version

**Core 3.0 requires Claude Code ≥ 2.1.160.** The release deliberately hands
work back to the harness: native read-before-edit (which is why
`read-before-write.sh` could be deleted), the `Agent` hook matcher, and dynamic
workflow orchestration on `@tech-lead`'s large route. On an older build none of
those raise an error — they are simply absent, so the guarantees quietly stop
holding. `session-start.sh` prints a warning when it detects an older build; it
never blocks. `plugin.json` has no minimum-version field to declare this in, so
the check lives in the hook and in this note.

## Commands → skills

Deleted commands (invoke the same-named skill instead — the `/name` form
still works): `code-quality`, `deps-check`, `env-check`, `migration-check`,
`refactor-plan`, `run-plan`, `security-audit`, `test-coverage`.

## Hooks

- `read-before-write.sh` removed — the check is native in Claude Code
  (≥ v2.1.160).
- `activity-tracker.sh` + `session-budget.sh` merged into
  `post-tool-observe.sh` (one process per tool call instead of two; the
  per-call colored activity box is gone, the shared session log and the
  once-per-session budget warning remain).
- `pre-commit-test-gate.sh` and `memory-drift-check.sh` are documented
  opt-ins, not wired by default.
- `subagent-stop.sh` now writes the delegation ledger row itself (3.0.2). It
  derives agent, phase, verdict, loops and artifact from the SubagentStop
  payload — `last_assistant_message` carries the subagent's final text, so the
  `VERDICT:` line and reported artifact are read rather than retyped — and
  appends a 7-cell row to `{task-dir}/logs/agents.md` in the shape
  `internal/wsingest` already parses. `quality` and `mistakes` stay empty:
  they are the orchestrator's judgment. `@tech-lead` no longer instructs the
  model to write the row by hand. The task dir comes from `AGENT_TASK_ID` when
  set, otherwise from `transcript_path`, the same way `session-summary.sh`
  attributes a session to a task.

## Domain packs

Packs were patch-bumped (stale model pins → aliases, frontmatter cleanup,
references updated to the new roster). Pack agents may reference core skills —
that layering is now explicit and CI-checked.
