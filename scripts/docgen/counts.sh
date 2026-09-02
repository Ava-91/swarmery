#!/usr/bin/env bash
# counts.sh — the repo's own numbers, derived. No LLM, no writes.
#
# Emits a single JSON object on stdout describing what this marketplace
# actually ships, so that no published number is ever hand-typed:
#
#   packs, agents, skills, commands   the corpus totals, counted through the
#                                     SAME walk the docs gate uses (listItems in
#                                     lib.sh) — one definition of "an item", so
#                                     check-coverage.sh and the landing page can
#                                     never disagree about how many there are
#   core                              version + per-item counts for the one pack
#                                     every consumer enables, read from
#                                     plugins/core/.claude-plugin/plugin.json
#   go_packages, api_routes           the two control-plane numbers the landing
#                                     page quotes, or null
#   plugins[]                         name + description straight from the
#                                     manifest, in manifest order
#
# The manifest is the registry: `.claude-plugin/marketplace.json` decides which
# packs exist, and the `plugins/` directory listing is only a cross-check. When
# the two disagree — a pack added to the tree but never registered, or the
# reverse — that is a shipping defect, not a rounding error, so this script
# reports every mismatch and exits 1 rather than publishing a number derived
# from half of the truth.
#
# Go packages are counted off the filesystem (directories under tools/swarmery
# holding at least one .go file) rather than from `go list ./...`. Two reasons,
# both about reproducibility: `go list ./...` FAILS outright on a clean checkout
# because web/embed.go carries `//go:embed all:dist` and web/dist/ is a build
# artifact nobody commits; and a drift gate that needed a Go toolchain would
# emit a different number on a runner that lacks one. The filesystem walk needs
# no toolchain, no module cache and no network. Where Go IS present the script
# still asks `go list -e ./...` (the -e is what survives the broken embed) and
# warns on stderr if the two disagree — a warning, not a failure, because a
# landing-page counter being one out is worth telling someone about, not worth
# breaking every build over.
#
# API routes are counted from the literal `mux.HandleFunc("…/api/…")` lines in
# internal/api/routes.go, which is where every one of them is registered; the
# lone `mux.Handle("/", spaHandler)` in server.go is the SPA catch-all, not a
# REST route, and is deliberately not counted.
#
# Anything that cannot be derived is emitted as null. A stale number is worse
# than a missing one: apply-counts.sh drops the tile rather than publish a guess.
#
# Exit: 0 = JSON written, 1 = manifest/tree mismatch or an unreadable source.
#
# Env:
#   DOCGEN_ROOT  corpus root (default: this repo, resolved from lib.sh)
set -euo pipefail

# shellcheck source=scripts/docgen/lib.sh
. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

read -r -d '' COUNTS_JS <<'COUNTS' || true

const { execFileSync } = require('child_process');

const root = process.env.DOCGEN_ROOT || process.cwd();

const problems = [];
const problem = (why) => problems.push('PROBLEM: ' + why);

const die = (why) => {
  console.error('counts: ' + why);
  process.exit(1);
};

const readJSON = (rel) => {
  try {
    return JSON.parse(stripBOM(fs.readFileSync(path.join(root, rel), 'utf8')));
  } catch (err) {
    return die('cannot read ' + rel + ': ' + err.message);
  }
};

const ls = (dir) => (fs.existsSync(dir) ? fs.readdirSync(dir).sort() : []);

// ── packs ───────────────────────────────────────────────────────────────────
// marketplace.json is the registry. Every entry must resolve to a real pack
// directory, and every pack directory carrying a plugin.json must be registered.
const manifest = readJSON('.claude-plugin/marketplace.json');
const entries = Array.isArray(manifest.plugins) ? manifest.plugins : [];
if (entries.length === 0) die('.claude-plugin/marketplace.json lists no plugins');

const plugins = entries.map((p) => ({
  name: String(p.name || ''),
  description: String(p.description || '').trim(),
}));

const declared = new Set();
for (const p of entries) {
  const name = String(p.name || '');
  if (!name) {
    problem('marketplace.json has an entry with no name');
    continue;
  }
  if (declared.has(name)) problem('marketplace.json registers "' + name + '" twice');
  declared.add(name);
  // `source` is a repo-relative path such as "./plugins/core".
  const src = typeof p.source === 'string' ? p.source : 'plugins/' + name;
  if (!fs.existsSync(path.join(root, src))) {
    problem('marketplace.json registers "' + name + '" at ' + src + ', which does not exist');
  }
}

