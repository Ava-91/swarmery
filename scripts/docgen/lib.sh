#!/usr/bin/env bash
# lib.sh — shared plumbing for the scripts/docgen/ pipeline.
#
# Sourced (never executed) by extract.sh, generate.sh and check-coverage.sh. It
# exports two things:
#
#   DOCGEN_ROOT      the repository root, resolved from this file's own location
#                    exactly the way scripts/scan-flavor.sh does it — no project
#                    name and no absolute path is ever hard-coded.
#   DOCGEN_NODE_LIB  a JavaScript prelude implementing the `# How to use`
#                    contract from tools/swarmery/docs/system-docs-format.md.
#                    Each script concatenates it with its own program and runs
#                    the result through `node -e`.
#
# Why node: it is the only runtime the CI validate job is guaranteed to have
# (.github/workflows/ci.yml parses every manifest with `node -e`, and
# scripts/check-plugin-requirements.sh already embeds a node program the same
# way). It also gives correct JSON escaping for arbitrary markdown bodies and
# rune counting via string iteration — both of which pure bash gets wrong.
#
# The JS prelude is the single source of truth for the contract. Keeping it in
# one place is what stops extract.sh's `body_sha` and check-coverage.sh's
# subsection scan from drifting apart.

# Resolve the repo root: scripts/docgen/lib.sh -> ../.. is the root. An explicit
# DOCGEN_ROOT wins, which is how the test suite points the whole pipeline at a
# throwaway fixture tree without ever touching the real corpus.
DOCGEN_ROOT="${DOCGEN_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)}"
export DOCGEN_ROOT

read -r -d '' DOCGEN_NODE_LIB <<'DOCGEN_JS' || true
'use strict';
const crypto = require('crypto');
const fs = require('fs');
const path = require('path');

// ── §2 subsection schema ────────────────────────────────────────────────────
// Order is normative; `required` alone drives the coverage gate.
const SUBSECTIONS = [
  { key: 'what it does',        title: 'What it does',        required: true  },
  { key: 'when to use it',      title: 'When to use it',      required: true  },
  { key: 'when not to use it',  title: 'When not to use it',  required: false },
  { key: 'how to invoke',       title: 'How to invoke',       required: true  },
  { key: 'inputs',              title: 'Inputs',              required: false },
  { key: 'what you get back',   title: 'What you get back',   required: false },
  { key: 'worked example',      title: 'Worked example',      required: true  },
  { key: 'related',             title: 'Related',             required: false },
];
const REQUIRED = SUBSECTIONS.filter((s) => s.required).map((s) => s.key);
const GUIDE_HEADING = 'how to use';

// §2: required subsections carry >= 40 runes of body — with the SAME env
// override the Go side honours (sysscan.MinDocsSection / EnvMinDocsSection in
// internal/sysscan/docs.go). Hard-coding 40 here made the CI gate and the UI's
// `missing` list disagree the moment anyone set the override: one counted 40,
// the other N. Validation mirrors envInt (internal/sysscan/lint.go) exactly —
// unset or empty falls back silently, anything that is not a positive integer
// warns and falls back. Note Go's strconv.Atoi is strict where
// Number.parseInt is not ("40x" is an error, not 40), hence the regex.
const MIN_RUNES = (() => {
  const def = 40;
  const raw = process.env.SWARMERY_LINT_MIN_DOCS_SECTION;
  if (raw === undefined || raw === '') return def;
  const t = raw.trim();
  const n = /^[+-]?[0-9]+$/.test(t) ? Number.parseInt(t, 10) : NaN;
  if (!Number.isFinite(n) || n <= 0) {
    console.error(
      'warn: docgen: SWARMERY_LINT_MIN_DOCS_SECTION="' + raw +
        '" is not a positive integer — using ' + def
    );
    return def;
  }
  return n;
})();

// Runes, not bytes — the unit §2 mandates (Go's utf8.RuneCountInString).
const runeLen = (s) => Array.from(s).length;

const stripBOM = (s) => (s.charCodeAt(0) === 0xfeff ? s.slice(1) : s);

// §5.5 — a file whose first line is not `---` is not a registrable item.
function isFrontmatterStart(content) {
  const nl = content.indexOf('\n');
  const first = nl < 0 ? content : content.slice(0, nl);
  return first.replace(/[\r \t]+$/, '') === '---';
}

