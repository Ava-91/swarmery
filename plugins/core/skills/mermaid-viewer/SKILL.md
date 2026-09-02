---
name: mermaid-viewer
version: "1.0.0"
owner: "swarmery-core"
description: "Use this skill when the user has a Mermaid source file (.mmd) and wants to view it in a browser -- asking to convert mermaid to html, render this diagram, make an HTML viewer, or make the schema explorable. Don't use it for generating new diagrams from scratch or exporting to PNG/PDF."
allowed-tools: Read, Bash, Write
color: teal
mermaid-version: "11.4.1"
docs:
  status: reviewed
  source_sha: 03d848ecd091
  updated: 2026-08-06
---

# Purpose

Materialise any Mermaid diagram source (`.mmd`) into a themed, interactive HTML viewer — one self-contained file with pan/zoom, ER entity search, and a source-download button. Mermaid 11.4.1 and svg-pan-zoom 3.6.2 load from jsDelivr at runtime; styling is a dark-terminal design system consistent with the `html-reporting` shell. ER-specific features no-op for other diagram types.

# Rules

- Colors in `mermaid.initialize()` config must be hex or rgba, never OKLCH — Mermaid's color parser (khroma) does not support it; violation gives a blank viewport.
- Mermaid source stays in `<script id="mmd-source" type="text/plain">`, never `<pre class="mermaid">`, or the download button returns SVG.
- `build.sh` substitutes sentinels with POSIX `awk`, never `sed` — Mermaid source breaks `sed`.
- Playwright verification navigates via `http://localhost:<port>`, never `file://`; always `verify.sh stop <port>` afterwards.
- Never bump the pinned deps (mermaid 11.4.1, svg-pan-zoom 3.6.2) without re-running `test.sh` + `verify.sh` + Playwright on a real fixture.
- Fix bugs in `templates/viewer.html.tpl`, regenerate, refresh the golden (`test.sh --update`) — never patch generated HTML in isolation.

# Resources

- Read `resources/build-procedure.md` when running the pipeline — inputs/outputs, the four-step procedure, script reference, self-check, escalation, examples, file layout.
- Read `resources/gotchas-and-pinned-deps.md` when debugging rendering or touching the template — the five baked-in gotchas, OKLCH→sRGB conversion table, pinned-dependency rules, CDN supply-chain note, failure modes.
- Existing subdirs: `templates/viewer.html.tpl` (sentinel skeleton), `scripts/` (`stats.sh`, `build.sh`, `verify.sh`, `test.sh`), `examples/` (`sample.mmd` fixture + `sample.html` golden).

# How to use

## What it does

Turns an existing `.mmd` file into one self-contained HTML page: rendered diagram, pan and zoom, an entity search filter for ER diagrams, meta badges with counts, and a button to download the original Mermaid source — dark-themed so the output looks finished.

## When to use it

- A `.mmd` file exists and you want to view or review it in a browser.
- "Convert this mermaid to HTML", "make a viewer", "make the schema explorable".
- Not for authoring new diagrams, PNG/PDF/SVG export, or editing diagram content; in CI, call `scripts/build.sh` directly.

## How to invoke

```
Skill(skill: "core:mermaid-viewer")
```

Point it at the `.mmd`. Optional: `--out <path>` (default writes next to the source — use `--out` in git-tracked dirs), `--title`, `--subtitle` (otherwise inferred from `%%` comments).

## Worked example

```
Request: "render docs/architecture.mmd to /tmp/arch-viewer.html"

Read source -> stats.sh (flowchart, node count) -> build.sh fills the
template -> verify.sh serves it for a zero-console-error browser check
-> server stopped. Result: /tmp/arch-viewer.html — open it and pan around.
```

You are told the file path, golden smoke-test status, and any console errors seen during verification.
