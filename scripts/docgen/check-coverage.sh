#!/usr/bin/env bash
# check-coverage.sh — the `# How to use` coverage gate.
#
# Walks the three item globs (plugins/*/agents/*.md, plugins/*/skills/*/SKILL.md,
# plugins/*/commands/*.md) and, for every registrable item, checks the two things
# tools/swarmery/docs/system-docs-format.md makes gateable:
#
#   1. exactly one `# How to use` H1 outside a fenced region (§1.1, §1.4, §5.4);
#   2. all four REQUIRED subsections present, each carrying >= 40 runes of body
#      (§2) — What it does, When to use it, How to invoke, Worked example.
#
# The four recommended subsections are deliberately NOT checked: §2 keeps them at
# info severity so a guide with the required four is a passing, documented item.
# Review status is not checked either — `status: reviewed` is a human act, tracked
# by the docs_unreviewed lint rule, never by CI.
#
# Output mirrors the voice of the existing "Agent frontmatter" CI step
# (.github/workflows/ci.yml): one `PROBLEM: <path> — <why>` line per failure, then
# a `checked=N documented=M problems=K` summary.
#
# Exit: 0 when problems <= ${DOCS_MAX_PROBLEMS:-0}, else 1. DOCS_MAX_PROBLEMS is
# the ratchet knob — set it to today's baseline and lower it as the corpus is
# backfilled, so coverage can only ever improve.
#
# Env:
#   DOCS_MAX_PROBLEMS  tolerated problem count (default 0)
#   DOCGEN_ROOT        corpus root (default: this repo, resolved from lib.sh)
set -euo pipefail

# shellcheck source=scripts/docgen/lib.sh
. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

read -r -d '' COVERAGE_JS <<'COVERAGE' || true

const root = process.env.DOCGEN_ROOT || process.cwd();
const maxProblems = Number.parseInt(process.env.DOCS_MAX_PROBLEMS || '0', 10);
if (!Number.isFinite(maxProblems) || maxProblems < 0) {
  console.error('check-coverage: DOCS_MAX_PROBLEMS must be a non-negative integer');
  process.exit(1);
}

let checked = 0;
let documented = 0;
let problems = 0;
const lines = [];
const report = (rel, why) => {
  problems += 1;
  lines.push('PROBLEM: ' + rel + ' — ' + why);
};

for (const abs of listItems(root)) {
  const rel = path.relative(root, abs).split(path.sep).join('/');
  let content;
  try {
    content = stripBOM(fs.readFileSync(abs, 'utf8'));
  } catch (err) {
    checked += 1;
    report(rel, 'unreadable: ' + err.message);
    continue;
  }
  // §5.5 — no leading `---` means this is a helper file, not an item. Skipped
  // silently and completely: it is not counted, not reported, not documented.
  if (!isFrontmatterStart(content)) continue;

  checked += 1;
  const fm = splitFrontmatter(content);
  if (fm === null) {
    report(rel, 'unterminated frontmatter (no closing `---`)');
    continue;
  }
  const sc = scanLines(fm.body.replace(/\r\n/g, '\n'));
  const found = coverageProblems(sc);
  if (found.length === 0) {
    documented += 1;
    continue;
  }
  for (const why of found) report(rel, why);
}

for (const l of lines) console.log(l);
console.log('checked=' + checked + ' documented=' + documented + ' problems=' + problems);

if (problems > maxProblems) {
  console.error(
    'check-coverage: ' + problems + ' problem(s) exceed DOCS_MAX_PROBLEMS=' + maxProblems + '.'
  );
  console.error(
    'Every agent, skill and command owes its reader one `# How to use` block with the four'
  );
  console.error(
    'required subsections — see tools/swarmery/docs/system-docs-format.md, or run'
  );
  console.error('scripts/docgen/generate.sh on the offending files.');
  process.exit(1);
}
COVERAGE

node -e "${DOCGEN_NODE_LIB}${COVERAGE_JS}"
