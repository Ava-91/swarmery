#!/bin/bash
# SessionStart hook: inject a compact architecture digest as context.
#
# When a repo has an architecture map (architecture-out/architecture-map.json,
# produced by the architecture-map skill), a session starts blind to the repo
# topology and burns tool calls (Grep/Read) rediscovering it. This hook feeds
# the model a small, high-signal summary — modules by layer + named flows —
# so it knows WHERE things are before searching. Deliberately compact: the
# digest must cost far less than the exploration it replaces.
#
# Vendor-neutral: reads only the generic map schema; no project/brand tokens.
# Best-effort: a missing map, missing node, or a parse error exits 0 silently.
set -e

PROJECT_DIR="${CLAUDE_PROJECT_DIR:-$(pwd)}"
MAP="${PROJECT_DIR}/architecture-out/architecture-map.json"

[ -f "$MAP" ] || exit 0
command -v node >/dev/null 2>&1 || exit 0

# MAX_MODULES / MAX_FLOWS keep the digest bounded on large repos; overflow is
# summarized as a count, never silently dropped.
MAP="$MAP" node <<'NODE' 2>/dev/null || exit 0
const fs = require('fs');
const MAX_MODULES = 28; // detailed responsibility lines; the by-layer list is unbounded
const MAX_FLOWS = 15;
const RESP = 70; // max chars of a responsibility line

let m;
try { m = JSON.parse(fs.readFileSync(process.env.MAP, 'utf8')); }
catch (e) { process.exit(0); }

const clip = (s, n) => {
  s = String(s || '').replace(/\s+/g, ' ').trim();
  return s.length > n ? s.slice(0, n - 1) + '…' : s;
};

const out = [];
// `project` may be a string or an object ({name,...}) across map versions.
const projectName = typeof m.project === 'string' ? m.project : (m.project && m.project.name) || 'this repo';
const project = clip(projectName, 60);
out.push(`## Architecture map — ${project}`);
if (m.analyzedAtCommit) out.push(`_(map @ ${String(m.analyzedAtCommit).slice(0, 7)}; run /architecture-map if stale)_`);
out.push('');

const modules = Array.isArray(m.modules) ? m.modules : [];
if (modules.length) {
  // Group module ids by layer so the shape of the system reads at a glance.
  const byLayer = {};
  for (const mod of modules) {
    const layer = clip(mod.layer || 'other', 24);
    (byLayer[layer] ||= []).push(mod);
  }
  out.push(`### Modules (${modules.length}) by layer`);
  for (const layer of Object.keys(byLayer)) {
    out.push(`- **${layer}**: ${byLayer[layer].map((x) => clip(x.id || x.name, 30)).join(', ')}`);
  }
  out.push('');
  out.push('### Module responsibilities & paths');
  let shown = 0;
  for (const mod of modules) {
    if (shown >= MAX_MODULES) break;
    const id = clip(mod.id || mod.name, 30);
    const path = clip(mod.path || '', 50);
    const resp = clip(mod.responsibility || '', RESP);
    out.push(`- \`${id}\`${path ? ` (${path})` : ''}${resp ? ` — ${resp}` : ''}`);
    shown++;
  }
  if (modules.length > MAX_MODULES) out.push(`- …and ${modules.length - MAX_MODULES} more modules (see the full map)`);
  out.push('');
}

const flows = Array.isArray(m.flows) ? m.flows : [];
if (flows.length) {
  out.push(`### Named flows (${flows.length})`);
  let shown = 0;
  for (const f of flows) {
    if (shown >= MAX_FLOWS) break;
    out.push(`- **${clip(f.name || f.id, 40)}**${f.description ? ` — ${clip(f.description, RESP)}` : ''}`);
    shown++;
  }
  if (flows.length > MAX_FLOWS) out.push(`- …and ${flows.length - MAX_FLOWS} more flows`);
  out.push('');
}

out.push('_Use this map to navigate directly instead of broad searching. Full detail: `architecture-out/architecture-map.html`._');
process.stdout.write(out.join('\n') + '\n');
NODE

exit 0
