# Core agents — metadata registry

Frontmatter in `agents/*.md` is limited to fields Claude Code actually consumes
(`name`, `description`, `model`, `tools`, `disallowedTools`, `skills`, `memory`,
`isolation`, `maxTurns`, `color`, plus the docgen `docs:` provenance block that
`scripts/docgen/` and the control plane's system registry read). Ownership and
lifecycle metadata evicted from frontmatter in the 2026-09 audit remediation
lives here instead.

`permissionMode` is recorded for history only: Claude Code ignores it on plugin
subagents, so it never had runtime effect. Consumers grant allowances via
`permissions.allow` in their project `.claude/settings.json` — see
`overlays/example/settings.snippet.json`.

| Agent | Owner | Version | Autonomy | Historical permissionMode |
|---|---|---|---|---|
| api-designer | platform-team | 1.0.0 | auto | plan |
| architecture-designer | platform-team | 1.1.0 | auto | plan |
| build-error-resolver | platform-team | 1.0.0 | auto | acceptEdits |
| ci-incident-responder | platform-team | 1.0.0 | auto | plan |
| code-auditor | platform-team | 1.0.0 | semi-auto | plan |
| commit-message | platform-team | 1.0.0 | semi-auto | plan |
| context-gatherer | platform-team | 1.0.0 | auto | plan |
| contract-validator | platform-team | 1.0.0 | semi-auto | plan |
| database-designer | platform-team | 1.0.0 | auto | plan |
| debugger | platform-team | 1.1.0 | auto | acceptEdits |
| downstream-analyzer | platform-team | 1.0.0 | auto | acceptEdits |
| founder-reality-check | — | — | — | default |
| full-stack-feature | platform-team | 1.1.0 | auto | acceptEdits |
| guardrail-checker | platform-team | 1.0.0 | semi-auto | plan |
| implementation-agent | platform-team | 1.2.0 | semi-auto | acceptEdits |
| implementation-planner | platform-team | 1.3.0 | auto | plan |
| migration-agent | — | — | — | plan |
| migration-helper | platform-team | 1.0.0 | auto | acceptEdits |
| performance-monitor | platform-team | 1.0.0 | auto | plan |
| performance-optimizer | platform-team | 1.1.0 | semi-auto | acceptEdits |
| plan-reviewer | platform-team | 1.0.0 | semi-auto | plan |
| post-task-completion | platform-team | 1.0.0 | highly-auto | plan |
| pr-generator | — | — | — | plan |
| prompting-agent | platform-team | 1.0.0 | auto | plan |
| quality-checker | platform-team | 1.1.0 | semi-auto | plan |
| react-specialist | platform-team | 1.1.1 | auto | acceptEdits |
| retrospective-agent | platform-team | 1.0.0 | highly-auto | plan |
| security-auditor | platform-team | 1.1.0 | semi-auto | plan |
| silent-failure-hunter | platform-team | 1.0.0 | semi-auto | plan |
| sprint-review | platform-team | 1.0.0 | auto | plan |
| sre-orchestrator | platform-team | 1.0.0 | semi-auto | plan |
| summary-generator | platform-team | 1.1.0 | semi-auto | acceptEdits |
| system-improver | platform-team | 1.0.0 | highly-auto | plan |
| task-decomposer | — | — | auto | plan |
| task-documenter | platform-team | 1.0.0 | semi-auto | acceptEdits |
| task-planner | platform-team | 1.3.0 | auto | plan |
| tech-lead | platform-team | 1.4.0 | auto | default |
| tech-researcher | platform-team | 1.0.0 | auto | plan |
| test-runner | platform-team | 1.0.0 | semi-auto | plan |
| test-writer | platform-team | 1.1.0 | semi-auto | acceptEdits |
| ui-designer | platform-team | 1.1.1 | auto | acceptEdits |
| verification-agent | platform-team | 1.2.0 | highly-auto | plan |
