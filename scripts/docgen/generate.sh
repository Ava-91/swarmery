#!/usr/bin/env bash
# generate.sh — write (or rewrite) the `# How to use` block of one or more items.
#
#   generate.sh [--force] [--dry-run] <file>...
#
# Per file:
#   1. run extract.sh for the deterministic brief;
#   2. SKIP when fm_docs.source_sha already equals body_sha AND the existing block
#      passes the coverage gate — this is the idempotency contract, and it is what
#      makes a re-run over the whole corpus a no-op unless an item actually changed;
#   3. otherwise ask the model for a new block, validate it, and splice it in.
#
# The splice never reformats anything the author wrote. The block replaces the old
# block in place (or is appended at the end of the body when there is none), and
# the frontmatter `docs:` map is rewritten as a LINE-ORIENTED edit of that one key.
# A YAML round-trip is deliberately avoided: sysscan/frontmatter.go tolerates
# comments between keys and folded scalars in the real corpus, and a round-trip
# reflows both.
#
# --dry-run prints the block to stdout and touches nothing.
# --force regenerates even when the guide is current.
#
# Env:
#   DOCGEN_LLM_CMD  the model seam (see below)
#   DOCGEN_DATE     the date written to `docs.updated` (default: today)
#   DOCGEN_ROOT     repo root (default: resolved from lib.sh)
#
# Exit: 0 when every file was skipped, printed, or written; 1 when any file failed.
set -euo pipefail

DOCGEN_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=scripts/docgen/lib.sh
. "${DOCGEN_DIR}/lib.sh"

STYLE="${DOCGEN_DIR}/style.md"
EXTRACT="${DOCGEN_DIR}/extract.sh"

force=0
dry_run=0
files=()

usage() {
  echo "usage: generate.sh [--force] [--dry-run] <file>..." >&2
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --force)   force=1; shift ;;
    --dry-run) dry_run=1; shift ;;
    -h|--help) usage; exit 0 ;;
    --)        shift; while [ "$#" -gt 0 ]; do files+=("$1"); shift; done ;;
    -*)        echo "generate: unknown option: $1" >&2; usage; exit 1 ;;
    *)         files+=("$1"); shift ;;
  esac
done

if [ "${#files[@]}" -eq 0 ]; then
  usage
  exit 1
fi

if [ ! -f "$STYLE" ]; then
  echo "generate: style prompt not found at ${STYLE}" >&2
  exit 1
fi

# ── the model seam ──────────────────────────────────────────────────────────
# run_llm reads the user prompt on stdin and writes the raw block on stdout.
#
# DOCGEN_LLM_CMD replaces the real invocation wholesale and is given the style
# file as its single argument. It exists so scripts/tests/docgen.test.sh can run
# the entire pipeline offline and deterministically against a canned block — a
# test suite that reaches for a live model is neither. Leave it unset in normal
# use and the real headless call below runs.
run_llm() {
  local style="$1"
  if [ -n "${DOCGEN_LLM_CMD:-}" ]; then
    # Intentionally unquoted: DOCGEN_LLM_CMD may carry its own arguments.
    # shellcheck disable=SC2086
    ${DOCGEN_LLM_CMD} "$style"
  else
    claude -p --output-format text --append-system-prompt "$(cat "$style")"
  fi
}

read -r -d '' PLAN_JS <<'PLAN' || true

const file = process.argv[1];
const briefPath = process.argv[2];
const promptPath = process.argv[3];
const force = process.env.DOCGEN_FORCE === '1';
const root = process.env.DOCGEN_ROOT || process.cwd();

const brief = JSON.parse(fs.readFileSync(briefPath, 'utf8'));
const content = stripBOM(fs.readFileSync(file, 'utf8'));
const fm = splitFrontmatter(content);
if (fm === null) {
  console.error('generate: not a registrable item: ' + brief.path);
  process.exit(2);
}
const body = fm.body.replace(/\r\n/g, '\n');
const sc = scanLines(body);
const gaps = coverageProblems(sc);
const current = brief.fm_docs && brief.fm_docs.source_sha === brief.body_sha;

