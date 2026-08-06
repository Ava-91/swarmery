---
name: mermaid-viewer
version: "1.0.0"
owner: "swarmery-core"
description: "Use this skill when the user has a Mermaid source file (.mmd) and wants to view it in a browser -- asking to convert mermaid to html, render this diagram, make an HTML viewer, or make the schema explorable. Don't use it for generating new diagrams from scratch or exporting to PNG/PDF."
allowed-tools: Read, Bash, Write
color: teal
mermaid-version: "11.4.1"
docs:
  status: generated
  source_sha: 03d848ecd091
  updated: 2026-08-06
---

# Purpose

Materialise any Mermaid diagram source (`.mmd`) into a themed, interactive HTML viewer. Output is a single self-contained file; Mermaid 11.4.1 and svg-pan-zoom 3.6.2 are fetched from jsDelivr at runtime. Styling uses a dark-terminal design system (OKLCH CSS tokens, Inter + JetBrains Mono, dark mode, cyan-green accent) consistent with the `html-reporting` shell.

**ER-specific features** (entity indexing, search filter) gracefully no-op for non-ER diagram types.

# When to use this skill

- `.mmd` file exists and user wants a browser viewer
- User says: "convert mermaid to html", "make a viewer for X.mmd", "render this schema"
- User says: "make this diagram easy to review / explore"
- Default output path: same directory as `.mmd`, same basename, `.html` extension
- Explicit override via `--out <path>`

# When NOT to use this skill

- **User wants to generate a new diagram from scratch** -- this skill requires an existing `.mmd` file as input. Use a Mermaid authoring workflow instead.
- **User wants to export to PNG, PDF, or SVG** -- this skill produces interactive HTML only.
- **Output must be embedded in a CI artifact pipeline** -- use `scripts/build.sh` directly from a CI job without the LLM orchestration steps.
- **User wants to edit the diagram content** -- this skill renders existing content; it does not modify `.mmd` source.
- **User asks to "create a diagram showing..."** without providing a `.mmd` file -- the prerequisite is an existing source file.

# Required environment (Runtime: .claude/skills/mermaid-viewer/SKILL.md)

- `bash` with POSIX `awk` (macOS or Linux)
- `python3` (for `verify.sh` HTTP server, optional)
- Write access to the output directory
- Network access to jsDelivr CDN at runtime (when the HTML is opened in a browser)

# Inputs

| Input | Required | Description |
|-------|----------|-------------|
| `.mmd` file path | Yes | Path to the Mermaid source file |
| Output path | No | `--out <path>`. Default: same directory as `.mmd`, same basename, `.html` extension |
| Title | No | Heading + browser tab title. Inferred from `%%` comments or diagram declaration if not provided |
| Subtitle | No | Subtitle line below the heading |

# Outputs

**Format:** A single self-contained HTML file.

**Length budget:** Output size is determined by the template + diagram source length; the HTML file is not subject to a line count limit. The SKILL.md itself is under 250 lines.

**Contents:**
- Mermaid diagram rendered via client-side JS (fetched from jsDelivr CDN)
- Pan/zoom controls (svg-pan-zoom)
- Search filter (ER diagrams: filters entities by name)
- Download `.mmd` button (original source preserved in `<script id="mmd-source" type="text/plain">`)
- Meta badges (table count, FK count, etc.) when stats JSON is provided
- Dark-mode themed shell

# Procedure

<procedure>

Four steps -- two LLM (read + orchestrate), three deterministic shell scripts:

1. **Read the `.mmd`** with the `Read` tool. Pull `title` / `subtitle` hints from the leading `%%` comment block or the diagram declaration.
   Checkpoint: `.mmd` content loaded; title/subtitle resolved.

2. **`scripts/stats.sh <mmd>`** -- JSON to stdout: `{"type":"erDiagram","tables":N,"hardFks":N,"logicalLinks":N}` or `{"type":"flowchart","nodes":N}`.
   Checkpoint: stats.sh exits 0 and JSON is valid before proceeding to build.sh.

