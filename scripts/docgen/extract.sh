#!/usr/bin/env bash
# extract.sh — deterministic brief for one registrable item. No LLM, no writes.
#
# Emits a single JSON object on stdout describing everything a writer (human or
# model) needs in order to produce the item's `# How to use` block, per
# tools/swarmery/docs/system-docs-format.md:
#
#   path, kind, plugin, name        identity — kind from the path shape, name and
#                                   the composite prefix exactly as sysscan's
#                                   registry.go resolves them
#   description, model, allowed-tools   frontmatter, tolerantly read
#   headings                        every heading OUTSIDE a fenced region (§5.1)
#   invocation                      COMPUTED from kind + plugin + name, never
#                                   scraped out of prose
#   existing_examples               the body of an existing examples/usage
#                                   section, when the item already has one
#   existing_block                  the current guide, if any
#   body_sha                        §4 fingerprint: sha256 of the body with the
#                                   guide removed, first 12 hex
#   fm_docs                         the current `docs:` provenance map, or null
#
# `body_sha` is the same computation as internal/sysscan — that agreement is a
# contract, not a coincidence: the generator's idempotency and the linter's
# staleness check both hinge on the two implementations returning one value.
#
# Exit: 0 = brief written, 1 = usage error, unreadable file, or a file that is
# not a registrable item (no frontmatter, §5.5).
set -euo pipefail

# shellcheck source=scripts/docgen/lib.sh
. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

if [ "$#" -ne 1 ]; then
  echo "usage: extract.sh <file>" >&2
  exit 1
fi

if [ ! -f "$1" ]; then
  echo "extract: not a file: $1" >&2
  exit 1
fi

read -r -d '' EXTRACT_JS <<'EXTRACT' || true

const target = process.argv[1];
const root = process.env.DOCGEN_ROOT || process.cwd();
const abs = path.resolve(target);
const rel = path.relative(root, abs).split(path.sep).join('/');
// Items outside the repo root keep the path they were given; inside it, the
// brief always speaks in repo-relative paths.
const relPath = rel.startsWith('..') ? target.split(path.sep).join('/') : rel;

const content = stripBOM(fs.readFileSync(abs, 'utf8'));
const fm = splitFrontmatter(content);
if (fm === null) {
  console.error('extract: not a registrable item (no parsable frontmatter, §5.5): ' + relPath);
  process.exit(1);
}

const parsed = parseFrontmatter(fm.block);
const { kind, plugin } = classify(relPath);
if (kind === '') {
  console.error('extract: path shape is not an agent, skill or command: ' + relPath);
  process.exit(1);
}
const name = itemName(relPath, kind, parsed.fields);

const sc = scanLines(fm.body.replace(/\r\n/g, '\n'));
const guide = findGuide(sc);

// An existing examples/usage section is the best raw material a writer has for
// `## Worked example`; 39 of 54 agents and 33 of 60 skills already carry one.
// Extent is the same rule as any section: until the next heading of the same or
// a shallower level, outside fences.
const EXAMPLE_TITLES = ['examples', 'example', 'usage', 'worked example', 'worked examples'];
function sectionBody(startIdx) {
  const level = sc[startIdx].heading.level;
  const lines = [];
  for (let i = startIdx + 1; i < sc.length; i++) {
    const e = sc[i];
    if (!e.fenced && e.heading && e.heading.level <= level) break;
    lines.push(e.raw);
  }
  return lines.join('\n').trim();
}
let existingExamples = '';
for (let i = 0; i < sc.length; i++) {
  const e = sc[i];
  if (e.fenced || !e.heading) continue;
  if (guide && i >= guide.start && i < guide.end) continue; // the guide's own example is not source material
  if (!EXAMPLE_TITLES.includes(e.heading.text.trim().toLowerCase())) continue;
  existingExamples = sectionBody(i);
  break;
}
// Keep the brief bounded — a prompt is not an archive.
const CAP = 4000;
if (Array.from(existingExamples).length > CAP) {
  existingExamples = Array.from(existingExamples).slice(0, CAP).join('') + '\n…(truncated)';
}

const brief = {
  path: relPath,
  kind,
  plugin: plugin || null,
  name,
  description: strField(parsed.fields, 'description'),
  model: strField(parsed.fields, 'model'),
  'allowed-tools': parsed.fields['allowed-tools'] === undefined
    ? null
    : parsed.fields['allowed-tools'],
  headings: headings(sc),
  invocation: invocationFor(kind, plugin, name),
  existing_examples: existingExamples,
  existing_block: guide ? sc.slice(guide.start, guide.end).map((e) => e.raw).join('\n').replace(/\s+$/, '') : null,
  body_sha: sourceSHA(fm.body),
  fm_docs: docsProvenance(parsed.fields),
};

process.stdout.write(JSON.stringify(brief, null, 2) + '\n');
EXTRACT

node -e "${DOCGEN_NODE_LIB}${EXTRACT_JS}" -- "$1"
