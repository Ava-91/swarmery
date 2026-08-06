#!/bin/bash
# Behavioral tests for the scripts/docgen/ pipeline.
#
# Framework-free and offline (portable, no bats and no model call): every case
# builds a throwaway item tree under mktemp, points DOCGEN_ROOT at it, and drives
# the real scripts. The LLM leg is exercised through generate.sh's DOCGEN_LLM_CMD
# seam with a stub that prints a canned block, so the suite is deterministic and
# costs nothing. Run locally with `bash scripts/tests/docgen.test.sh`; phase 7 wires
# it into the shell-test step in .github/workflows/ci.yml.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DOCGEN="${ROOT}/scripts/docgen"

pass=0
fail=0
skipped=0

ok()      { pass=$((pass + 1)); printf '  ok    %s\n' "$1"; }
bad()     { fail=$((fail + 1)); printf '  FAIL  %s\n        %s\n' "$1" "$2"; }
skip()    { skipped=$((skipped + 1)); printf '  skip  %s (%s)\n' "$1" "$2"; }

# eq <desc> <expected> <actual>
eq() { if [ "$2" = "$3" ]; then ok "$1"; else bad "$1" "expected [$2], got [$3]"; fi; }
# ne <desc> <unexpected> <actual>
ne() { if [ "$2" != "$3" ]; then ok "$1"; else bad "$1" "expected a value other than [$2]"; fi; }
# has <desc> <haystack> <needle>
has() { case "$2" in *"$3"*) ok "$1" ;; *) bad "$1" "missing [$3]" ;; esac; }
# hasnt <desc> <haystack> <needle>
hasnt() { case "$2" in *"$3"*) bad "$1" "unexpected [$3]" ;; *) ok "$1" ;; esac; }

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

# jget <brief.json> <dotted.path> — read one field out of a brief.
jget() {
  node -e '
    const fs = require("fs");
    const j = JSON.parse(fs.readFileSync(process.argv[1], "utf8"));
    const v = process.argv[2].split(".").reduce((a, k) => (a == null ? a : a[k]), j);
    process.stdout.write(v === undefined || v === null ? "" : (typeof v === "string" ? v : JSON.stringify(v)));
  ' "$1" "$2"
}

brief() { bash "${DOCGEN}/extract.sh" "$1" > "$TMP/brief.json"; }

# fmkeys <file> — the TOP-LEVEL frontmatter keys, in file order, comma-joined.
# Indented keys inside a mapping are deliberately not matched: the property under
# test is that the line-oriented `docs:` splice leaves every other key where the
# author put it.
fmkeys() {
  node -e '
    const fs = require("fs");
    const lines = fs.readFileSync(process.argv[1], "utf8").split("\n");
    const keys = [];
    for (let i = 1; i < lines.length; i++) {
      const l = lines[i].replace(/\r$/, "");
      if (l === "---" || l === "...") break;
      const m = /^([A-Za-z0-9_.-]+):/.exec(l);
      if (m) keys.push(m[1]);
    }
    process.stdout.write(keys.join(","));
  ' "$1"
}

# ── fixture tree ────────────────────────────────────────────────────────────
REPO="$TMP/repo"
mkdir -p "$REPO/plugins/testpack/agents" \
         "$REPO/plugins/testpack/skills/sample-skill" \
         "$REPO/plugins/testpack/commands"

cat > "$REPO/plugins/testpack/agents/complete-agent.md" <<'FIXTURE'
---
name: complete-agent
description: Fixture agent whose guide carries all four required subsections.
---

# Role

Fixture agent for the docgen test suite.

# How to use

## What it does
Turns a batch of order line items into a priced order, so you never have to sum the
line totals by hand.

## When to use it
- A caller sent line items and you need one priced order back.
- A line changed and the order total has to be recomputed.

## How to invoke

```
@testpack:complete-agent price the order in orders/line-items/1042
```

Pass the order path; everything else is read from the order document itself.

## Worked example

```
> @testpack:complete-agent price the order in orders/line-items/1042
reads 3 line items and writes the priced order back
PRICED | lines: 3 | total: 148.20
```

You end up with the same document, now carrying a total and a per-line breakdown.
FIXTURE