3. **`scripts/build.sh`** with flags:
   - `--mmd <path>` source (required)
   - `--out <path>` target HTML (required)
   - `--title <str>` heading + browser tab (required)
   - `--subtitle <str>` subtitle line (optional)
   - `--stats-json <json>` output from stats.sh (optional; drives meta badges)
   - `--footer <html>` custom footer HTML (optional)
   Checkpoint: build.sh exits 0; all seven sentinels substituted.

4. **(opt-in) `scripts/verify.sh start <dir>`** -- prints `URL<TAB>PID<TAB>PORT`. Caller drives Playwright (`browser_navigate` -> `browser_console_messages` -> assert zero errors -> `browser_take_screenshot`). Always follow up with `scripts/verify.sh stop <port>`.
   Checkpoint: zero console errors; verify.sh stopped.

</procedure>

**Output path warning:** The default output path writes HTML adjacent to the source `.mmd`, which may be in a committed source tree. If the `.mmd` is inside a git-tracked directory, consider using `--out` to write to a temporary or workspace directory to avoid accidentally staging generated HTML.

## Quick reference

| Script | Purpose | Signature |
|---|---|---|
| `stats.sh` | Parse `.mmd` -> JSON stats | `stats.sh <mmd-path>` |
| `build.sh` | Sentinel substitution -> HTML | `build.sh --mmd --out --title [--subtitle] [--stats-json] [--footer]` |
| `verify.sh` | HTTP server (Playwright cannot reach `file://`) | `verify.sh {start <dir>\|stop <port>}` |
| `test.sh` | Pipeline smoke test vs golden | `test.sh` or `test.sh --update` |

# Self-check before returning

- [ ] The generated HTML opens in a browser without console errors
- [ ] All seven template sentinels (`{%%%PAGE_TITLE%%%}`, `{%%%HEADING%%%}`, `{%%%SUBTITLE%%%}`, `{%%%META_BADGES%%%}`, `{%%%FOOTER_HTML%%%}`, `{%%%DOWNLOAD_BASENAME%%%}`, `{%%%MERMAID_BODY%%%}`) were substituted (build.sh bails if any remain)
- [ ] The Download `.mmd` button returns the original Mermaid source, not SVG markup
- [ ] Entity search (ER diagrams) dims whole entities, not individual rows
- [ ] `scripts/test.sh` passes (diff against golden matches)
- [ ] If `verify.sh` was used, `verify.sh stop <port>` was called to clean up the HTTP server

# Common mistakes to avoid

| Red flag / symptom | Fix |
|---|---|
| Edited the template and regenerated HTML without running `scripts/test.sh` | Run it. Diff against golden. Refresh golden only with `--update` after reviewing the diff. |
| Used `sed` in `build.sh` for substitution | Do not. Mermaid source contains regex metacharacters and the `%%` comment syntax breaks `sed`. `build.sh` uses POSIX `awk index`/`substr`. Keep it that way. |
| New gotcha discovered -> patched in the generated HTML but not the template | Fix the template, then regenerate, then refresh the golden. Never patch generated output in isolation. |
| Skipped `verify.sh` because "it is just a styling change" | Run it. OKLCH passed khroma unit tests for five years before Mermaid 11. Visual checks catch rendering regressions cheaper than user bug reports. |
| Used OKLCH colors in `mermaid.initialize()` config | Use hex or rgba only. Mermaid's color parser (khroma) does not support OKLCH. See conversion table below. |

# What to surface to the user

- The generated HTML file path
- Whether `test.sh` passed or drifted from golden
- Any console errors observed during `verify.sh` Playwright check
- If the `.mmd` contains a diagram type not tested by the golden fixture, note that ER-specific features (search, entity indexing) were not verified for this diagram type

# Escalation

- **`python3` or `awk` not available on the host:** Report that `verify.sh` (Python) or `build.sh` (awk) cannot run; do not attempt workarounds
- **Mermaid version upgrade needed:** Follow the bumping rule (re-run `scripts/test.sh` + `verify.sh` + Playwright on a real fixture); do not bump version without full verification
- **Template sentinel not substituted:** build.sh bails automatically; check that the sentinel name matches exactly between template and script

