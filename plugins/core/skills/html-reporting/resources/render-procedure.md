# html-reporting — inputs, procedure, and skeletons

## Inputs

| Input | Required | Description |
|-------|----------|-------------|
| Report kind | Yes | `summary` (Phase 8) or `audit` (Phase 5) — selects the skeleton |
| Title | Yes | The report's `<h1>` text |
| Sections | Yes | Already-authored content blocks (metrics, findings, backlog, …) |
| Output path | Yes | Where to write, e.g. `…/{slug}/phases/05-audit.html` |
| Health score | Audit only | Integer 1–10 for the audit header badge |

## Output

One self-contained `.html` file at the caller's path. Body budget: `<main>` ≤ 300
lines for a summary, ≤ 500 for an audit. Consolidate similar findings to stay
inside it; never pad. The shell CSS does not count against the content budget.

## Procedure

### 1. Pick the skeleton

- **`summary`** → Status header → Metrics table → per-role `<details>` "How to
  use" → Next steps `<ul>` → Known issues as `.card.crit`. Mirror the markdown
  `summary-templates` produced.
- **`audit`** → Executive summary (health `.score` /10) → Metrics table (current
  vs target) → Dimension Coverage table → P0–P3 backlog as `.card.sev-pN` blocks,
  each with What → Risk/Cost → Fix → How-to-verify → Engineering Standards.

*Checkpoint: skeleton chosen for the report kind.*

### 2. Fill the shell

Paste the shell from `resources/shell.md`, set `{{TITLE}}` and `{{META}}`, and
drop the authored content into `<main>`. Use the severity classes; never invent
inline colors. *Checkpoint: no `{{…}}` placeholder remains.*

### 3. Map content to components

Follow the component map in `resources/shell.md` — status header to `<h1>` plus a
`.badge`, metrics to `<table>`, per-role guidance to `<details>`, findings to
`.card.sev-pN`, positives to `.badge.ok`. *Checkpoint: every section uses a shell
component, not ad-hoc HTML.*

### 4. Verify self-containment and budget

No external `src`/`href` to a CDN; no `<script>` unless explicitly needed. Count
`<main>` lines against the budget and trim the lowest-priority sections first.
*Checkpoint: the file opens offline and is within budget.*

### 5. Write and confirm

Write to the caller's output path; confirm the file exists and is non-empty
(`test -s`). *Checkpoint: artifact on disk.*

## Self-check before returning

- [ ] Output is a single self-contained `.html` — no CDN or external assets.
- [ ] The skeleton matches the report kind (summary vs audit).
- [ ] Every `{{…}}` placeholder is replaced.
- [ ] Severity uses `.sev-p0..p3` and `.badge ok|warn|crit`, never ad-hoc colors.
- [ ] No metric fabricated — every number came from the caller.
- [ ] No secrets or PII in the output.
- [ ] `<main>` is within budget (300 summary / 500 audit).
- [ ] The file is written and verified on disk.

## Common mistakes to avoid

- **Linking external CSS or JS** — reports must render offline; keep it all inline.
- **Inventing metrics or a health score** — format only supplied numbers.
- **Re-styling per report** — one shell, so reports stay comparable.
- **Treating HTML as the canonical artifact** — markdown stays canonical; the
  HTML is the optional mirror.

## Worked example — an audit report

```
kind: audit
title: "Audit — orders/line-items"
output: .../{slug}/phases/05-audit.html
health score: 6
sections: metrics table, dimension coverage, 9 findings across P0–P3
```

The audit skeleton is selected, the canonical shell pasted, the score badge and
tables rendered, and each finding emitted as a `.card.sev-pN` block carrying
What → Risk/Cost → Fix → How-to-verify. The result is one offline HTML file at
that path.
