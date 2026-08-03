#!/usr/bin/env bash
# Static contract test for plugins/jira-pack/ (Phase 8 -- verification & release).
#
# This test NEVER runs a model. It asserts that the pack's shipped docs
# actually describe the contract the plan agreed to hold -- provider-agnostic
# tool resolution, the todo-column ban, the six dry-run markers, comment-
# before-transition ordering, the needs-info != cannot-reproduce rule, a
# well-formed agent frontmatter, no in-repo plan location, and no real
# Atlassian host. Cheap, deterministic, no ANTHROPIC_API_KEY required.
#
# Style matches scripts/tests/protect-sensitive-files.test.sh (pass/fail
# counters, one line per failure) and scripts/tests/eval-gate.test.sh
# (set -euo pipefail). No dependencies beyond grep/find.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
PACK="$ROOT/plugins/jira-pack"

pass=0
fail=0

ok()  { pass=$((pass + 1)); printf '  ok   - %s\n' "$1"; }
bad() { fail=$((fail + 1)); printf '  FAIL - %s\n' "$1"; }

# ── 1. No .mcp.json bundled -- provider-agnostic, no bundled MCP server ─────
if find "$PACK" -iname '.mcp.json' 2>/dev/null | grep -q .; then
  bad "check1: a .mcp.json file exists under plugins/jira-pack/"
else
  ok "check1: no .mcp.json under plugins/jira-pack/"
fi

# ── 2. Both MCP tool-prefix examples always appear together, per file ──────
prefix_a='mcp__plugin_atlassian_atlassian__'
prefix_b='mcp__claude_ai_Atlassian_Rovo__'
files_with_either="$(grep -rlE "${prefix_a}|${prefix_b}" "$PACK" 2>/dev/null || true)"
check2_bad=0
if [ -z "$files_with_either" ]; then
  bad "check2: neither MCP prefix is mentioned anywhere in the pack (expected at least one illustrative mention)"
  check2_bad=1
else
  while IFS= read -r f; do
    [ -z "$f" ] && continue
    has_a="$(grep -c "$prefix_a" "$f" || true)"
    has_b="$(grep -c "$prefix_b" "$f" || true)"
    if [ "$has_a" -eq 0 ] || [ "$has_b" -eq 0 ]; then
      bad "check2: ${f#"$ROOT"/} mentions only one MCP prefix, not both"
      check2_bad=1
    fi
  done <<< "$files_with_either"
fi
[ "$check2_bad" -eq 0 ] && ok "check2: every file mentioning an MCP prefix mentions both, together (illustrative only)"

# ── 3. swarmery-board-card never sets boardColumn to todo in a real body ───
board_card="$PACK/skills/swarmery-board-card/SKILL.md"
check3_bad=0
if grep -qF '"boardColumn": "todo"' "$board_card"; then
  bad "check3: ${board_card#"$ROOT"/} sets boardColumn to \"todo\" in a real request body"
  check3_bad=1
fi
if ! grep -qF 'never sets `boardColumn: "todo"`' "$board_card"; then
  bad "check3: ${board_card#"$ROOT"/} is missing the explicit todo-prohibition sentence"
  check3_bad=1
fi
[ "$check3_bad" -eq 0 ] && ok "check3: todo is never a real boardColumn value; the prohibition is explicit"

# ── 4. All six dry-run markers are documented somewhere in the pack ────────
markers=(
  "DRY-RUN board POST"
  "DRY-RUN board PATCH"
  "DRY-RUN jira comment"
  "DRY-RUN jira transition"
  "DRY-RUN git"
  "DRY-RUN gh pr create"
)
check4_bad=0
for m in "${markers[@]}"; do
  if ! grep -rqF "$m" "$PACK"; then
    bad "check4: marker '$m' not found anywhere in the pack"
    check4_bad=1
  fi
done
[ "$check4_bad" -eq 0 ] && ok "check4: all six dry-run markers are documented"

# ── 5. jira-writeback fixes the comment-then-transition order ──────────────
writeback="$PACK/skills/jira-writeback/SKILL.md"
if grep -qF '# Step 4 — post, in order: comment, then transition' "$writeback"; then
  ok "check5: jira-writeback documents the comment-before-transition ordering"
else
  bad "check5: jira-writeback is missing the comment-before-transition ordering heading"
fi

# ── 6. jira-triage documents needs-info != cannot-reproduce, explicitly ────
triage="$PACK/skills/jira-triage/SKILL.md"
check6_bad=0
grep -qF '## The rule that carries this skill' "$triage" \
  || { bad "check6: jira-triage is missing the 'rule that carries this skill' section"; check6_bad=1; }
grep -qF 'is not the same verdict as' "$triage" \
  || { bad "check6: jira-triage is missing the needs-info != cannot-reproduce framing"; check6_bad=1; }
grep -qF 'Could not run it at all → `needs-info`' "$triage" \
  || { bad "check6: jira-triage is missing the needs-info classification rule"; check6_bad=1; }
[ "$check6_bad" -eq 0 ] && ok "check6: jira-triage explicitly distinguishes needs-info from cannot-reproduce"

# ── 7. Agent frontmatter: --- first line, name:/description: in first 15 ───
agent="$PACK/agents/jira-task-runner.md"
check7_bad=0
first_line="$(head -n1 "$agent")"
[ "$first_line" = "---" ] || { bad "check7: ${agent#"$ROOT"/} does not start with a --- line"; check7_bad=1; }
head -n15 "$agent" | grep -q '^name:' || { bad "check7: ${agent#"$ROOT"/} missing name: in first 15 lines"; check7_bad=1; }
head -n15 "$agent" | grep -q '^description:' || { bad "check7: ${agent#"$ROOT"/} missing description: in first 15 lines"; check7_bad=1; }
[ "$check7_bad" -eq 0 ] && ok "check7: jira-task-runner.md frontmatter is well-formed"

# ── 8. No instruction anywhere in the pack to write a plan into this repo ──
check8_bad=0
if grep -rq 'docs/plan' "$PACK" 2>/dev/null; then
  bad "check8: the pack references docs/plan (an in-repo plan location) somewhere"
  check8_bad=1
fi
grep -rqF '<workspace>/<project>/workspace/working/{YYYY}/{MM}/{DD}/{slug}/plan/' "$PACK" \
  || { bad "check8: the pack is missing the private-workspace plan path escalation should point at"; check8_bad=1; }
[ "$check8_bad" -eq 0 ] && ok "check8: no in-repo plan location is ever proposed"

# ── 9. No real Atlassian host -- placeholders only ──────────────────────────
# Note: this check covers the syntactically-detectable half of Design check 9
# (a concrete *.atlassian.net hostname). Detecting a "real" Jira project key
# via static grep has no reliable oracle -- every project-key example in the
# pack today already uses the neutral <PROJECT-KEY> placeholder or a generic
# 3-letter example (ABC-142); scripts/scan-flavor.sh (run separately, already
# gated in CI) is this repo's actual backstop for real brand/identity tokens.
check9_bad=0
if grep -rEq '[A-Za-z0-9-]+\.atlassian\.net' "$PACK" 2>/dev/null; then
  bad "check9: a concrete *.atlassian.net host is referenced (placeholders only expected)"
  check9_bad=1
fi
[ "$check9_bad" -eq 0 ] && ok "check9: no concrete Atlassian host found (placeholders only)"

printf 'jira-pack-dry-run: %d passed, %d failed\n' "$pass" "$fail"
[ "$fail" -eq 0 ]
