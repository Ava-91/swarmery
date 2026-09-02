# Core agents — roster and metadata registry

Core 3.0.0 consolidated the roster from 42 overconstrained agents (~94k words)
to 13 judgment-style agents (2026-09 audit remediation). Agent frontmatter is
limited to fields Claude Code actually consumes (`name`, `description`,
`model`, `effort`, `tools`, `disallowedTools`, `skills`, `memory`,
`isolation`, `maxTurns`, `color`, plus the docgen `docs:` provenance block).
Ownership/lifecycle metadata lives here, not in frontmatter.

`permissionMode` was removed everywhere: Claude Code ignores it on plugin
subagents, so it never had runtime effect. Consumers grant allowances via
`permissions.allow` in their project `.claude/settings.json` — see
`overlays/example/settings.snippet.json`.

## Roster (13)

| Agent | Class | Model | Role in one line |
|---|---|---|---|
| tech-lead | orchestrator | opus | Understand → route by size → independent review gate → close with summary |
| planner | planning | opus | Workspace plans: phase docs, falsifiable criteria, executor prompts |
| architect | design | opus | System/API/data design with trade-offs, migration safety, rollout |
| researcher | read-only | sonnet | Tech evaluation, impact analysis, context synthesis with citations |
| implementation-agent | executor | opus | Leaf code execution (step_file) or plan-execution orchestration (task_dir) |
| ui-developer | executor | sonnet | Frontend components with type/token/a11y/state gates |
| debugger | executor | sonnet | Root cause first, minimal proven fix (bugs, builds, CI, perf) |
| test-writer | executor | sonnet | Behavior-pinning tests in the project's stack |
| test-runner | read-only | haiku | Execute suites, report faithfully, VERDICT line |
| code-reviewer | read-only | opus | Multi-lens review (correctness, silent failures, contracts, plan, quality), VERDICT line |
| security-auditor | read-only | opus | OWASP + STRIDE with attack paths, VERDICT line |
| verification-agent | read-only | haiku | Deterministic checks (build/type/lint/test), VERDICT line |
| system-improver | meta | opus | Evidence-cited diagnosis of the whole agent system from a retro digest |

Read-only-class agents carry a `tools:` allowlist without Edit/Write/NotebookEdit.
Verdict-emitting agents end with the platform grammar `VERDICT: PASS | FAIL |
INCONCLUSIVE` (the only grammar `tools/swarmery/internal/verify` parses).

## Where the removed 34 went (2.x → 3.0 mapping)

| Removed agent | Now |
|---|---|
| full-stack-feature | @tech-lead (routing judgment) |
| prompting-agent | @planner (executor prompts are part of the plan) |
| task-planner, implementation-planner, task-decomposer | @planner |
| architecture-designer, api-designer, database-designer | @architect |
| migration-agent, migration-helper | @architect (design/safety) + @implementation-agent (execution); checklist in `migration-check` skill |
| tech-researcher, context-gatherer, downstream-analyzer | @researcher (context breadth → built-in Explore) |
| react-specialist, ui-designer | @ui-developer |
| build-error-resolver, ci-incident-responder, performance-monitor, performance-optimizer | @debugger |
| quality-checker, code-auditor, plan-reviewer, contract-validator, silent-failure-hunter | @code-reviewer |
| summary-generator, task-documenter, post-task-completion, retrospective-agent | `session-closeout` skill |
| guardrail-checker | `guardrails` skill (APPROVED/REJECTED contract preserved) |
| commit-message | `git-commit` skill |
| pr-generator | `pr-generator` skill |
| sprint-review | `sprint-review` skill |
| founder-reality-check | `founder-reality-check` skill |
| sre-orchestrator | `sre-operations` skill |

A consumer that depended on a removed agent name can pin the old definition
project-locally (`.claude/agents/<name>.md`) — project-local components
override plugin ones by design.

## Metadata

| Agent | Owner | Since |
|---|---|---|
| tech-lead | platform-team | 1.0 (rewritten 3.0) |
| implementation-agent | platform-team | 1.0 (rewritten 3.0) |
| debugger | platform-team | 1.0 (rewritten 3.0) |
| security-auditor | platform-team | 1.0 (rewritten 3.0) |
| verification-agent | platform-team | 1.0 (rewritten 3.0) |
| test-writer | platform-team | 1.0 (rewritten 3.0) |
| test-runner | platform-team | 1.0 (rewritten 3.0) |
| system-improver | platform-team | 2.20 |
| code-reviewer | platform-team | 3.0 |
| researcher | platform-team | 3.0 |
| planner | platform-team | 3.0 |
| architect | platform-team | 3.0 |
| ui-developer | platform-team | 3.0 |
