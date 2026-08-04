# Extending swarmery: where project-specific things live

swarmery is **one global system + a thin native overlay per project** — never a separate
"sub-agent-system" inside a project. Claude Code merges both layers in every session:
enabled plugins supply the global components; the project's own `.claude/` supplies local
ones; **on a name collision the project-local component wins** (native base + overlay).

## Decision tree for every new skill / agent / command / template / script

| The thing is… | It goes to… |
|---|---|
| useful to any project | `plugins/core` (bump semver → consumers adopt via `/plugin update`) |
| useful to ≥2 projects of one domain or capability | the matching pack — domain (`uav-pack` / `iot-pack` / `web-pack`) or capability (`infra-pack` / `lsp-pack`) |
| unique to one project | **the project's own `.claude/{agents,skills,commands,templates}/`** — versioned with the project's code, because it evolves with the product |
| configuration, not logic (repo lists, env names, commit scopes, domain nouns) | the project's `.claude/project.json` |

## The graduation rule (flow goes UP only)

New things are born **project-local**. When a *second* project needs the same thing, promote it
to a pack; when every project needs it, promote to core. Never copy downward — copying framework
files into projects recreates the fork-and-sync rot this repo exists to eliminate.

Promotion checklist:
1. De-flavor it (see `docs/NEUTRALITY.md`) — values move to `project.json` reads.
2. Move the file into the pack/core; bump that plugin's semver.
3. Delete the project-local copy in the consumer that donated it (the plugin now supplies it).

## Pack requirements (`requirements.json`)

A pack that cannot work without project-specific config declares it in
`plugins/<pack>/requirements.json` — at the **pack root**, next to `agents/` and `skills/`
(only `plugin.json` lives under `.claude-plugin/`). This is how a pack says "I need block `X`
in `.claude/project.json`, here is its schema, here is why" **without** any reader needing to
know what the pack does — the same neutrality rule that keeps domain knowledge out of core.

```json
{
  "version": 1,
  "projectConfig": [
    {
      "key": "<top-level project.json key>",
      "title": "<human label>",
      "why": "<one sentence: what breaks without it>",
      "docs": "skills/<skill>/SKILL.md",
      "schema": { "type": "object", "properties": {}, "required": [] }
    }
  ]
}
```

| Field | Required | Purpose |
|---|---|---|
| `version` | yes | integer, currently exactly `1`. A reader seeing any other number ignores the file (forward-compat) |
| `projectConfig[]` | yes | the top-level `project.json` keys the pack needs |
| `.key` | yes | the key name in `project.json` |
| `.title` | yes | human-readable heading |
| `.why` | yes | one sentence on why the pack can't run without it — shown above the form |
| `.docs` | no | pack-relative path to documentation, rendered as a link |
| `.schema` | yes | self-contained JSON Schema fragment (`type: object` + `properties` + `required`) |

Unknown fields are ignored by readers, so a later version can add optional fields without
bumping `version`.

**Sync rule (CI-enforced).** `.schema` must be canonically identical to the matching branch of
`overlays/_schema/project.schema.json` → `properties[<key>]` — same `description` strings, same
`required`, same defaults. A consumer's `project.json` is validated against the overlay schema
while the form is rendered from the pack file; if they drift, the form collects one shape and
the schema rejects another. `scripts/check-plugin-requirements.sh` compares them under a
canonical form (object keys sorted recursively, so ordering and formatting never count as
drift) and fails the build on any difference, or when the key is missing from the overlay
schema entirely. The file is optional per pack — its absence is not an error. Run it locally:

```bash
bash scripts/check-plugin-requirements.sh   # → ✓ plugin requirements in sync (<n> checked)
```

Changing the schema means editing **both** files in the same commit.

**Neutrality.** `requirements.json` is under `plugins/**`, so it carries placeholders only —
never a real host, project key, or status name (`docs/NEUTRALITY.md`).

**Semver.** Adding a new required key to `requirements.json` changes the pack's contract with
its consumers: existing projects become under-configured until someone fills the new key in.
That is a **minor bump** of the pack's `plugin.json` at minimum, so consumers pick it up via
`/plugin update`. Loosening the contract (dropping a required key, adding an optional one) is a
patch bump.

## Overriding core behavior

A project may ship a component with the **same name** as a core one in its `.claude/agents/`
(etc.) — the local one wins in that project only. Use this for project-specific variants of a
core agent instead of forking the framework.

## Template & script resolution convention

- **Templates:** agents look in `${CLAUDE_PROJECT_DIR}/.claude/templates/` first, then fall back
  to the plugin's `${CLAUDE_PLUGIN_ROOT}/templates/`. Project-specific report/summary formats
  live with the project; generic ones ship with core.
- **Scripts:** project automation stays in the project repo (`scripts/` or `.claude/scripts/`).
  Core ships only project-agnostic tooling (`plugins/core/bin/`), which reads `project.json`
  for anything project-shaped.

## What a consumer project's `.claude/` looks like

```
<project>/.claude/
├── settings.json        # enables core@swarmery + the packs it needs (+ AGENT_PROJECT env)
├── project.json         # the flavor config (schema: overlays/_schema/project.schema.json)
├── agents/              # project-unique agents + intentional overrides (often empty)
├── skills/              # project-unique skills (often just a few)
├── templates/           # project-specific templates (checked before plugin templates)
└── scripts/             # project automation (optional)
```

Thin is healthy: if this directory starts growing generic content, something wants promoting.

## Versioning the overlay itself

A single-repo project versions `.claude/` naturally — it lives inside the project repo.
**Multi-repo workspaces** (no single root repo) should make the overlay its own small repo:

```
<workspace>/agents/        ← git repo holding ONLY the project-specific overlay
<workspace>/.claude -> agents   (symlink; sub-repo .claude symlinks point here too)
```

This keeps rules/, project-specific skills/agents/commands, agent-memory and project.json
under version control and shareable across machines, while the generic framework still
arrives via plugins. If the workspace previously hosted a full pre-swarmery agent framework
repo, reuse it: push the thin overlay as a successor branch — history stays, layout shrinks.
