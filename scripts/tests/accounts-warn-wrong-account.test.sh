#!/bin/bash
# Behavioral tests for plugins/accounts-pack/hooks/warn-wrong-account.sh — the
# SessionStart hook that warns when a session runs under an account other than
# the one the project is bound to.
#
# Framework-free (portable, no bats dependency), fully offline: each case
# builds a temp "project" dir with (or without) .claude/settings.local.json and
# feeds the hook a SessionStart payload shaped exactly like
# internal/hookshim/shim_test.go's sessionStartStdin fixture (session_id, cwd,
# hook_event_name — NO transcript_path; the hook must not depend on one).
# Run locally with `bash scripts/tests/accounts-warn-wrong-account.test.sh`.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
HOOK="$ROOT/plugins/accounts-pack/hooks/warn-wrong-account.sh"
BASH_BIN="$(command -v bash)"

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

pass=0
fail=0

ok()  { pass=$((pass + 1)); }
bad() { fail=$((fail + 1)); printf '  ✗ %s\n     expected: %s\n     actual:   %s\n' "$1" "$2" "$3"; }

# stdin_for <projectDir> -> the hook's SessionStart payload (shape verified
# against internal/hookshim/shim_test.go:203).
stdin_for() {
  printf '{"session_id":"sid-1","cwd":"%s","hook_event_name":"SessionStart"}' "$1"
}

CFG_WORK="$WORK/.claude-work"

# ── (a) no binding at all -> silent ──────────────────────────────────────
proj_a="$WORK/proj-a"; mkdir -p "$proj_a"
OUT="$(stdin_for "$proj_a" | CLAUDE_CONFIG_DIR="$CFG_WORK" bash "$HOOK" 2>/dev/null)"; HOOK_EXIT=$?
if [ -z "$OUT" ] && [ "$HOOK_EXIT" -eq 0 ]; then ok
else bad "(a) no binding -> silent, exit 0" "'' exit 0" "'$OUT' exit $HOOK_EXIT"; fi

# ── (b) binding matches the actual account -> silent ─────────────────────
proj_b="$WORK/proj-b"; mkdir -p "$proj_b/.claude"
printf '{"swarmery":{"claudeAccount":"work"}}' > "$proj_b/.claude/settings.local.json"
OUT="$(stdin_for "$proj_b" | CLAUDE_CONFIG_DIR="$CFG_WORK" bash "$HOOK" 2>/dev/null)"; HOOK_EXIT=$?
if [ -z "$OUT" ] && [ "$HOOK_EXIT" -eq 0 ]; then ok
else bad "(b) binding matches actual account -> silent" "'' exit 0" "'$OUT' exit $HOOK_EXIT"; fi

# ── (c) mismatch -> exactly one line of valid SessionStart hook JSON ─────
proj_c="$WORK/proj-c"; mkdir -p "$proj_c/.claude"
printf '{"swarmery":{"claudeAccount":"science"}}' > "$proj_c/.claude/settings.local.json"
OUT="$(stdin_for "$proj_c" | CLAUDE_CONFIG_DIR="$CFG_WORK" bash "$HOOK" 2>/dev/null)"; HOOK_EXIT=$?
lines="$(printf '%s' "$OUT" | grep -c '^' || true)"
if [ "$HOOK_EXIT" -eq 0 ] && [ "$lines" -eq 1 ] && printf '%s' "$OUT" | jq empty >/dev/null 2>&1; then
  event="$(printf '%s' "$OUT" | jq -r '.hookSpecificOutput.hookEventName // empty')"
  ctx="$(printf '%s' "$OUT" | jq -r '.hookSpecificOutput.additionalContext // empty')"
  if [ "$event" = "SessionStart" ] && printf '%s' "$ctx" | grep -q "work" && printf '%s' "$ctx" | grep -q "science"; then
    ok
  else
    bad "(c) mismatch -> hookEventName=SessionStart, additionalContext names both keys" \
      "SessionStart; context mentions work AND science" "event='$event' ctx='$ctx'"
  fi
else
  bad "(c) mismatch -> exactly 1 line of valid JSON, exit 0" "1 line, valid JSON, exit 0" "$lines line(s) '$OUT' exit $HOOK_EXIT"
fi

# ── (d) settings.local.json is not JSON at all -> silent ─────────────────
proj_d="$WORK/proj-d"; mkdir -p "$proj_d/.claude"
printf 'not valid json at all' > "$proj_d/.claude/settings.local.json"
OUT="$(stdin_for "$proj_d" | CLAUDE_CONFIG_DIR="$CFG_WORK" bash "$HOOK" 2>/dev/null)"; HOOK_EXIT=$?
if [ -z "$OUT" ] && [ "$HOOK_EXIT" -eq 0 ]; then ok
else bad "(d) unparseable settings file -> silent, exit 0" "'' exit 0" "'$OUT' exit $HOOK_EXIT"; fi

# ── (e) jq missing from PATH -> silent (same degradation path as (a)/(d)) ─
proj_e="$WORK/proj-e"; mkdir -p "$proj_e/.claude"
printf '{"swarmery":{"claudeAccount":"science"}}' > "$proj_e/.claude/settings.local.json"
NO_JQ_DIR="$WORK/no-jq-path"; mkdir -p "$NO_JQ_DIR"
# PATH points ONLY at an empty dir, so `jq` cannot resolve inside the hook's own
# process. The hook binary itself is invoked by absolute path (resolved above,
# with the test harness's own PATH) so the PATH override never breaks finding
# bash itself — only what the HOOK can find on its own PATH.
OUT="$(stdin_for "$proj_e" | CLAUDE_PROJECT_DIR="$proj_e" PATH="$NO_JQ_DIR" "$BASH_BIN" "$HOOK" 2>/dev/null)"; HOOK_EXIT=$?
if [ -z "$OUT" ] && [ "$HOOK_EXIT" -eq 0 ]; then ok
else bad "(e) jq absent from PATH -> silent, exit 0" "'' exit 0" "'$OUT' exit $HOOK_EXIT"; fi

# ── (f) SWARMERY_USAGE_OAUTH=0 changes nothing (Д5 — this phase reads no credential) ──
proj_f="$WORK/proj-f"; mkdir -p "$proj_f/.claude"
printf '{"swarmery":{"claudeAccount":"science"}}' > "$proj_f/.claude/settings.local.json"
OUT="$(stdin_for "$proj_f" | SWARMERY_USAGE_OAUTH=0 CLAUDE_CONFIG_DIR="$CFG_WORK" bash "$HOOK" 2>/dev/null)"; HOOK_EXIT=$?
lines_f="$(printf '%s' "$OUT" | grep -c '^' || true)"
if [ "$HOOK_EXIT" -eq 0 ] && [ "$lines_f" -eq 1 ] && printf '%s' "$OUT" | jq empty >/dev/null 2>&1; then ok
else bad "(f) SWARMERY_USAGE_OAUTH=0 -> same mismatch behavior as without it" \
  "1 line, valid JSON, exit 0" "$lines_f line(s) '$OUT' exit $HOOK_EXIT"; fi

printf 'accounts-warn-wrong-account: %d passed, %d failed\n' "$pass" "$fail"
[ "$fail" -eq 0 ]
