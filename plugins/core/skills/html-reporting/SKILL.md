---
name: html-reporting
version: "1.0.0"
owner: "swarmery-core"
description: "Render a self-contained HTML report or dashboard (task summaries, code/operational audits) in one canonical dark-terminal shell. NOT for markdown-only output or the mermaid viewer (use mermaid-viewer)."
disable-model-invocation: true
color: teal
docs:
  status: reviewed
  source_sha: 70bbc7ba9aea
  updated: 2026-08-06
---

# Purpose

Wrap already-authored content in one reusable, **self-contained** HTML shell —
inline CSS, no external assets, no build step — so every report an agent emits
shares one visual language: a dark terminal theme with severity-coded cards,
metric tables, and collapsible sections. Used by the `session-closeout` skill's
Phase 8 dashboard and `@code-reviewer`'s Phase 5 audit.

Presentation only. This skill never gathers data, measures anything, or computes
a number — the caller supplies the content and this skill renders it.

# Rules (never violate)

1. Never fabricate a metric or a health score; format only what the caller passed.
2. Keep the file self-contained — no CDN link, no external `src`/`href`, no
   `<script>` unless explicitly required.
3. Use the canonical shell verbatim and its severity classes; never re-style a
   report or hand-roll inline colors.
4. Markdown stays the canonical artifact; the HTML is its optional mirror.
5. Respect the body budget — `<main>` ≤ 300 lines (summary) / ≤ 500 (audit);
   consolidate rather than pad, and never leave a `{{…}}` placeholder.
6. No secrets or PII in the output; verify the written file is non-empty.

# Resources

- Read `resources/shell.md` when rendering: the canonical HTML shell to copy
  verbatim plus the content-to-component map.
- Read `resources/render-procedure.md` first: the input table, the two section
  skeletons (summary, audit), the 5-step procedure, the self-check, common
  mistakes, and a worked example.

# How to use

## What it does

Turns content you already wrote into a single self-contained HTML page that opens
offline in any browser and survives being copied between machines.

## When to use it

- An agent finished its analysis and wants a browsable page rather than terminal text.
- You are rendering a task summary dashboard — status header, metrics, per-role guidance.
- You are rendering an audit report with a health score and a P0–P3 backlog.
- A report runs past three sections, or will be read outside the terminal.

Not for: markdown-only output (`SUMMARY.md` and audit markdown stay the source of
truth); work where the numbers are not measured yet — analyse first, format
after; diagrams (`mermaid-viewer`); or writing code, tests, or config.

## How to invoke

```
Skill(skill: "core:html-reporting")

kind: audit
title: "Audit — orders/line-items"
output: .../{slug}/phases/05-audit.html
health score: 6
sections: metrics table, dimension coverage, 9 findings across P0–P3
```

Kind, title, sections, and output path are required; health score for audits only.

## Worked example

The invocation above selects the audit skeleton, pastes the canonical shell,
renders the score badge and the metrics and coverage tables, and emits each of
the nine findings as a `.card.sev-pN` block carrying What → Risk/Cost → Fix →
How-to-verify. You end up with one offline HTML file at that path, verified
non-empty on disk and inside the 500-line body budget.

## Related

- `summary-templates` — authors the summary content this skill renders; run first.
- `mermaid-viewer` — whenever the artifact is a diagram.
- `code-standards` — produces the review findings that feed an audit report.