cat > "$REPO/plugins/testpack/agents/no-guide-agent.md" <<'FIXTURE'
---
name: no-guide-agent
description: Fixture agent with no usage guide at all.
---

# Role

Fixture agent that the generator has never touched.

# Boundaries

- Never writes to the order store.
FIXTURE

# Guide is complete except for `## Worked example`, and the fenced bash block
# carries the false-heading trap from system-docs-format.md §5.1.
cat > "$REPO/plugins/testpack/skills/sample-skill/SKILL.md" <<'FIXTURE'
---
name: whatever-the-frontmatter-says
description: Fixture skill whose directory stem is the identity, not this name.
---

# Purpose

Fixture skill for the docgen test suite.

# How to use

## What it does
Renders the deployment command for an order-processing service so you can read the
exact invocation before anything runs.

## When to use it
- You are about to deploy and want the literal command in front of you first.
- You are reviewing a change and need to know what the pipeline will run.

## How to invoke

```bash
# deploy the thing
deploy --service orders --env <envAlias>
```

Run the printed command yourself; the skill never executes it.
FIXTURE

cat > "$REPO/plugins/testpack/commands/sample-command.md" <<'FIXTURE'
---
description: Fixture command with no usage guide.
---

# Purpose

Fixture command for the docgen test suite.
FIXTURE

echo "docgen: extract"

# ── 1. kind + invocation, all three item kinds ──────────────────────────────
brief "$REPO/plugins/testpack/agents/complete-agent.md"
eq "agent: kind is agent"            "agent" "$(jget "$TMP/brief.json" kind)"
eq "agent: composite invocation"     "@testpack:complete-agent" "$(jget "$TMP/brief.json" invocation)"

brief "$REPO/plugins/testpack/skills/sample-skill/SKILL.md"
eq "skill: name is the DIRECTORY stem, not the frontmatter name" \
   "sample-skill" "$(jget "$TMP/brief.json" name)"
eq "skill: Skill() invocation"       'Skill(skill: "testpack:sample-skill")' \
   "$(jget "$TMP/brief.json" invocation)"

# ── 2. §5.1 — a `#` inside a fence is a comment, not a heading ──────────────
hasnt "skill: fenced '# deploy the thing' is not a heading" \
      "$(jget "$TMP/brief.json" headings)" "deploy the thing"
has   "skill: real headings are still listed" \
      "$(jget "$TMP/brief.json" headings)" "How to invoke"

brief "$REPO/plugins/testpack/commands/sample-command.md"
eq "command: kind is command"        "command" "$(jget "$TMP/brief.json" kind)"
eq "command: slash invocation"       "/sample-command" "$(jget "$TMP/brief.json" invocation)"
eq "command: no guide yet"           "" "$(jget "$TMP/brief.json" existing_block)"

# ── 3. §4 — body_sha ignores the guide entirely ─────────────────────────────
AGENT="$REPO/plugins/testpack/agents/complete-agent.md"
brief "$AGENT"
sha_before="$(jget "$TMP/brief.json" body_sha)"

# Whitespace-only edit INSIDE the block: trailing spaces on a subsection heading
# plus an extra blank line at the end of the file (the block runs to EOF).
ws="$TMP/repo/plugins/testpack/agents/ws-agent.md"
sed 's/^## What it does$/## What it does   /' "$AGENT" > "$ws"
printf '\n\n' >> "$ws"
brief "$ws"
eq "body_sha survives a whitespace-only edit inside the block" \
   "$sha_before" "$(jget "$TMP/brief.json" body_sha)"

# A real edit OUTSIDE the block must move it — otherwise staleness never fires.
outside="$TMP/repo/plugins/testpack/agents/outside-agent.md"
sed 's/^Fixture agent for the docgen test suite\.$/Fixture agent for the docgen test suite, revised./' \
  "$AGENT" > "$outside"
brief "$outside"
ne "body_sha moves when the item body outside the block changes" \
   "$sha_before" "$(jget "$TMP/brief.json" body_sha)"
rm -f "$ws" "$outside"