// splitFrontmatter mirrors sysscan.SplitFrontmatter: the raw YAML block and the
// markdown body after the closing delimiter. Offsets are returned as well so a
// caller can rewrite the frontmatter without disturbing a single byte outside it.
// Returns null for helper files and for an unterminated block.
function splitFrontmatter(content) {
  content = stripBOM(content);
  if (!isFrontmatterStart(content)) return null;
  const nl = content.indexOf('\n');
  if (nl < 0) return null;
  const fmStart = nl + 1;
  let off = fmStart;
  for (;;) {
    const nlIdx = content.indexOf('\n', off);
    const lineEnd = nlIdx < 0 ? content.length : nlIdx;
    const t = content.slice(off, lineEnd).replace(/[\r \t]+$/, '');
    if (t === '---' || t === '...') {
      const bodyStart = Math.min(lineEnd + 1, content.length);
      return {
        block: content.slice(fmStart, off),
        body: content.slice(bodyStart),
        fmStart,
        fmEnd: off,        // start of the closing delimiter line
        bodyStart,
        content,
      };
    }
    if (nlIdx < 0) break;
    off = nlIdx + 1;
    if (off > content.length) break;
  }
  return null;
}

// ── §5.1 fence-aware line scanner ───────────────────────────────────────────
// A `#` at column 0 inside a fenced region is a comment, never a heading. Fences
// open and close with ``` or ~~~ at column 0; a closing fence carries no info
// string. `raw` is the byte-exact line, `text` has the trailing CR trimmed (§5.2)
// so heading comparison never sees it.
function scanLines(text) {
  const raws = text.split('\n');
  const out = new Array(raws.length);
  let fenceChar = null;
  let fenceLen = 0;
  for (let i = 0; i < raws.length; i++) {
    const raw = raws[i];
    const line = raw.replace(/\r$/, '');
    const fm = /^(`{3,}|~{3,})(.*)$/.exec(line);
    if (fenceChar !== null) {
      if (fm && fm[1][0] === fenceChar && fm[1].length >= fenceLen && fm[2].trim() === '') {
        fenceChar = null;
        fenceLen = 0;
      }
      out[i] = { raw, text: line, fenced: true, heading: null };
      continue;
    }
    if (fm) {
      fenceChar = fm[1][0];
      fenceLen = fm[1].length;
      out[i] = { raw, text: line, fenced: true, heading: null };
      continue;
    }
    // §1.1 — a heading is 1..6 `#` at column 0 followed by at least one space or tab.
    const hm = /^(#{1,6})[ \t]+(.*)$/.exec(line);
    out[i] = {
      raw,
      text: line,
      fenced: false,
      heading: hm ? { level: hm[1].length, text: hm[2].trim() } : null,
    };
  }
  return out;
}

// Every heading outside a fenced region, in document order.
const headings = (sc) =>
  sc.reduce((acc, e, i) => {
    if (!e.fenced && e.heading) acc.push({ level: e.heading.level, text: e.heading.text, line: i + 1 });
    return acc;
  }, []);

// §5.3 — the match is on the heading text, trimmed and lowercased. §1.1 — level
// must be exactly 1, so `## How to use` is deliberately not the block.
const isGuideHeading = (e) =>
  !e.fenced && e.heading && e.heading.level === 1 && e.heading.text.trim().toLowerCase() === GUIDE_HEADING;

// All guide-heading line indices. Two of them is a §5.4 violation, not a merge.
const guideStarts = (sc) => sc.reduce((acc, e, i) => (isGuideHeading(e) ? acc.concat(i) : acc), []);

// §1.3 extent — from the heading line to the next column-0 H1 outside a fence,
// or to EOF. Returned as the half-open line range [start, end).
function blockExtent(sc, start) {
  for (let j = start + 1; j < sc.length; j++) {
    if (!sc[j].fenced && sc[j].heading && sc[j].heading.level === 1) return { start, end: j };
  }
  return { start, end: sc.length };
}

// findGuide returns the FIRST block (§5.4: the first one wins) or null.
function findGuide(sc) {
  const starts = guideStarts(sc);
  if (starts.length === 0) return null;
  const ext = blockExtent(sc, starts[0]);
  ext.duplicates = starts.length - 1;
  return ext;
}

// ── §4 source_sha ───────────────────────────────────────────────────────────
// CRLF -> LF; delete the guide block per §1.3; strip trailing spaces and tabs
// from every remaining line; join with LF; trim all trailing newlines and, if
// anything is left, terminate with exactly one LF. First 12 lowercase hex of the
// SHA-256. Frontmatter is not part of the input, and the guide is removed before
// hashing, so rewriting either can never change the value.
function sourceSHA(body) {
  const normalized = body.replace(/\r\n/g, '\n');
  const sc = scanLines(normalized);
  const blk = findGuide(sc);
  let lines = sc.map((e) => e.raw);
  if (blk) lines = lines.slice(0, blk.start).concat(lines.slice(blk.end));
  lines = lines.map((l) => l.replace(/[ \t]+$/, ''));
  let s = lines.join('\n').replace(/\n+$/, '');
  if (s !== '') s += '\n';
  return crypto.createHash('sha256').update(Buffer.from(s, 'utf8')).digest('hex').slice(0, 12);
}

// ── §2 subsections of a guide block ─────────────────────────────────────────
// Body text is everything between an H2 and the next H2 (or the end of the
// block), heading line excluded, whitespace trimmed. `###` and deeper are body
// content of the subsection they sit under, never subsections themselves.
function guideSubsections(sc, blk) {
  const out = [];
  let cur = null;
  for (let i = blk.start + 1; i < blk.end; i++) {
    const e = sc[i];
    if (!e.fenced && e.heading && e.heading.level === 2) {
      if (cur) out.push(cur);
      cur = { title: e.heading.text, lines: [] };
      continue;
    }
    if (cur) cur.lines.push(e.raw);
  }
  if (cur) out.push(cur);
  return out.map((s) => {
    const body = s.lines.join('\n').trim();
    return { title: s.title, key: s.title.trim().toLowerCase(), body, runes: runeLen(body) };
  });
}

// coverageProblems returns the human-readable reasons a file fails the gate.
// An empty array means the item is documented. Recommended subsections are
// never reported here — §2 keeps them at info severity, outside the gate.
function coverageProblems(sc) {
  const starts = guideStarts(sc);
  if (starts.length === 0) return ['no `# How to use` block'];
  const problems = [];
  if (starts.length > 1) {
    problems.push('duplicate `# How to use` block (' + starts.length + ' found; §5.4 keeps the first)');
  }
  const blk = blockExtent(sc, starts[0]);
  const subs = guideSubsections(sc, blk);
  const byKey = new Map();
  for (const s of subs) if (!byKey.has(s.key)) byKey.set(s.key, s);
  for (const key of REQUIRED) {
    const title = SUBSECTIONS.find((s) => s.key === key).title;
    const found = byKey.get(key);
    if (!found) {
      problems.push('missing required subsection `## ' + title + '`');
    } else if (found.runes < MIN_RUNES) {
      problems.push(
        '`## ' + title + '` has ' + found.runes + ' runes of body, needs ' + MIN_RUNES
      );
    }
  }
  return problems;
}

// ── frontmatter: tolerant, line-oriented ────────────────────────────────────
// Deliberately NOT a YAML round-trip. sysscan/frontmatter.go tolerates comments
// between keys and folded scalars in the real corpus; a round-trip reflows both.
// This reader keeps each top-level key's line range so the writer can splice one
// key and leave every other byte alone.
function unquote(v) {
  if (v.length >= 2) {
    const a = v[0];
    const z = v[v.length - 1];
    if (a === '"' && z === '"') return v.slice(1, -1).replace(/\\"/g, '"').replace(/\\\\/g, '\\');
    if (a === "'" && z === "'") return v.slice(1, -1).replace(/''/g, "'");
  }
  return v;
}

const TOP_KEY = /^([A-Za-z0-9_.-]+):[ \t]*(.*)$/;
const NESTED_KEY = /^[ \t]+([A-Za-z0-9_.-]+):[ \t]*(.*)$/;
const LIST_ITEM = /^[ \t]*-[ \t]*(.*)$/;

function parseFrontmatter(block) {
  const lines = block.split('\n');
  const fields = {};
  const ranges = {};
  let i = 0;
  while (i < lines.length) {
    const line = lines[i].replace(/\r$/, '');
    if (line.trim() === '' || /^[ \t]*#/.test(line)) {
      i += 1;
      continue;
    }
    const m = TOP_KEY.exec(line);
    if (!m) {
      i += 1;
      continue;
    }
    const key = m[1];
    const inline = m[2].trim();
    const start = i;
    let value;
    let next = i + 1;
    if (inline === '' || inline === '>' || inline === '|' || inline === '>-' || inline === '|-') {
      const cont = [];
      let j = i + 1;
      while (j < lines.length) {
        const l = lines[j].replace(/\r$/, '');
        if (l.trim() === '') { cont.push(l); j += 1; continue; }
        if (!/^[ \t]/.test(l)) break;
        cont.push(l); j += 1;
      }
      next = j;
      while (cont.length && cont[cont.length - 1].trim() === '') cont.pop();
      const first = cont.find((l) => l.trim() !== '');
      if (first === undefined) {
        value = '';
      } else if (inline !== '') {
        // folded or literal scalar — fold to one line, which is all any consumer needs
        value = cont.map((l) => l.replace(/^[ \t]+/, '')).join(' ').trim();
      } else if (LIST_ITEM.test(first)) {
        value = cont
          .filter((l) => l.trim() !== '')
          .map((l) => unquote(LIST_ITEM.exec(l)[1].trim()));
      } else if (NESTED_KEY.test(first)) {
        const obj = {};
        for (const l of cont) {
          const mm = NESTED_KEY.exec(l);
          // Inside a constrained mapping a trailing ` # comment` is far likelier
          // than a literal hash, so strip it here and nowhere else.
          if (mm) obj[mm[1]] = unquote(mm[2].replace(/[ \t]+#.*$/, '').trim());
        }
        value = obj;
      } else {
        value = cont.map((l) => l.trim()).join(' ');
      }
    } else {
      value = unquote(inline);
    }
    if (!(key in fields)) {
      fields[key] = value;
      ranges[key] = { start, end: next };
    }
    i = next;
  }
  return { fields, ranges, lines };
}

// Scalar field access that tolerates the type drift §3 warns about.
function strField(fields, key) {
  const v = fields[key];
  if (v === undefined || v === null) return '';
  if (typeof v === 'string') return v.trim();
  if (Array.isArray(v)) return v.join(', ');
  return String(v).trim();
}

// §3 — `docs:` is a mapping. Anything else is unknown provenance, never a crash.
function docsProvenance(fields) {
  const v = fields.docs;
  if (!v || typeof v !== 'object' || Array.isArray(v)) return null;
  return {
    status: typeof v.status === 'string' ? v.status : '',
    source_sha: typeof v.source_sha === 'string' ? v.source_sha : '',
    updated: typeof v.updated === 'string' ? v.updated : '',
  };
}

// ── item identity ───────────────────────────────────────────────────────────
// Path shape decides the kind; registry.go decides the name and the composite.
function classify(relPath) {
  const parts = relPath.split('/');
  const base = parts[parts.length - 1];
  let kind = '';
  if (base === 'SKILL.md' && parts.length >= 3 && parts[parts.length - 3] === 'skills') kind = 'skill';
  else if (parts.length >= 2 && parts[parts.length - 2] === 'agents') kind = 'agent';
  else if (parts.length >= 2 && parts[parts.length - 2] === 'commands') kind = 'command';
  // The plugin is the segment right after `plugins/`. Items outside plugins/
  // (fixtures, a consumer's own .claude/) have no plugin and no composite name.
  let plugin = '';
  const pi = parts.indexOf('plugins');
  if (pi >= 0 && parts.length > pi + 1) plugin = parts[pi + 1];
  return { kind, plugin };
}

// registry.go: agents fall back to the file stem, skills ALWAYS take the
// directory stem (the frontmatter name is ignored), commands always the file stem.
function itemName(relPath, kind, fields) {
  const parts = relPath.split('/');
  const stem = (s) => s.replace(/\.md$/, '');
  if (kind === 'skill') return parts[parts.length - 2];
  if (kind === 'agent') return strField(fields, 'name') || stem(parts[parts.length - 1]);
  return stem(parts[parts.length - 1]);
}

// Computed, never read out of prose. registry.go prefixes plugin items with
// `<plugin>:` — the composite naming rule.
function invocationFor(kind, plugin, name) {
  const composite = plugin ? plugin + ':' + name : name;
  if (kind === 'agent') return '@' + composite;
  if (kind === 'skill') return 'Skill(skill: "' + composite + '")';
  if (kind === 'command') return '/' + name;
  return '';
}

// ── corpus walk ─────────────────────────────────────────────────────────────
// The three item globs, resolved without a shell glob so an empty pack is not
// an error and the order is stable.
function listItems(root) {
  const items = [];
  const pluginsDir = path.join(root, 'plugins');
  if (!fs.existsSync(pluginsDir)) return items;
  const ls = (dir) => (fs.existsSync(dir) ? fs.readdirSync(dir).sort() : []);
  for (const pack of ls(pluginsDir)) {
    const packDir = path.join(pluginsDir, pack);
    if (!fs.statSync(packDir).isDirectory()) continue;
    for (const f of ls(path.join(packDir, 'agents'))) {
      if (f.endsWith('.md')) items.push(path.join(packDir, 'agents', f));
    }
    for (const d of ls(path.join(packDir, 'skills'))) {
      const p = path.join(packDir, 'skills', d, 'SKILL.md');
      if (fs.existsSync(p)) items.push(p);
    }
    for (const f of ls(path.join(packDir, 'commands'))) {
      if (f.endsWith('.md')) items.push(path.join(packDir, 'commands', f));
    }
  }
  return items;
}
DOCGEN_JS

export DOCGEN_NODE_LIB
