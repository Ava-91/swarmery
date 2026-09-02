# phases/09-retrospective.md — machine-parseable retrospective

Location: `{task-dir}/phases/09-retrospective.md`. Budget ≤150 lines. The
control plane ingests this file with regexes — headings below are contracts,
not suggestions:

- `## Lessons Learned` — section heading (case-insensitive, H2/H3).
- `### Lesson N: <title>` — one H3 per lesson, numbered.
- `**Action**: <what to change>` — one line inside each lesson.
- `## Process Improvements` — that exact phrase; "Improvement
  Recommendations" and other synonyms are NOT matched and silently produce an
  empty ingest.
- The metrics table's duration row is parsed: hours as `{N}h` and variance as
  a percentage.

```markdown
# Retrospective: {task name}

## Task Summary
Type: {Feature | Bug Fix | Refactor | Chore}
Outcome: {Success | Partial | Failed}

## What Went Well
- {win with a specific phase/file reference}

## What Didn't Go Well
- {challenge} — root cause: {cause}; resolution: {fix}

## Lessons Learned

### Lesson 1: {short imperative title}
{1-3 sentences of context — what happened, why it will recur}
**Action**: {the concrete change that prevents it}

## Process Improvements
- {improvement to the workflow/agents/hooks, with the number it should move}

## Metrics
| Metric | Value |
|---|---|
| Duration | Estimated {N}h vs Actual {M}h ({+/-X}%) |
| Delegations | {N} ({M} correction loops) |

## Bias Check
- Confirmation / anchoring / sunk-cost: {one honest line each}
```

Evidence rules:

- Read `{task-dir}/logs/agents.md` first — the 7-cell ledger
  (`agent | phase | verdict | loops | quality | mistakes | artifact`) is the
  primary evidence for challenges and lessons.
- Lessons are actionable and specific; "communicate better" is not a lesson.
- Do not modify agent definitions or code from here — recommend only; the
  retro/advisor loop turns accepted recommendations into reviewed changes.
