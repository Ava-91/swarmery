---
name: architecture-map
description: Generate or refresh the repo-wide architecture map — architecture-out/architecture-map.json (machine contract with named flows) + architecture-map.html (self-contained viewer). Use when the user asks for an architecture map, repo map, "/architecture-map", or when an agent needs a fresh machine-readable architecture overview. NOT for per-epic C4 deep-dives (use c4-architecture-docs) and NOT for building the knowledge graph itself (use /graphify).
docs:
  status: generated
  source_sha: ff1a7f56a769
  updated: 2026-08-06
---

# Architecture Map

Produce `<repo>/architecture-out/architecture-map.json` + `.html`. The JSON is the
source of truth; the HTML is rendered from it by `scripts/build.sh` — never write
HTML by hand.

## 0. Freshness gate (always first)

```bash
HEAD=$(git rev-parse HEAD)
LAST=$(node -e "try{const v=JSON.parse(require('fs').readFileSync('architecture-out/architecture-map.json')).analyzedAtCommit;console.log(typeof v==='string'&&v.length>=7?v:'')}catch{console.log('')}")
```

- `LAST == HEAD` → report "architecture map is up to date (commit <short>)" and STOP.
- `LAST` non-empty → **incremental mode**: `git diff --name-only $LAST..HEAD` →
  map changed paths onto existing `modules[].path` prefixes; re-describe ONLY
  touched modules, re-check only flows whose steps reference them; keep the rest.
- `LAST` empty → full analysis.

## 1. Inventory (ground truth, no invention)

- `.claude/project.json` — name, repos, stack, domainTerms.
- Root `CLAUDE.md` — layout section, commands, hard rules → `conventions` + `importantNotes`.
- `graphify-out/graph.json` if present (nodes have `community`/`community_name`,
  top-level `built_at_commit`): communities are *candidate* module groupings,
  god nodes are *candidate* hubs. Curate — target 15–40 modules, never 1:1 with
  communities. If graphify's `built_at_commit` trails HEAD, note it in
  `importantNotes` and lean on direct exploration instead.
- Manifests (`package.json`, `go.mod`, `plugin.json`, workflow YAML) → techStack,
  entryPoints, externalServices.

## 2. Layers

Pick 3–7 layers that fit THIS repo (do not force presentation/domain/infra onto
a repo that is a plugin marketplace or a CLI). `order` = left-to-right viewer
columns, upstream (actors/entrypoints) first.

## 3. Modules (fan out)

Dispatch parallel read-only subagents, one per layer (or per module group for
big layers). Each returns, per module: `responsibility` (1–2 sentences),
`keyFiles` (3–7 real paths — verify each exists), `exports` (public surface:
commands, endpoints, functions), `dependencies` (ids of modules it imports/calls).
Real paths only — a file that does not exist is a hard failure.

## 4. Flows (the point of the map)

5–10 named end-to-end scenarios a developer actually asks about ("what happens
when X"). Each step: `from`/`to` module ids, `action`, `file` anchor (at least
one per flow), `payload` where meaningful. Prefer flows crossing ≥ 3 modules.

## 5. Synthesize + validate + render

Assemble the full JSON (`schemaVersion: 1`, `analyzedAt` = today,
`analyzedAtCommit` = HEAD). Then:

Bundled files (schema, validator, renderer) live under ${CLAUDE_PLUGIN_ROOT}/skills/architecture-map/ — never copy them into the project.

```bash
mkdir -p architecture-out
node "${CLAUDE_PLUGIN_ROOT}/skills/architecture-map/scripts/validate.mjs" architecture-out/architecture-map.json
bash "${CLAUDE_PLUGIN_ROOT}/skills/architecture-map/scripts/build.sh" \
  --json architecture-out/architecture-map.json \
  --out  architecture-out/architecture-map.html
```

Fix every validator error before rendering. Finish by reporting: module/flow
counts, commit stamp, and the two artifact paths.

# How to use

## What it does

This skill builds a whole-repository architecture map: a machine-readable JSON file with layers, modules, and named end-to-end flows, plus a self-contained HTML viewer rendered from that JSON. It reads the repository as it actually is — manifests, entry points, real file paths — so the map describes shipped code rather than an idealized design. Re-running it is cheap: if the map is already stamped with the current commit it stops, and if it trails behind it only re-describes the modules the diff touched.

## When to use it

- You want a repo-wide picture of layers, modules, and how a request travels across them.
- Someone new needs to answer "what happens when X" without reading every directory.
- The map exists but the repo has moved on, and you want it refreshed against the current commit.
- Another agent needs a machine-readable architecture overview to plan against.

## When not to use it

- You need a deep C4 breakdown of one epic or feature — use the `c4-architecture-docs` skill instead.
- You need the underlying knowledge graph built or queried — use the `graphify` skill.
- You only want to render an existing Mermaid diagram — use the `mermaid-viewer` skill.

## How to invoke

```
Skill(skill: "architecture-pack:architecture-map")
```

Run it from the repository root you want mapped; everything else is discovered from the repo itself.

## Inputs

- Repository — the working directory the skill runs in — required, and it must be a git checkout, since the freshness gate stamps the map with `HEAD`.
- `.claude/project.json` — project name, repos, stack, domain terms — optional, used as ground truth when present.
- `graphify-out/graph.json` — an existing knowledge graph — optional, used only as candidate module groupings.

## What you get back

Two files under `architecture-out/` in the repository: `architecture-map.json` (the source of truth, schema version 1, stamped with the analysis date and commit) and `architecture-map.html` (a self-contained viewer built from that JSON). The JSON is validated before the HTML is rendered, and every validator error is fixed first. The final message reports module and flow counts, the commit stamp, and both artifact paths.

## Worked example

```
Skill(skill: "architecture-pack:architecture-map")

→ freshness gate: stored commit 4f2a19c, HEAD 9bc7e01 → incremental mode
→ 6 layers, 23 modules re-checked against the diff, 8 named flows
→ validate.mjs passed, viewer rendered

architecture-out/architecture-map.json
architecture-out/architecture-map.html
```

Open the HTML file in a browser to walk the layers left to right and step through each flow with its file anchors.

## Related

- `c4-architecture-docs` — when the subject is one epic or feature, not the whole repository.
- `graphify` — when you need the knowledge graph itself, or want to query relationships directly.
- `impact` — when the question is which code a specific change ripples into.
