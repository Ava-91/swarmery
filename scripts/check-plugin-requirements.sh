#!/usr/bin/env bash
# check-plugin-requirements.sh — CI gate for the pack requirements contract.
#
# A pack declares the .claude/project.json keys it needs in plugins/<pack>/requirements.json
# (see docs/EXTENDING.md → "Pack requirements"). Readers render a config form from that
# declaration, while a consumer's project.json is validated against
# overlays/_schema/project.schema.json. If the two drift, the form asks for one shape and
# the schema rejects another — so this gate pins them together.
#
# For every plugins/*/requirements.json (the file is optional per pack — absence is not an
# error) it: parses the JSON; checks version == 1, a non-empty projectConfig, and
# key/title/why/schema on every entry; then compares each entry's .schema against
# overlays/_schema/project.schema.json → properties[key] under a canonical form (object keys
# sorted recursively), so key order and whitespace never count as drift but a changed
# description does.
#
# Implementation is bash + `node -e`: node is the only runtime the CI validate job has
# (.github/workflows/ci.yml already parses every manifest with it).
#
# Exit: 0 = all declarations in sync (or no pack ships one), 1 = malformed file or drift.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if [ ! -f "${ROOT}/overlays/_schema/project.schema.json" ]; then
  echo "check-plugin-requirements: overlay schema not found at overlays/_schema/project.schema.json" >&2
  exit 1
fi

# Discovery lives in node (fs.readdirSync) rather than a shell glob: no nullglob/empty-array
# handling, and the pack list is walked in one place with the parsing.
read -r -d '' NODE_SRC <<'NODE' || true
const fs = require('fs');
const path = require('path');

const root = process.env.REQ_ROOT;
const schemaPath = path.join(root, 'overlays', '_schema', 'project.schema.json');
const pluginsDir = path.join(root, 'plugins');
const rel = (p) => path.relative(root, p);

let overlay;
try {
  overlay = JSON.parse(fs.readFileSync(schemaPath, 'utf8'));
} catch (err) {
  console.error('check-plugin-requirements: cannot parse ' + rel(schemaPath) + ': ' + err.message);
  process.exit(1);
}
const overlayProps = (overlay && overlay.properties) || {};

// Canonical form: sort object keys recursively so ordering/formatting is never "drift".
const canon = (value) => {
  if (Array.isArray(value)) return value.map(canon);
  if (value && typeof value === 'object') {
    return Object.keys(value).sort().reduce((acc, k) => {
      acc[k] = canon(value[k]);
      return acc;
    }, {});
  }
  return value;
};
const canonStr = (value) => JSON.stringify(canon(value), null, 2);

let problems = 0;
let checked = 0;
const note = (msg) => { problems += 1; console.error('  ✗ ' + msg); };

const packs = fs.existsSync(pluginsDir) ? fs.readdirSync(pluginsDir).sort() : [];

for (const pack of packs) {
  const file = path.join(pluginsDir, pack, 'requirements.json');
  if (!fs.existsSync(file)) continue; // optional per pack
  checked += 1;
  console.log('── ' + rel(file));

  let doc;
  try {
    doc = JSON.parse(fs.readFileSync(file, 'utf8'));
  } catch (err) {
    note('invalid JSON: ' + err.message);
    continue;
  }

  if (doc.version !== 1) {
    note('version must be exactly 1 (found: ' + JSON.stringify(doc.version) + ')');
    continue;
  }
  if (!Array.isArray(doc.projectConfig) || doc.projectConfig.length === 0) {
    note('projectConfig must be a non-empty array');
    continue;
  }

  doc.projectConfig.forEach((entry, i) => {
    const at = 'projectConfig[' + i + ']';
    if (!entry || typeof entry !== 'object' || Array.isArray(entry)) {
      note(at + ' is not an object');
      return;
    }
    let keyOk = true;
    for (const field of ['key', 'title', 'why']) {
      if (typeof entry[field] !== 'string' || entry[field].trim() === '') {
        note(at + '.' + field + ' is missing or not a non-empty string');
        if (field === 'key') keyOk = false;
      }
    }
    if (!entry.schema || typeof entry.schema !== 'object' || Array.isArray(entry.schema)) {
      note(at + '.schema is missing or not an object');
      return;
    }
    if (!keyOk) return;

    const key = entry.key;
    if (!Object.prototype.hasOwnProperty.call(overlayProps, key)) {
      note(at + '.key "' + key + '" has no counterpart in ' + rel(schemaPath) + ' → properties.' + key);
      return;
    }

    const want = canonStr(overlayProps[key]);
    const got = canonStr(entry.schema);
    if (want === got) {
      console.log('  ✓ ' + key + ' matches ' + rel(schemaPath) + ' → properties.' + key);
      return;
    }

    note(at + '.schema drifted from ' + rel(schemaPath) + ' → properties.' + key);
    const wantLines = want.split('\n');
    const gotLines = got.split('\n');
    const max = Math.max(wantLines.length, gotLines.length);
    let shown = 0;
    for (let l = 0; l < max && shown < 10; l += 1) {
      if (wantLines[l] === gotLines[l]) continue;
      const show = (s) => (s === undefined ? '(absent)' : s.trim());
      console.error('      canonical line ' + (l + 1) + ':');
      console.error('        overlay schema: ' + show(wantLines[l]));
      console.error('        requirements  : ' + show(gotLines[l]));
      shown += 1;
    }
    if (shown === 10) console.error('      … further differences suppressed');
  });
}

if (problems > 0) {
  console.error('');
  console.error('check-plugin-requirements: ' + problems + ' problem(s) in ' + checked + ' file(s).');
  console.error("A pack's declared config contract must stay identical to overlays/_schema/project.schema.json —");
  console.error('fix the pack file, or change both together (see docs/EXTENDING.md → "Pack requirements").');
  process.exit(1);
}

console.log('✓ plugin requirements in sync (' + checked + ' checked)');
NODE

REQ_ROOT="$ROOT" node -e "$NODE_SRC"
