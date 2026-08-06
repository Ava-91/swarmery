#!/bin/bash
# Behavioral tests for the statusline account chip:
#   plugins/core/statusline/statusline.sh — account_key_from_config_dir() +
#   transcript_config_dir(), and the 🪪<key> BADGES entry they feed.
#
# Framework-free (portable, no bats dependency), fully offline. Every render
# gets its OWN brand-new empty TMPDIR, so the weather cache can never have been
# warmed by an earlier call in this same run and every invocation deterministically
# takes the cold "warming up" fallback — see phase-6 design Д7: two runs of the
# same script can otherwise differ on their own (clock, weather, counters),
# which would make a diff-based regression proof flaky for reasons unrelated to
# this phase. The default-account case also uses a cwd OUTSIDE this git
# repository, so the git tail of the PWD line vanishes deterministically too.
# Run locally with `bash scripts/tests/statusline-account-chip.test.sh`.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
STATUSLINE="$ROOT/plugins/core/statusline/statusline.sh"

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

OUTSIDE_REPO="$WORK/outside-repo"
mkdir -p "$OUTSIDE_REPO"

pass=0
fail=0

ok()  { pass=$((pass + 1)); }
bad() { fail=$((fail + 1)); printf '  ✗ %s\n     expected: %s\n     actual:   %s\n' "$1" "$2" "$3"; }

# render <script> <transcriptPathOrEmpty> <configDirOrEmpty> <oauthFlagOrEmpty>
# -> prints the rendered statusline on stdout. CLAUDE_CONFIG_DIR and
# SWARMERY_USAGE_OAUTH are explicitly unset first so the test is independent of
# whatever the ambient shell happens to export. Every call gets a fresh, empty
# TMPDIR (see header comment) that is discarded right after.
render() {
  local script="$1" transcript="$2" cfgdir="$3" oauth="$4" tmp stdin_json out
  tmp="$(mktemp -d)"
  stdin_json="$(printf '{"model":{"display_name":"Claude"},"workspace":{"current_dir":"%s","project_dir":"%s"},"transcript_path":"%s"}' \
    "$OUTSIDE_REPO" "$OUTSIDE_REPO" "$transcript")"
  out="$(
    unset CLAUDE_CONFIG_DIR SWARMERY_USAGE_OAUTH SWARMERY_STATUSLINE_FABLE
    [ -n "$cfgdir" ] && export CLAUDE_CONFIG_DIR="$cfgdir"
    [ -n "$oauth" ] && export SWARMERY_USAGE_OAUTH="$oauth"
    printf '%s' "$stdin_json" | TMPDIR="$tmp" bash "$script" 2>/dev/null
  )"
  rm -rf "$tmp"
  printf '%s' "$out"
}

# ── (a) default account: no chip, PLUS the two-step "unchanged" proof ────
OUT_A="$(render "$STATUSLINE" "" "" "")"
if printf '%s' "$OUT_A" | grep -qF '🪪'; then
  bad "(a) default account -> no 🪪 chip in render" "no 🪪 badge" "$OUT_A"
else
  ok
fi

OLD_STATUSLINE="$WORK/statusline-old.sh"
git -C "$ROOT" show HEAD:plugins/core/statusline/statusline.sh > "$OLD_STATUSLINE" 2>/dev/null

# STEP 1 — fixture stability: the OLD script against itself, twice. Must be
# empty, or the fixture is not pinned and STEP 2 below would prove nothing
# (see Д7 rationale: the clock/weather/counters can differ between runs on
# their own, with no code change involved at all).
STEP1_A="$(render "$OLD_STATUSLINE" "" "" "")"
STEP1_B="$(render "$OLD_STATUSLINE" "" "" "")"
DIFF1="$(diff <(printf '%s\n' "$STEP1_A") <(printf '%s\n' "$STEP1_B") 2>&1 || true)"
if [ -z "$DIFF1" ]; then
  ok
else
  bad "(a) STEP 1 -- fixture is pinned (old vs itself)" "empty diff" "$DIFF1"
fi

# STEP 2 — only meaningful once STEP 1 is clean: old vs the new (edited) script.
STEP2_NEW="$(render "$STATUSLINE" "" "" "")"
DIFF2="$(diff <(printf '%s\n' "$STEP1_A") <(printf '%s\n' "$STEP2_NEW") 2>&1 || true)"
if [ -z "$DIFF2" ]; then
  ok
else
  bad "(a) STEP 2 -- default account renders byte-for-byte unchanged" "empty diff" "$DIFF2"
fi

# ── (b) $TRANSCRIPT under a named account's config dir -> chip shows that key ──
CFG_WORK="$WORK/.claude-work"
TRANSCRIPT_B="$CFG_WORK/projects/-some-slug/deadbeef-session.jsonl"
OUT_B="$(render "$STATUSLINE" "$TRANSCRIPT_B" "" "")"
if printf '%s' "$OUT_B" | grep -qF '🪪work'; then
  ok
else
  bad "(b) transcript under .claude-work -> chip shows 'work'" "🪪work present" "$OUT_B"
fi

# ── (c) no transcript, CLAUDE_CONFIG_DIR fallback -> chip shows that key ─
OUT_C="$(render "$STATUSLINE" "" "$CFG_WORK" "")"
if printf '%s' "$OUT_C" | grep -qF '🪪work'; then
  ok
else
  bad "(c) no transcript, CLAUDE_CONFIG_DIR fallback -> chip shows 'work'" "🪪work present" "$OUT_C"
fi

# ── (d) SWARMERY_USAGE_OAUTH=0 in case (b) -> chip still present (Д5) ─────
OUT_D="$(render "$STATUSLINE" "$TRANSCRIPT_B" "" "0")"
if printf '%s' "$OUT_D" | grep -qF '🪪work'; then
  ok
else
  bad "(d) SWARMERY_USAGE_OAUTH=0 -> chip still present" "🪪work present" "$OUT_D"
fi

printf 'statusline-account-chip: %d passed, %d failed\n' "$pass" "$fail"
[ "$fail" -eq 0 ]
