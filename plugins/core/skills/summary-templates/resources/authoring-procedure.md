# Summary authoring procedure

## Inputs

| Input | Required | Description |
|-------|----------|-------------|
| Work type | Yes | One of: task, feature, bug-fix, refactoring |
| Work details | Yes | What was done, files changed, metrics available |
| Output format | No | `markdown` (default) or `html` (for summaries with >3 sections or shared outside terminal) |

If the work type is ambiguous (e.g., a refactor that also fixes a bug), reason about it in Step 1 before selecting a template. No special tooling is needed; HTML output is produced self-contained, inline.

## Procedure

### 1. Reason about work type

Before selecting a template, determine the primary nature of the work:
- Does the work introduce wholly new functionality? -> Feature Summary
- Does the work fix a specific defect with a root cause? -> Bug Fix Summary
- Does the work restructure existing code without changing behavior? -> Refactoring Summary
- Does the work fall outside the above categories (docs, config, tooling)? -> Task Summary

If the work spans two categories (e.g., a refactor that also fixes a bug), select the template matching the primary intent. If still ambiguous, ask the user.

Checkpoint: Work type determined; template selected with rationale.

### 2. Gather data
Collect from the user or from the completed work:
- Files created, modified, deleted (with counts)
- Measurable metrics (response time improvements, coverage delta, line counts)
- Known issues or limitations
- Next steps with owners

Checkpoint: Data collected; any gaps noted as N/A.

### 3. Fill the template
- Fill all sections. Mark sections without data as `N/A` rather than omitting them.
- **Do not invent metrics.** If data is unavailable, write `N/A -- measure post-deploy` instead of fabricating numbers.
- **Do not include sensitive data** (credentials, PII, internal API keys) in summary sections.
- Use specific numbers: "Updated 12 files: 5 created, 7 modified" not "Updated many files."

Checkpoint: All sections filled or marked N/A.

### 4. Choose output format
Markdown by default; HTML when the summary has >3 sections or will be shared outside the terminal. See the format-selection table in `template-structures.md`.

Checkpoint: Format selected; output rendered.

### 5. Add project domain context
Apply the project's domain terminology per the "Project domain context" section of `template-structures.md`.

Checkpoint: Domain terminology applied.

### 6. Verify length budget
Count the output lines. If the output exceeds the length budget (200 lines markdown, 300 lines HTML), trim lower-priority sections (passing checks, verbose sub-items) until within budget.

Checkpoint: Output within length budget.

## Self-check before returning

- [ ] Correct template selected for the work type (with reasoning documented in Step 1)
- [ ] All sections filled out or marked N/A
- [ ] Metrics are quantified with numbers, not vague descriptions
- [ ] "How to Use" section exists with role-specific instructions
- [ ] Next steps are actionable with owners assigned
- [ ] Known issues documented (or explicitly stated as none)
- [ ] No metrics were fabricated -- all numbers come from actual data
- [ ] No sensitive data (credentials, PII) included
- [ ] Date is current
- [ ] Template file existence was verified before loading (if using external template files)
- [ ] Output stays within length budget (200 lines markdown / 300 lines HTML)

## Common mistakes to avoid

- **Inventing metrics** -- "Improved performance by 60%" with no measurement is worse than "N/A -- measure post-deploy"
- **Filling "TBD" placeholders with fabricated numbers** -- leave them as TBD or N/A
- **Including sensitive data** -- API keys, database passwords, PII must never appear in summaries
- **Vague next steps** -- "Test the feature" is not actionable; "QA testing of waypoint editing happy path and boundary cases -- [QA Team] -- by 2026-05-26" is actionable
- **Modifying source files while generating a summary** -- this skill produces summary documents only
- **Skipping template selection reasoning** -- always document why a template was chosen, especially for ambiguous work types

## What to surface to the user

- The selected template and why it was chosen
- The output format (markdown or HTML)
- Any sections marked N/A due to missing data, so the user can fill them in
- If the work type was ambiguous, state which template was chosen and why

## Escalation

- **Ambiguous work type** (e.g., refactor + bug fix): Reason about primary intent in Step 1; if still unclear, ask the user
- **Missing metrics data:** Mark as N/A with a note to measure post-deploy; do not block summary creation
- **External template file missing:** Fall back to the structure described in `template-structures.md`
- **Summary requires cross-repo scope** (changes spanning multiple repos from project.json -> `repos`): Use the Task Summary template with a per-repo breakdown section

## Failure modes

| Failure | Recovery |
|---------|----------|
| User provides no metrics | Mark all metric fields as N/A; note in the summary that metrics should be measured post-deploy |
| Template file at `.claude/templates/` is missing | Use the structure documented in `template-structures.md` as authoritative fallback |
| Work type does not fit any template | Use Task Summary as the catch-all default |
| Summary is requested for work not yet completed | Ask the user to confirm scope; fill completed sections and mark remaining as "In Progress" |

## Related skills

- **session-closeout** -- owns the canonical end-of-task `SUMMARY.md` and retrospective; defer to it when closing a workspace task
- **troubleshooting** -- contains the incident postmortem template (different from a work summary)
- **testing** -- for writing tests referenced in summary testing sections
- **code-standards** -- for code review findings that may feed into summary recommendations