# Examples

<example title="Render a database schema">

**Input:** `.mmd` file containing an ER diagram of the main app's database schema
**Process:** Read `.mmd` -> `stats.sh` -> `build.sh --title "Database Schema"` -> `verify.sh` -> Playwright screenshot -> `verify.sh stop`
**Output:** `schema.html` in the same directory

</example>

<example title="Render a flowchart with custom output path">

**Input:** `docs/architecture.mmd`, user specifies `--out /tmp/arch-viewer.html`
**Process:** Read `.mmd` -> `stats.sh` (returns flowchart type) -> `build.sh` with `--out /tmp/arch-viewer.html` -> verify
**Output:** `/tmp/arch-viewer.html`

</example>

# Failure modes

| Failure | Recovery |
|---------|----------|
| Blank viewport in browser | Check console for `Unsupported color format: "oklch(...)"` -- a color in `mermaid.initialize()` config is OKLCH; convert to hex |
| Search filter dims rows instead of entities | Entity title `<text>` must be identified by `id^="text-entity-"`, not `class="er entityLabel"` |
| Download button returns SVG instead of Mermaid source | Source must be in `<script id="mmd-source" type="text/plain">`, not `<pre class="mermaid">` |
| `test.sh` fails with diff | Inspect the diff; if the change is intentional, refresh golden with `test.sh --update` after review |
| `verify.sh` cannot bind a port | Check if a previous `verify.sh` process was not stopped; kill orphan python3 processes |

# Related skills

- **code-standards** -- for reviewing generated HTML against the project's design conventions
- **testing** -- for writing Playwright tests against the generated viewer
- **supply-chain-security** -- for hardened deployment, use this skill to evaluate CDN dependency risks and generate a self-hosting plan for jsDelivr assets

---

## Gotchas -- the template bakes these in; do not remove them

| # | Rule | Why | Symptom if violated |
|---|---|---|---|
| 1 | `themeVariables` / `er` config in `mermaid.initialize({...})` must be **hex or rgba**, never OKLCH | Mermaid's color parser (khroma) does not support OKLCH | Blank viewport + `Unsupported color format: "oklch(...)"` in console |
| 2 | `indexEntities()` must call `g.classList.add("entity-group")` | Mermaid 11 does NOT emit that class itself | Search input dims nothing |
| 3 | Mermaid source lives in `<script id="mmd-source" type="text/plain">`, never `<pre class="mermaid">` | Mermaid replaces children with SVG on render, destroying `textContent` | Download `.mmd` button returns SVG markup |
| 4 | Playwright must navigate via `http://localhost:<port>/...`, never `file://...` | Browser security model blocks `file://` when driven by automation | `Error: Access to "file:" protocol is blocked` |
| 5 | Entity title `<text>` is identified by `id^="text-entity-"`, never by `class="er entityLabel"` alone | Field cells share that class (380+ matches on a 12-table diagram) | Search filter dims individual rows instead of whole entities |

## OKLCH -> sRGB conversion table (for rule #1)

| CSS token (keep as OKLCH) | `themeVariables` value (must be hex/rgba) |
|---|---|
| `oklch(0.20 0.008 220)` | `#1e232b` (mainBkg, card body) |
| `oklch(0.17 0.008 220)` | `#191e26` (row-odd) |
| `oklch(0.22 0.006 220)` | `#252a31` (tertiary) |
| `oklch(0.22 0.02 162)` | `#1f2e28` (primaryColor -- ER title bar) |
| `oklch(0.72 0.18 162)` | `#3dd5a5` (brand cyan-green) |
| `oklch(0.95 0 0)` | `#ededed` (foreground) |
| `oklch(0.12 0.005 220)` | `#12161c` (labelBackground) |
| `oklch(0.72 0.18 162 / 70%)` | `rgba(61, 213, 165, 0.7)` (lineColor) |

## Pinned dependencies

| Dep | Version | Bumping rule |
|---|---|---|
| mermaid | 11.4.1 | Re-run `scripts/test.sh` + drive `verify.sh` + Playwright on a real fixture |
| svg-pan-zoom | 3.6.2 | Same |