// The idempotency contract: provenance still fingerprints this exact body AND
// the block it describes still passes the gate. Either half failing means the
// guide has to be written again.
if (!force && current && gaps.length === 0) {
  process.stdout.write('SKIP\n');
  process.exit(0);
}

// The schema travels with the prompt, lifted out of the contract doc itself so
// the two can never drift. §2 is the shape, §6 is the voice.
let schema = '';
const contractPath = path.join(root, 'tools', 'swarmery', 'docs', 'system-docs-format.md');
if (fs.existsSync(contractPath)) {
  const doc = fs.readFileSync(contractPath, 'utf8').replace(/\r\n/g, '\n');
  const wanted = ['## §2', '## §6'];
  const lines = doc.split('\n');
  let keep = false;
  const picked = [];
  for (const l of lines) {
    if (/^## /.test(l)) keep = wanted.some((w) => l.startsWith(w));
    if (keep) picked.push(l);
  }
  schema = picked.join('\n').trim();
}

const CAP = 60000;
let itemBody = body.trim();
if (itemBody.length > CAP) itemBody = itemBody.slice(0, CAP) + '\n…(item truncated)';

const reason = force
  ? 'forced regeneration'
  : gaps.length > 0
    ? 'gate: ' + gaps.join('; ')
    : 'provenance is stale or absent';

const prompt = [
  'Write the `# How to use` block for the item below.',
  '',
  schema ? '## The contract\n\n' + schema + '\n' : '',
  '## The brief',
  '',
  '```json',
  JSON.stringify(brief, null, 2),
  '```',
  '',
  'The `invocation` value above — `' + brief.invocation + '` — must appear verbatim',
  'inside the fenced block under `## How to invoke`. Do not reword it.',
  '',
  '## The item, in full',
  '',
  '```markdown',
  itemBody,
  '```',
  '',
  'Return only the block, starting with the line `# How to use`.',
  '',
].join('\n');

fs.writeFileSync(promptPath, prompt, 'utf8');
process.stdout.write('GENERATE ' + reason + '\n');
PLAN

read -r -d '' APPLY_JS <<'APPLY' || true

const file = process.argv[1];
const briefPath = process.argv[2];
const rawPath = process.argv[3];
const dryRun = process.env.DOCGEN_DRY_RUN === '1';

const brief = JSON.parse(fs.readFileSync(briefPath, 'utf8'));
let raw = fs.readFileSync(rawPath, 'utf8').replace(/\r\n/g, '\n').trim();
const fail = (why) => {
  console.error('PROBLEM: ' + brief.path + ' — ' + why);
  process.exit(1);
};

// Tolerate a model that wrapped the whole answer in an outer fence.
const wrapped = /^(`{3,}|~{3,})[^\n]*\n([\s\S]*)\n\1[^\S\n]*$/.exec(raw);
if (wrapped) raw = wrapped[2].trim();

// Locate the block: the first `# How to use` H1 outside a fence. Anything the
// model printed before it is preamble and is dropped; a stray H1 after the block
// ends it.
const rawSc = scanLines(raw);
const guide = findGuide(rawSc);
if (!guide) fail('model output has no `# How to use` H1 heading');
const blockText = rawSc.slice(guide.start, guide.end).map((e) => e.raw).join('\n').replace(/\s+$/, '');

// Validate the block on its own terms before it is allowed near the file.
const blockSc = scanLines(blockText);
const problems = coverageProblems(blockSc);
if (problems.length > 0) fail('generated block fails the gate: ' + problems.join('; '));
if (brief.invocation && !blockText.includes(brief.invocation)) {
  fail('generated block does not carry the invocation `' + brief.invocation + '` verbatim');
}

if (dryRun) {
  process.stdout.write(blockText + '\n');
  process.exit(0);
}

const content = stripBOM(fs.readFileSync(file, 'utf8'));
const fm = splitFrontmatter(content);
if (fm === null) fail('not a registrable item');
const body = fm.body.replace(/\r\n/g, '\n');
const sc = scanLines(body);
const existing = findGuide(sc);
const bodyLines = sc.map((e) => e.raw);
const blockLines = blockText.split('\n');

// §1.2 — a block that is not last is rewritten IN PLACE; the reader's layout wins
// over the generator's convenience. Only a body with no block gets an append.
let newBody;
if (existing) {
  const tail = bodyLines.slice(existing.end);
  const sep = tail.length > 0 && tail[0].trim() !== '' ? [''] : [];
  newBody = bodyLines.slice(0, existing.start).concat(blockLines, sep, tail).join('\n');
} else {
  const head = body.replace(/[ \t\n]+$/, '');
  newBody = (head === '' ? '' : head + '\n\n') + blockLines.join('\n') + '\n';
}
if (!newBody.endsWith('\n')) newBody += '\n';

// Line-oriented splice of the single `docs:` key — every other frontmatter byte
// is preserved exactly, comments and folded scalars included.
const pad = (n) => String(n).padStart(2, '0');
const now = new Date();
const today =
  process.env.DOCGEN_DATE ||
  now.getFullYear() + '-' + pad(now.getMonth() + 1) + '-' + pad(now.getDate());
const docsLines = [
  'docs:',
  '  status: generated',
  '  source_sha: ' + sourceSHA(newBody),
  '  updated: ' + today,
];

const fmLines = fm.block.split('\n');
let start = -1;
for (let i = 0; i < fmLines.length; i++) {
  if (/^docs:/.test(fmLines[i].replace(/\r$/, ''))) { start = i; break; }
}
let newFmLines;
if (start >= 0) {
  let end = fmLines.length;
  for (let j = start + 1; j < fmLines.length; j++) {
    const l = fmLines[j].replace(/\r$/, '');
    if (l.trim() === '' || /^[ \t]/.test(l)) continue; // blank or indented — still the map
    end = j;
    break;
  }
  while (end > start + 1 && fmLines[end - 1].trim() === '') end -= 1;
  newFmLines = fmLines.slice(0, start).concat(docsLines, fmLines.slice(end));
} else {
  let k = fmLines.length;
  while (k > 0 && fmLines[k - 1].trim() === '') k -= 1;
  newFmLines = fmLines.slice(0, k).concat(docsLines, fmLines.slice(k));
}

const out =
  content.slice(0, fm.fmStart) +
  newFmLines.join('\n') +
  content.slice(fm.fmEnd, fm.bodyStart) +
  newBody;
fs.writeFileSync(file, out, 'utf8');
process.stdout.write('WROTE\n');
APPLY

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

status=0
for f in "${files[@]}"; do
  if [ ! -f "$f" ]; then
    echo "PROBLEM: $f — not a file" >&2
    status=1
    continue
  fi

  if ! bash "$EXTRACT" "$f" > "$tmp/brief.json"; then
    echo "PROBLEM: $f — extract failed" >&2
    status=1
    continue
  fi

  plan="$(DOCGEN_FORCE="$force" node -e "${DOCGEN_NODE_LIB}${PLAN_JS}" \
    "$f" "$tmp/brief.json" "$tmp/prompt.txt")" || { status=1; continue; }

  if [ "${plan%% *}" = "SKIP" ]; then
    echo "SKIP: $f — guide is current"
    continue
  fi

  if ! run_llm "$STYLE" < "$tmp/prompt.txt" > "$tmp/raw.md"; then
    echo "PROBLEM: $f — the model call failed" >&2
    status=1
    continue
  fi

  if DOCGEN_DRY_RUN="$dry_run" node -e "${DOCGEN_NODE_LIB}${APPLY_JS}" \
      "$f" "$tmp/brief.json" "$tmp/raw.md" > "$tmp/out.txt"; then
    if [ "$dry_run" -eq 1 ]; then
      cat "$tmp/out.txt"
    else
      echo "WROTE: $f — ${plan#GENERATE }"
    fi
  else
    status=1
  fi
done

exit "$status"
