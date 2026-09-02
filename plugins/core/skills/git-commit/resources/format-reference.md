# Commit Format Reference

## Type table

| Type | Use for | Example |
|------|---------|---------|
| `feat` | new capability | `feat(app): add mission approval page` |
| `fix` | bug fix | `fix(device): handle telemetry reconnect jitter` |
| `docs` | docs only | `docs(ci): document the staging promotion flow` |
| `refactor` | structure change, no behavior change | `refactor(app): split telemetry state adapter` |
| `test` | tests only | `test(app): cover approval failure state` |
| `ci` | pipeline/CI changes | `ci(infra): add staging deploy verification job` |
| `build` | build/package/image changes | `build(app): update container build args` |
| `perf` | performance improvement | `perf(device): reduce telemetry serialization overhead` |
| `chore` | maintenance, no behavior change | `chore(versions): refresh current and previous digests` |

## Scope table

The authoritative scope list is project-specific: read `.claude/project.json` -> `commitScopes`. Illustrative defaults:

| Scope | Meaning |
|-------|---------|
| `app` | the main app (project.json -> `mainApp`) |
| `infra` | the infrastructure repo |
| `device` | the device/edge repo (project.json -> `device`) |
| `versions` | the version-pinning repo, if the project uses one |
| `auth` | auth flow (OIDC / sessions) |
| `telemetry` | telemetry pipeline or stream handling |
| `docs` | cross-repo docs changes |
| `db` | database migrations |

### Deprecated scopes

Projects that migrated stacks may document deprecated scopes (e.g., `be`, `fe`, `helm`). Check the project's `CLAUDE.md`; never use a deprecated scope -- map it to its documented replacement.

## Subject rules

- Imperative mood ("add", not "adds" or "added")
- Lowercase first word
- No trailing period
- Mention the user-visible change, not just the file touched
- Max 72 characters

## Multi-scope commits

When a single logical change spans multiple repos, create one commit per repo with its own scope. Do not combine scopes like `feat(app,infra)`. Cross-reference with a shared description.

## Common mistakes to avoid

- DO NOT use scopes the project has deprecated -- map them to their documented replacements
- DO NOT write "Updated file X" as the subject -- describe the user-visible effect
- DO NOT combine multiple scopes in one commit message -- create separate commits per repo
- DO NOT generate commit messages when staged files include secrets (`.env`, `*.populated.yaml`, `credentials.json`, key/pem files) -- refuse and instruct the user to unstage them
- DO NOT omit `BREAKING CHANGE:` footer when the change breaks existing APIs, CLI flags, or port assignments

## Examples

<example name="single-scope-feature">
```
feat(app): add real-time telemetry panel to device detail page

- Create TelemetryPanel component with SSE subscription
- Add useTelemetry hook with auto-reconnect
- Display battery, altitude, heading, and GPS fix status
```
</example>

<example name="bug-fix-with-body">
```
fix(device): prevent WebSocket reconnection loop on gateway restart

FrontendDataAggregatorHandler was creating new connections
without closing previous ones. Added connection state tracking
and cleanup in the disconnect handler.
```
</example>

<example name="ci-change">
```
ci(infra): add staging rollback verification step

- Run smoke checks after the deploy completes
- Block promotion to staging if verification fails
```
</example>

<example name="breaking-change">
```
build(infra): bump device-gateway chart to v0.3.0

- Add EXTERNAL_DEVICE_DNS env var to values.yaml
- Update NodePort range to 30100-30112

BREAKING CHANGE: NodePort base changed from 30080 to 30100
```
</example>

## Failure modes

| Failure | Detect | Fix |
|---------|--------|-----|
| Wrong scope used | Commit history shows a deprecated scope for main-app changes | Use interactive rebase to amend if not yet pushed; note for future commits |
| Subject too vague | Subject says "update code" or "fix bug" | Rewrite to describe the user-visible effect |
| Missing BREAKING CHANGE footer | Diff contains port changes, API changes, or removed exports without footer | Add `BREAKING CHANGE:` footer describing what breaks and the migration path |

## Related skills

- The project's infra pack skills -- use for pipeline YAML changes (commit messages use the `ci` type with the infra scope) and for the promotion flow (version-pin commits use the `chore(versions)` scope).
