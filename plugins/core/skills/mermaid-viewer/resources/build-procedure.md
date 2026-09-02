# Build Procedure and Script Reference

## When to use

- `.mmd` file exists and user wants a browser viewer
- User says: "convert mermaid to html", "make a viewer for X.mmd", "render this schema"
- User says: "make this diagram easy to review / explore"
- Default output path: same directory as `.mmd`, same basename, `.html` extension
- Explicit override via `--out <path>`

## When NOT to use

- **User wants to generate a new diagram from scratch** -- this skill requires an existing `.mmd` file as input. Use a Mermaid authoring workflow instead.
- **User wants to export to PNG, PDF, or SVG** -- this skill produces interactive HTML only.
- **Output must be embedded in a CI artifact pipeline** -- use `scripts/build.sh` directly from a CI job without the LLM orchestration steps.
- **User wants to edit the diagram content** -- this skill renders existing content; it does not modify `.mmd` source.
- **User asks to "create a diagram showing..."** without providing a `.mmd` file -- the prerequisite is an existing source file.

## Required environment

- `bash` with POSIX `awk` (macOS or Linux)
- `python3` (for `verify.sh` HTTP server, optional)
- Write access to the output directory
- Network access to jsDelivr CDN at runtime (when the HTML is opened in a browser)

## Inputs

| Input | Required | Description |
|-------|----------|-------------|
| `.mmd` file path | Yes | Path to the Mermaid source file |
| Output path | No | `--out <path>`. Default: same directory as `.mmd`, same basename, `.html` extension |
| Title | No | Heading + browser tab title. Inferred from `%%` comments or diagram declaration if not provided |
| Subtitle | No | Subtitle line below the heading |

## Outputs

**Format:** A single self-contained HTML file. Output size is determined by the template + diagram source length.

**Contents:**
- Mermaid diagram rendered via client-side JS (fetched from jsDelivr CDN)
- Pan/zoom controls (svg-pan-zoom)
- Search filter (ER diagrams: filters entities by name)
- Download `.mmd` button (original source preserved in `<script id="mmd-source" type="text/plain">`)
- Meta badges (table count, FK count, etc.) when stats JSON is provided
- Dark-mode themed shell

**ER-specific features** (entity indexing, search filter) gracefully no-op for non-ER diagram types.

## Procedure

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

**Output path warning:** The default output path writes HTML adjacent to the source `.mmd`, which may be in a committed source tree. If the `.mmd` is inside a git-tracked directory, consider using `--out` to write to a temporary or workspace directory to avoid accidentally staging generated HTML.

## Quick reference

| Script | Purpose | Signature |
|---|---|---|
| `stats.sh` | Parse `.mmd` -> JSON stats | `stats.sh <mmd-path>` |
| `build.sh` | Sentinel substitution -> HTML | `build.sh --mmd --out --title [--subtitle] [--stats-json] [--footer]` |
| `verify.sh` | HTTP server (Playwright cannot reach `file://`) | `verify.sh {start <dir>\|stop <port>}` |
| `test.sh` | Pipeline smoke test vs golden | `test.sh` or `test.sh --update` |

## Self-check before returning

- [ ] The generated HTML opens in a browser without console errors
- [ ] All seven template sentinels (`{%%%PAGE_TITLE%%%}`, `{%%%HEADING%%%}`, `{%%%SUBTITLE%%%}`, `{%%%META_BADGES%%%}`, `{%%%FOOTER_HTML%%%}`, `{%%%DOWNLOAD_BASENAME%%%}`, `{%%%MERMAID_BODY%%%}`) were substituted (build.sh bails if any remain)
- [ ] The Download `.mmd` button returns the original Mermaid source, not SVG markup
- [ ] Entity search (ER diagrams) dims whole entities, not individual rows
- [ ] `scripts/test.sh` passes (diff against golden matches)
- [ ] If `verify.sh` was used, `verify.sh stop <port>` was called to clean up the HTTP server

## What to surface to the user

- The generated HTML file path
- Whether `test.sh` passed or drifted from golden
- Any console errors observed during `verify.sh` Playwright check
- If the `.mmd` contains a diagram type not tested by the golden fixture, note that ER-specific features (search, entity indexing) were not verified for this diagram type

## Escalation

- **`python3` or `awk` not available on the host:** Report that `verify.sh` (Python) or `build.sh` (awk) cannot run; do not attempt workarounds
- **Mermaid version upgrade needed:** Follow the bumping rule (re-run `scripts/test.sh` + `verify.sh` + Playwright on a real fixture); do not bump version without full verification
- **Template sentinel not substituted:** build.sh bails automatically; check that the sentinel name matches exactly between template and script

## Examples

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

## File layout

```
mermaid-viewer/
  SKILL.md                  (skill entry point)
  resources/
    build-procedure.md      (this file)
    gotchas-and-pinned-deps.md
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

## Related skills

- **code-standards** -- for reviewing generated HTML against the project's design conventions
- **testing** -- for writing Playwright tests against the generated viewer
- **supply-chain-security** -- for hardened deployment, evaluate CDN dependency risks and generate a self-hosting plan for jsDelivr assets
- **c4-architecture-docs** -- prefer it when you still need to author the diagrams; it produces the `.mmd` files this skill renders