# ── 4. the phase-1 fixtures pin body_sha to known values ────────────────────
FIX="${ROOT}/tools/swarmery/testdata/sysconfig/claude"
check_fixture() {
  local file="$1" want="$2" desc="$3"
  if [ ! -f "$file" ]; then skip "$desc" "fixture not present"; return; fi
  brief "$file"
  eq "$desc" "$want" "$(jget "$TMP/brief.json" body_sha)"
}
check_fixture "$FIX/agents/documented-agent.md" "bf1f17459cf5" \
  "fixture body_sha: documented-agent"
check_fixture "$FIX/skills/documented-skill/SKILL.md" "7031e8347e4e" \
  "fixture body_sha: documented-skill"
check_fixture "$FIX/agents/stale-docs-agent.md" "ff5722f9923d" \
  "fixture body_sha: stale-docs-agent (stored value is deliberately stale)"

echo "docgen: check-coverage"

# ── 5. the gate: one complete item, one missing a required subsection ───────
GATE="$TMP/gate"
mkdir -p "$GATE/plugins/testpack/agents"
cp "$AGENT" "$GATE/plugins/testpack/agents/complete-agent.md"
cp "$REPO/plugins/testpack/skills/sample-skill/SKILL.md" \
   "$GATE/plugins/testpack/agents/missing-example-agent.md"

gate_out="$(DOCGEN_ROOT="$GATE" DOCS_MAX_PROBLEMS=999 bash "${DOCGEN}/check-coverage.sh" 2>&1)"
has 'gate: flags the item missing the Worked example subsection' "$gate_out" \
    "PROBLEM: plugins/testpack/agents/missing-example-agent.md — missing required subsection"
hasnt "gate: the complete item is not flagged" "$gate_out" \
      "PROBLEM: plugins/testpack/agents/complete-agent.md"
has "gate: summary counts one documented item" "$gate_out" "checked=2 documented=1 problems=1"

# ── 6. DOCS_MAX_PROBLEMS is the ratchet knob ───────────────────────────────
DOCGEN_ROOT="$GATE" DOCS_MAX_PROBLEMS=1 bash "${DOCGEN}/check-coverage.sh" >/dev/null 2>&1
eq "gate: DOCS_MAX_PROBLEMS=1 tolerates exactly one problem" "0" "$?"
DOCGEN_ROOT="$GATE" DOCS_MAX_PROBLEMS=0 bash "${DOCGEN}/check-coverage.sh" >/dev/null 2>&1
eq "gate: the default of 0 rejects that same tree" "1" "$?"

# ── 6b. the §2 rune floor honours the same env override the Go side does ───
# SWARMERY_LINT_MIN_DOCS_SECTION is the knob internal/sysscan/docs.go reads
# through MinDocsSection(). A hard-coded 40 here would put this gate and the
# UI's `missing` list on different floors the moment anyone set it.
strict_out="$(DOCGEN_ROOT="$GATE" DOCS_MAX_PROBLEMS=999 \
  SWARMERY_LINT_MIN_DOCS_SECTION=5000 bash "${DOCGEN}/check-coverage.sh" 2>&1)"
has "gate: a raised floor flags the otherwise-complete item" "$strict_out" \
    "PROBLEM: plugins/testpack/agents/complete-agent.md"
has "gate: the raised floor is the number reported" "$strict_out" "needs 5000"
has "gate: nothing is documented under the raised floor" "$strict_out" \
    "checked=2 documented=0"

# envInt's contract: anything that is not a positive integer warns and falls
# back to 40 — it never crashes and never silently becomes a different floor.
badenv_out="$(DOCGEN_ROOT="$GATE" DOCS_MAX_PROBLEMS=999 \
  SWARMERY_LINT_MIN_DOCS_SECTION=not-a-number bash "${DOCGEN}/check-coverage.sh" 2>&1)"
has "gate: an unparseable override warns" "$badenv_out" "is not a positive integer"
has "gate: an unparseable override falls back to the default floor" "$badenv_out" \
    "checked=2 documented=1 problems=1"

echo "docgen: generate"

# ── the model stub ─────────────────────────────────────────────────────────
# Stands in for `claude -p`. Reads the prompt on stdin (and ignores it), and
# prints a canned block built from DOCGEN_STUB_INVOCATION so the suite can drive
# both the happy path and generate.sh's two rejection paths.
cat > "$TMP/llm-stub.sh" <<'STUB'
#!/bin/bash
# $1 is the style file; the prompt arrives on stdin. Both are drained and ignored.
cat > /dev/null
cat <<BLOCK
# How to use