const pluginsDir = path.join(root, 'plugins');
for (const dir of ls(pluginsDir)) {
  const packDir = path.join(pluginsDir, dir);
  if (!fs.statSync(packDir).isDirectory()) continue;
  // A directory is a pack only once it carries its own manifest; anything else
  // under plugins/ is scaffolding and is not expected in the registry.
  if (!fs.existsSync(path.join(packDir, '.claude-plugin', 'plugin.json'))) continue;
  if (!declared.has(dir)) {
    problem('plugins/' + dir + ' has a plugin.json but is not registered in marketplace.json');
  }
}

if (problems.length > 0) {
  for (const line of problems) console.error(line);
  console.error('counts: the manifest and the plugins/ tree disagree. marketplace.json is');
  console.error('the registry every consumer installs from — reconcile it before any number');
  console.error('derived from it is published.');
  process.exit(1);
}

// ── agents / skills / commands ──────────────────────────────────────────────
// One walk, shared with check-coverage.sh, so "how many items" has one answer.
const totals = { agent: 0, skill: 0, command: 0 };
const perPack = new Map();
for (const abs of listItems(root)) {
  const rel = path.relative(root, abs).split(path.sep).join('/');
  const { kind, plugin } = classify(rel);
  if (!kind) continue;
  totals[kind] += 1;
  if (!perPack.has(plugin)) perPack.set(plugin, { agent: 0, skill: 0, command: 0 });
  perPack.get(plugin)[kind] += 1;
}

const corePlugin = readJSON('plugins/core/.claude-plugin/plugin.json');
const coreItems = perPack.get('core') || { agent: 0, skill: 0, command: 0 };

// ── Go packages ─────────────────────────────────────────────────────────────
const goDir = path.join(root, 'tools', 'swarmery');

// The directories the Go toolchain itself ignores. Matching that list is what
// keeps the filesystem walk and `go list` in agreement.
const skipDir = (name) =>
  name === 'vendor' || name === 'testdata' || name.startsWith('.') || name.startsWith('_');

function goPackageDirs(dir) {
  let found = 0;
  let hasGo = false;
  for (const name of ls(dir)) {
    const abs = path.join(dir, name);
    let st;
    try {
      st = fs.statSync(abs);
    } catch {
      continue;
    }
    if (st.isDirectory()) {
      if (skipDir(name)) continue;
      found += goPackageDirs(abs);
    } else if (name.endsWith('.go')) {
      hasGo = true;
    }
  }
  return found + (hasGo ? 1 : 0);
}

let goPackages = null;
if (fs.existsSync(goDir)) {
  goPackages = goPackageDirs(goDir);

  // Cross-check against the toolchain where it exists. `-e` is load-bearing:
  // without it `go list` aborts on the unbuilt web/dist embed and reports nothing.
  try {
    const out = execFileSync('go', ['list', '-e', './...'], {
      cwd: goDir,
      encoding: 'utf8',
      stdio: ['ignore', 'pipe', 'ignore'],
    });
    const listed = out.split('\n').filter((l) => l.trim() !== '').length;
    if (listed > 0 && listed !== goPackages) {
      console.error(
        'warn: counts: filesystem walk says ' + goPackages + ' Go packages, ' +
          '`go list -e ./...` says ' + listed + ' — publishing the walk. ' +
          'A new build tag or an ignored directory usually explains the gap.'
      );
    }
  } catch {
    // No Go on this machine, or the module would not load at all. The walk stands.
  }
}

// ── API routes ──────────────────────────────────────────────────────────────
// Both registration shapes in routes.go: `"GET /api/…"` and, for the three
// proxy/static handlers, a method-less `"/api/…"`.
const routesFile = path.join(goDir, 'internal', 'api', 'routes.go');
let apiRoutes = null;
if (fs.existsSync(routesFile)) {
  const src = fs.readFileSync(routesFile, 'utf8');
  const matches = src.match(/mux\.HandleFunc\("(?:[A-Z]+ )?\/api\//g);
  apiRoutes = matches ? matches.length : 0;
}

process.stdout.write(
  JSON.stringify(
    {
      packs: plugins.length,
      agents: totals.agent,
      skills: totals.skill,
      commands: totals.command,
      core: {
        version: String(corePlugin.version || ''),
        agents: coreItems.agent,
        skills: coreItems.skill,
        commands: coreItems.command,
      },
      go_packages: goPackages,
      api_routes: apiRoutes,
      plugins,
    },
    null,
    2
  ) + '\n'
);
COUNTS

node -e "${DOCGEN_NODE_LIB}${COUNTS_JS}"
