# Gotchas, Pinned Dependencies, and Failure Modes

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

## Common mistakes to avoid

| Red flag / symptom | Fix |
|---|---|
| Edited the template and regenerated HTML without running `scripts/test.sh` | Run it. Diff against golden. Refresh golden only with `--update` after reviewing the diff. |
| Used `sed` in `build.sh` for substitution | Do not. Mermaid source contains regex metacharacters and the `%%` comment syntax breaks `sed`. `build.sh` uses POSIX `awk index`/`substr`. Keep it that way. |
| New gotcha discovered -> patched in the generated HTML but not the template | Fix the template, then regenerate, then refresh the golden. Never patch generated output in isolation. |
| Skipped `verify.sh` because "it is just a styling change" | Run it. OKLCH passed khroma unit tests for five years before Mermaid 11. Visual checks catch rendering regressions cheaper than user bug reports. |
| Used OKLCH colors in `mermaid.initialize()` config | Use hex or rgba only. Mermaid's color parser (khroma) does not support OKLCH. See conversion table above. |

## Failure modes

| Failure | Recovery |
|---------|----------|
| Blank viewport in browser | Check console for `Unsupported color format: "oklch(...)"` -- a color in `mermaid.initialize()` config is OKLCH; convert to hex |
| Search filter dims rows instead of entities | Entity title `<text>` must be identified by `id^="text-entity-"`, not `class="er entityLabel"` |
| Download button returns SVG instead of Mermaid source | Source must be in `<script id="mmd-source" type="text/plain">`, not `<pre class="mermaid">` |
| `test.sh` fails with diff | Inspect the diff; if the change is intentional, refresh golden with `test.sh --update` after review |
| `verify.sh` cannot bind a port | Check if a previous `verify.sh` process was not stopped; kill orphan python3 processes |

## Real-world provenance

Every gotcha above corresponds to a bug observed and fixed in the session that authored this skill (a database schema viewer, April 2026). Full Playwright verification trail is in git history.
