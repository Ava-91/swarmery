# Summary template structures

All four templates share these common elements:

- Status header
- Items created, modified, deleted
- Quantified metrics (files, lines, duration, coverage)
- Role-specific "How to Use" instructions (developers, QA, PM)
- Actionable next steps with owners and timeframes
- Known issues and limitations

## Template structures

### Task Summary
Status -> Removed items -> Created items -> Modified items -> Details -> Metrics -> How to Use -> Key Changes -> Next Steps -> Known Issues -> Recommendations -> Footer

### Feature Summary
Overview -> User Stories -> Architecture (backend, frontend, integration) -> Files Created/Modified -> Metrics -> How to Use -> Key Features -> Security -> Testing -> Deployment Checklist -> Next Steps -> Known Issues -> Future Improvements

### Bug Fix Summary
Overview (ID, severity, environment) -> Impact -> Root Cause Analysis -> The Fix -> Files Modified -> Testing (reproduction, verification, regression) -> Metrics -> How to Verify -> Prevention Measures -> Related Issues -> Deployment -> Known Limitations -> Lessons Learned

### Refactoring Summary
Overview -> Problems Solved (before/after) -> Architecture Changes -> Files Created/Modified/Deleted -> Metrics (quality, performance, maintainability) -> How to Verify -> Improvements -> Migration Guide -> Testing -> Documentation Updates -> Next Steps -> Future Opportunities -> Known Limitations -> Lessons Learned

## Template file references

Full templates are maintained at:
- `.claude/templates/task-summary-template.md`
- `.claude/templates/feature-summary-template.md`
- `.claude/templates/bug-fix-summary-template.md`
- `.claude/templates/refactoring-summary-template.md`

**Verification:** Before using a template, confirm the file exists at the referenced path. If the file is missing, use the structure described here as the authoritative fallback.

## Relationship to the session-closeout skill

The canonical end-of-task final report — `{task-dir}/SUMMARY.md` with its seven fixed sections — is owned by the `session-closeout` skill and follows that skill's contract, not these templates. Use these templates for per-change-type summaries (a feature write-up, a bug-fix report, a refactoring summary) requested on their own; when closing a task in the workspace, defer to `session-closeout`.

## Output format selection

- **Markdown:** Default for inline use or when the caller specifies plain text. Max 200 lines.
- **HTML:** Use when the summary has >3 sections OR will be shared outside the terminal. Max 300 lines. Self-contained HTML, produced inline. Mapping:

| Markdown section | HTML equivalent |
|-----------------|-----------------|
| Status header | `<h1>` + status badge |
| Metrics table | `<table class="metrics">` |
| "How to Use" per role | `<details>` collapsible per role |
| Next Steps list | `<ul>` with `<input type="checkbox">` |
| Known Issues | `<div class="card" style="border-color:#7f1d1d">` |
| Positive findings | `<div class="card" style="border-color:#065f46">` |

If sections exceed the length budget, prioritize: summary, metrics, next steps, known issues. Omit passing checks unless the user requests a full compliance report.

## Project domain context

When summarizing platform work, use the project's domain terminology (see `.claude/project.json` -> `domainTerms` and the project's `CLAUDE.md`):
- Use the project's device noun (not a generic "device" or "node") when the project defines one
- Use the project's workflow nouns (e.g., "Mission", not "job" or "task", if that is the domain term)
- "Telemetry" (not "metrics") when referring to device data streams
- Use the project's name for the edge/gateway component (project.json -> `device`)
- Reference actual repo names from project.json -> `repos`

## Worked examples

### Feature summary for mission management

**Scenario:** Implemented mission CRUD with REST route handlers, a React UI, and PostgreSQL storage via the project's ORM.

**Step 1 reasoning:** This introduces wholly new functionality (mission CRUD). Template: Feature Summary.

**Key sections:**
- User Stories: "As an operator, I can create a new patrol mission with waypoints"
- Architecture: Route handlers at `src/app/api/missions/`, ORM schema extension, MissionCard component
- Security: session checks, schema validation for mission parameters
- Testing: 15 unit tests, 5 integration tests, 3 E2E tests

### Bug fix summary for telemetry reconnection

**Scenario:** Fixed telemetry SSE stream not reconnecting after a device-gateway pod restart.

**Step 1 reasoning:** This fixes a specific defect (SSE reconnection failure) with a root cause (missing retry logic). Template: Bug Fix Summary.

**Key sections:**
- Severity: High
- Root Cause: EventSource `onerror` handler was closing the connection without scheduling a reconnect
- The Fix: Added exponential backoff retry logic in `useTelemetry` hook
- Prevention: Added E2E test simulating device-gateway disconnect/reconnect

### Refactoring summary for auth middleware

**Scenario:** Extracted auth checks from individual route handlers into shared middleware.

**Step 1 reasoning:** This restructures existing code (auth check extraction) without changing behavior. Template: Refactoring Summary.

**Key sections:**
- Problems Solved: Duplicated `await auth()` + session check in 14 route handlers
- Architecture: Before (inline checks) -> After (shared `withAuth()` wrapper)
- Metrics: Reduced auth-related code from 14 locations to 1 shared module
- Migration Guide: Replace `const session = await auth()` pattern with `export const GET = withAuth(handler)`
