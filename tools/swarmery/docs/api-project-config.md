# Project config API

Two routes back the plugin **configure** modal: they write, and help fill in, a project's
`.claude/project.json` overlay. Both are keyed by a *declared* config key — a pack states which
top-level key it needs in `plugins/<pack>/requirements.json` (`EXTENDING.md` → "Pack
requirements"). The daemon never learns what a key means: packs declare, `internal/pluginreq`
evaluates, these handlers only fence.

## The fence

Identical on both routes, and copied from the plugin-toggle write rather than simplified — the
probe writes nothing, but it spawns a process with the project directory as its cwd, which is
not a reason to fence it less:

1. **Local origin only** — `requireLocalOrigin` wraps both routes (`internal/api/routes.go`).
2. **Onboard roots must be set** — an empty `SWARMERY_ONBOARD_ROOTS` disables both: `403`.
3. **The path must resolve under those roots** — `resolveUnderRoots` on the path from the
   `projects` row; anything outside is `403`.

Two further checks are the endpoints' own: the key must be one a catalogued pack **declared**
(`404 unknown config key`), and — on the write only — the value must satisfy that pack's schema
fragment (`422`). Without the first, the route is an arbitrary writer of any key into any
`project.json`.

## `PUT /api/projects/{id}/config/{key}`

Writes exactly one top-level key. Body — `value` must be a JSON object; a scalar or `null` is
`400`:

```json
{ "value": { "…": "the whole block for this key" } }
```

`200`:

```json
{ "key": "jira", "written": true, "backup": ".claude/project.json.bak", "changed": true }
```

`changed` is `false` when the key already held this exact value; the write still happened, so
the UI can say "no change" instead of claiming a save that moved nothing.

`422` carries per-field problems so the modal can put each one next to its input:

```json
{ "error": "jira does not satisfy the schema declared for it", "problems": ["…"] }
```

Other refusals: `400` invalid id or body, `404` marketplace not installed / undeclared key,
`409` not a managed overlay (no `.claude/project.json`) or the existing file does not parse.

**Invariant — single-key merge, backup first.** The existing file is decoded, only `key` is
replaced, and it is re-encoded: every foreign key, its order, and the file's formatting survive.
The previous contents are written to `<file>.bak` *before* the new file is put in place
atomically. The `.bak` lands beside the **real** file, which for a symlinked overlay is not
under the project directory — the response's `backup` says where to look.

## `POST /api/projects/{id}/config/{key}/probe`

Suggests live values for the fields the pack's `probe` block nominated, by handing the pack's
prompt to a `claude` session that already carries the operator's connectors. **It writes
nothing**, and the daemon gains no client and no token. Body is the form's current partial
contents:

```json
{ "value": { "…": "partial config" } }
```

`200` — success and failure share one shape, so the browser has one code path:

```json
{ "suggestions": { "qaStatus": ["…"] }, "reason": "", "notes": "" }
```

`suggestions` is dotted field path → candidates, never `null`, whitelisted to the `fields` the
pack nominated. `reason` is set only when nothing came back.

**Invariant — a probe never fails with a 5xx.** A timeout, a missing `claude` binary, a crashed
process, prose instead of JSON, a connector that would not resolve — each is a `200` with empty
`suggestions` and a one-line `reason`. A red 500 over an optional convenience would teach the
operator that the config page is broken; the form still works without the help. 5xx is
unreachable here on purpose.

Three refusals still precede any agent run: the fence above, `404` when the key declares no
`probe` (the UI only offers the button when it does, so reaching it is a bug), and `400` when
the probe's `needs` inputs are not filled in yet — the one failure the operator can fix in the
form they are already looking at, and not worth three minutes of agent time to report.