## What it does
Prices a batch of order line items and hands back the order total, so nobody has to
add the lines up by hand.

## When to use it
- You have line items and need one priced order back.
- A line changed and the total has to be recomputed.

## How to invoke

\`\`\`
${DOCGEN_STUB_INVOCATION} orders/line-items/1042
\`\`\`

Pass the order path; everything else is read from the order document itself.

BLOCK
if [ "${DOCGEN_STUB_OMIT_EXAMPLE:-0}" != "1" ]; then
cat <<BLOCK
## Worked example

\`\`\`
> ${DOCGEN_STUB_INVOCATION} orders/line-items/1042
reads 3 line items and writes the priced order back
PRICED | lines: 3 | total: 148.20
\`\`\`

You end up with the same document, now carrying a total and a per-line breakdown.
BLOCK
fi
STUB

GEN="$TMP/gen"
mkdir -p "$GEN/plugins/testpack/agents"
TARGET="$GEN/plugins/testpack/agents/no-guide-agent.md"
cp "$REPO/plugins/testpack/agents/no-guide-agent.md" "$TARGET"

export DOCGEN_LLM_CMD="bash $TMP/llm-stub.sh"
export DOCGEN_STUB_INVOCATION="@testpack:no-guide-agent"
export DOCGEN_DATE="2026-08-06"

# ── 7. --dry-run prints the block and writes nothing ───────────────────────
before="$(cat "$TARGET")"
dry_out="$(DOCGEN_ROOT="$GEN" bash "${DOCGEN}/generate.sh" --dry-run "$TARGET" 2>&1)"
eq "generate: --dry-run exits 0" "0" "$?"
has "generate: --dry-run prints the block" "$dry_out" "# How to use"
eq "generate: --dry-run leaves the file byte-identical" "$before" "$(cat "$TARGET")"

# ── 8. a real run writes the block and the provenance ──────────────────────
run_out="$(DOCGEN_ROOT="$GEN" bash "${DOCGEN}/generate.sh" "$TARGET" 2>&1)"
eq "generate: the write run exits 0" "0" "$?"
has "generate: reports the write" "$run_out" "WROTE:"
written="$(cat "$TARGET")"
has "generate: block landed in the body" "$written" "# How to use"
has "generate: invocation is verbatim" "$written" "@testpack:no-guide-agent orders/line-items/1042"
has "generate: provenance written as generated" "$written" "status: generated"
has "generate: provenance carries today's date" "$written" "updated: 2026-08-06"
has "generate: the author's own body is untouched" "$written" "- Never writes to the order store."

brief "$TARGET"
eq "generate: docs.source_sha matches the recomputed body_sha" \
   "$(jget "$TMP/brief.json" body_sha)" "$(jget "$TMP/brief.json" fm_docs.source_sha)"

gate_after="$(DOCGEN_ROOT="$GEN" bash "${DOCGEN}/check-coverage.sh" 2>&1)"
eq "generate: the written item now passes the gate" "0" "$?"
has "generate: gate counts it as documented" "$gate_after" "checked=1 documented=1 problems=0"

# ── 9. the idempotency contract — a second run is a skip ───────────────────
snapshot="$(cat "$TARGET")"
again="$(DOCGEN_ROOT="$GEN" bash "${DOCGEN}/generate.sh" "$TARGET" 2>&1)"
has "generate: a second run on an unchanged file skips" "$again" "SKIP:"
eq "generate: the skipped file is byte-identical" "$snapshot" "$(cat "$TARGET")"

forced="$(DOCGEN_ROOT="$GEN" bash "${DOCGEN}/generate.sh" --force "$TARGET" 2>&1)"
has "generate: --force regenerates a current guide" "$forced" "WROTE:"

# ── 10. the in-place splice — a guide that is NOT last in the file ─────────
# §1.2: a block followed by more of the author's document is rewritten WHERE IT
# STANDS, never lifted to the end. Cases 7-9 all put the block at EOF, so the
# `if (existing)` arm of generate.sh's splice — and the frontmatter splice of a
# `docs:` key that already has keys BELOW it — never ran at all. This is the only
# case that can catch the frontmatter being reordered or the author's tail eaten.
SPLICE="$GEN/plugins/testpack/agents/splice-agent.md"
cat > "$SPLICE" <<'FIXTURE'
---
name: splice-agent
docs:
  status: generated
  source_sha: 000000000000
  updated: 2020-01-01
model: opus
description: Fixture whose guide sits in the middle of the body, not at the end.
---

# Role

Fixture agent whose guide is followed by an appendix the author owns.

# How to use

## What it does
A stale one-liner.

# Appendix

- The author's own tail, which must survive the splice.
FIXTURE

eq "splice: the fixture opens with docs: in the middle of the frontmatter" \
   "name,docs,model,description" "$(fmkeys "$SPLICE")"

splice_out="$(DOCGEN_STUB_INVOCATION="@testpack:splice-agent" \
  DOCGEN_ROOT="$GEN" bash "${DOCGEN}/generate.sh" "$SPLICE" 2>&1)"
has "splice: the run writes" "$splice_out" "WROTE:"

spliced="$(cat "$SPLICE")"
eq "splice: frontmatter key ORDER survives (docs: is spliced in place)" \
   "name,docs,model,description" "$(fmkeys "$SPLICE")"
hasnt "splice: the stale fingerprint is gone" "$spliced" "source_sha: 000000000000"
has "splice: provenance carries today's date" "$spliced" "updated: 2026-08-06"
has "splice: the new block landed" "$spliced" "@testpack:splice-agent orders/line-items/1042"
hasnt "splice: the stale one-liner was replaced" "$spliced" "A stale one-liner."
has "splice: the author's appendix heading survives" "$spliced" "# Appendix"
has "splice: the author's tail survives verbatim" "$spliced" \
    "- The author's own tail, which must survive the splice."

# In place means above the appendix; an append would land it below.
splice_block_line="$(grep -n '^# How to use$' "$SPLICE" | head -1 | cut -d: -f1)"
splice_tail_line="$(grep -n '^# Appendix$' "$SPLICE" | head -1 | cut -d: -f1)"
if [ -n "$splice_block_line" ] && [ -n "$splice_tail_line" ] &&
   [ "$splice_block_line" -lt "$splice_tail_line" ]; then
  ok "splice: the block was rewritten in place, above the appendix"
else
  bad "splice: the block was rewritten in place, above the appendix" \
      "block at line ${splice_block_line:-none}, appendix at line ${splice_tail_line:-none}"
fi

# The idempotency contract has to hold for the splice path too, or every CI run
# would rewrite every non-last block forever.
splice_snapshot="$(cat "$SPLICE")"
splice_again="$(DOCGEN_STUB_INVOCATION="@testpack:splice-agent" \
  DOCGEN_ROOT="$GEN" bash "${DOCGEN}/generate.sh" "$SPLICE" 2>&1)"
has "splice: a second run on the spliced file skips" "$splice_again" "SKIP:"
eq "splice: the skipped file is byte-identical" "$splice_snapshot" "$(cat "$SPLICE")"

# ── 11. the two rejection paths ────────────────────────────────────────────
REJECT="$GEN/plugins/testpack/agents/reject-agent.md"
cp "$REPO/plugins/testpack/agents/no-guide-agent.md" "$REJECT"
reject_before="$(cat "$REJECT")"

DOCGEN_STUB_INVOCATION="@wrong:invocation" \
  DOCGEN_ROOT="$GEN" bash "${DOCGEN}/generate.sh" "$REJECT" >/dev/null 2>&1
eq "generate: rejects a block that reworded the invocation" "1" "$?"
eq "generate: the rejected file is untouched" "$reject_before" "$(cat "$REJECT")"

DOCGEN_STUB_OMIT_EXAMPLE=1 \
  DOCGEN_ROOT="$GEN" bash "${DOCGEN}/generate.sh" "$REJECT" >/dev/null 2>&1
eq "generate: rejects a block missing a required subsection" "1" "$?"
eq "generate: that file is untouched too" "$reject_before" "$(cat "$REJECT")"

printf 'docgen: %d passed, %d failed, %d skipped\n' "$pass" "$fail" "$skipped"
[ "$fail" -eq 0 ]