Version drift is the #1 cause of silent regressions. The template pins both with explicit CDN paths.

**CDN supply-chain note:** Mermaid and svg-pan-zoom are fetched from jsDelivr at runtime. This bypasses the team's dependency scanning pipeline (npm audit, pip-audit). For air-gapped or hardened environments, self-host these assets and update the CDN URLs in `templates/viewer.html.tpl`. Review jsDelivr release pages quarterly for security advisories. For hardened deployment, use the `supply-chain-security` skill to evaluate CDN dependency risks.

## File layout

```
mermaid-viewer/
  SKILL.md                  (this file)
  templates/
    viewer.html.tpl       (HTML skeleton with {%%%SENTINELS%%%})
  scripts/
    stats.sh              (.mmd -> JSON stats)
    build.sh              (substitute sentinels -> HTML)
    verify.sh             (HTTP server harness for Playwright)
    test.sh               (pipeline smoke test vs golden)
  examples/
    sample.mmd            (ER-diagram fixture)
    sample.html           (golden; refreshed via test.sh --update)
```

## Real-world provenance

Every gotcha above corresponds to a bug observed and fixed in the session that authored this skill (a database schema viewer, April 2026). Full Playwright verification trail is in git history.

# How to use

## What it does

Turns an existing Mermaid source file (`.mmd`) into a single self-contained HTML page you can open in a browser. The page renders the diagram, gives you pan and zoom, a search filter for ER diagrams, and a button to download the original Mermaid source. The styling is a dark theme with a cyan-green accent, so the output looks finished rather than raw.

## When to use it

- You have a `.mmd` file on disk and want to look at it in a browser instead of a text editor.
- Someone asks you to "convert this mermaid to HTML" or "make a viewer for this diagram".
- A database schema in an ER diagram needs to be explorable — you want to search entities by name.
- You want to hand a reviewer one file they can open, with no build step or server on their side.

## When not to use it

- You want a new diagram written from scratch — this skill needs an existing `.mmd` file as input.
- You need a PNG, PDF, or standalone SVG — this produces interactive HTML only.
- You want the diagram content changed — this renders the source, it never edits it.
- You are wiring this into a CI job — call `scripts/build.sh` directly and skip the orchestration.

## How to invoke

```
Skill(skill: "core:mermaid-viewer")
```

Point it at the `.mmd` file. Add `--out <path>` when you do not want the HTML written next to the source, and `--title` / `--subtitle` when the leading `%%` comments do not already carry a good heading.

## Inputs

- `.mmd` file path — the Mermaid source to render — required.
- Output path (`--out <path>`) — where the HTML lands; defaults to the source directory with an `.html` extension — optional.
- Title — the heading and browser tab text; inferred from `%%` comments or the diagram declaration when omitted — optional.
- Subtitle — one line under the heading — optional.

## What you get back

One HTML file at the output path. It loads Mermaid 11.4.1 and svg-pan-zoom 3.6.2 from a public CDN when opened, so the browser needs network access. Inside you get the rendered diagram, pan/zoom controls, an entity search box, meta badges with counts parsed from the source, and a download button that returns the original `.mmd` text. You also get told the file path, whether the golden-file smoke test passed, and any browser console errors seen during verification.

## Worked example

```
Skill(skill: "core:mermaid-viewer")

Request: "render docs/architecture.mmd, write it to /tmp/arch-viewer.html"

What happens: the source is read, stats.sh reports a flowchart with a node
count, build.sh fills the template, verify.sh serves the file so a browser
check can confirm zero console errors, then the server is stopped.

You end up with: /tmp/arch-viewer.html — open it and pan around.
```

Note the default output path writes next to the source. If the `.mmd` lives in a git-tracked directory, pass `--out` so you do not accidentally stage generated HTML.

## Related

- `c4-architecture-docs` — prefer it when you still need to author the diagrams; it produces the `.mmd` files this skill renders.
- `testing` — prefer it when you want Playwright tests written against the generated viewer.
- `supply-chain-security` — prefer it when the CDN dependency is a problem and you need a self-hosting plan.
