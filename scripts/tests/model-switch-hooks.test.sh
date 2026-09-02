#!/usr/bin/env bash
# model-switch-hooks.test.sh — the one hook in core allowed to block.
#
# The decision table is the spec: exit 2 ONLY on a definite negative (fail,
# inconclusive, or no recorded verdict). Everything ambiguous — daemon down,
# malformed payload, a no-op switch — allows, because a gate that fires when
# its own infrastructure is down is a gate people delete.
#
# The daemon is stubbed with a curl shim on PATH: these tests must never need
# a running daemon, or they will be the kind of test that quietly stops running.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
PRE="$ROOT/plugins/core/hooks/pre-model-switch.sh"
POST="$ROOT/plugins/core/hooks/post-model-switch.sh"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

pass=0; fail=0
ok()  { echo "ok   $1"; pass=$((pass + 1)); }
bad() { echo "FAIL $1: $2"; fail=$((fail + 1)); }

SHIM="$TMP/bin"; mkdir -p "$SHIM"

# stub_curl <exit-code> [body]
# The body goes through a FILE, not through the generated script: it is JSON
# full of double quotes, and interpolating it into a heredoc produced a stub
# whose test line was a syntax error while `exit 0` still ran — so curl
# "succeeded" with an empty body and every case looked like a missing verdict.
stub_curl() {
  printf '%s' "${2:-}" > "$SHIM/body"
  cat > "$SHIM/curl" <<'EOS'
#!/bin/sh
dir=$(dirname "$0")
[ -s "$dir/body" ] && cat "$dir/body"
exit "$(cat "$dir/rc")"
EOS
  printf '%s' "$1" > "$SHIM/rc"
  chmod +x "$SHIM/curl"
}

run_pre() { # run_pre <json> -> exit code
  printf '%s' "$1" | PATH="$SHIM:$PATH" bash "$PRE" >/dev/null 2>&1 || return $?
  return 0
}
pre_stderr() { printf '%s' "$1" | PATH="$SHIM:$PATH" bash "$PRE" 2>&1 >/dev/null || true; }

SWITCH='{"from_model":"claude-opus-5","to_model":"claude-opus-6","session_id":"s1"}'

# ── 1. pass → allow ───────────────────────────────────────────────────
stub_curl 0 '{"verdict":"pass","detail":"mean 3.4"}'
rc=0; run_pre "$SWITCH" || rc=$?
[ "$rc" -eq 0 ] && ok "verdict pass allows the switch" || bad "pass" "exit=$rc"

# ── 2. fail → BLOCK ───────────────────────────────────────────────────
stub_curl 0 '{"verdict":"fail","detail":"1.68 below baseline"}'
rc=0; run_pre "$SWITCH" || rc=$?
[ "$rc" -eq 2 ] && ok "verdict fail blocks with exit 2" || bad "fail" "exit=$rc, want 2"

# ── 3. inconclusive → BLOCK ───────────────────────────────────────────
stub_curl 0 '{"verdict":"inconclusive","detail":"only 0 judged trajectories"}'
rc=0; run_pre "$SWITCH" || rc=$?
[ "$rc" -eq 2 ] && ok "verdict inconclusive blocks" || bad "inconclusive" "exit=$rc, want 2"

# ── 4. 404 (never evaluated) → BLOCK. Unknown is not the same as fine ──
stub_curl 22
rc=0; run_pre "$SWITCH" || rc=$?
[ "$rc" -eq 2 ] && ok "no recorded validation blocks" || bad "404" "exit=$rc, want 2"

# ── 5. daemon unreachable → ALLOW. Infra failure must not brick the user ──
stub_curl 7
rc=0; run_pre "$SWITCH" || rc=$?
[ "$rc" -eq 0 ] && ok "daemon unreachable allows with a warning" || bad "daemon down" "exit=$rc, want 0"
pre_stderr "$SWITCH" | grep -q 'unreachable' \
  && ok "unreachable path says so on stderr" || bad "unreachable msg" "no warning"

# ── 6. override → ALLOW, and say it was used ──────────────────────────
stub_curl 0 '{"verdict":"fail","detail":"bad"}'
rc=0
printf '%s' "$SWITCH" | PATH="$SHIM:$PATH" SWARMERY_ALLOW_UNVALIDATED_MODEL=1 \
  bash "$PRE" >/dev/null 2>"$TMP/ov.txt" || rc=$?
if [ "$rc" -eq 0 ] && grep -q 'overridden' "$TMP/ov.txt"; then
  ok "override allows a failing model and logs that it was used"
else
  bad "override" "exit=$rc, stderr=$(cat "$TMP/ov.txt")"
fi

# ── 7. the block message must be actionable, not just a refusal ───────
stub_curl 22
msg=$(pre_stderr "$SWITCH")
if grep -q 'swarmery modeleval --model claude-opus-6' <<<"$msg" \
   && grep -q 'SWARMERY_ALLOW_UNVALIDATED_MODEL=1' <<<"$msg"; then
  ok "block names both the fix and the override"
else
  bad "block message" "missing the command or the override: $msg"
fi

# ── 8. malformed stdin and a no-op switch both allow ──────────────────
rc=0; printf 'not json' | PATH="$SHIM:$PATH" bash "$PRE" >/dev/null 2>&1 || rc=$?
[ "$rc" -eq 0 ] && ok "malformed stdin allows" || bad "malformed" "exit=$rc"

stub_curl 22
rc=0; run_pre '{"from_model":"claude-opus-5","to_model":"claude-opus-5"}' || rc=$?
[ "$rc" -eq 0 ] && ok "same-model resume is not a switch" || bad "no-op switch" "exit=$rc, want 0"

# ── 9. post hook: always exit 0, dedupe a no-op, record a real switch ──
rc=0; printf '%s' "$SWITCH" | bash "$POST" >/dev/null 2>&1 || rc=$?
[ "$rc" -eq 0 ] && ok "post hook exits 0" || bad "post exit" "exit=$rc"

LOG="/tmp/claude-session-$(date +%Y%m%d).jsonl"
before=$(grep -c 'ModelSwitch' "$LOG" 2>/dev/null || echo 0)
printf '%s' '{"from_model":"m","to_model":"m","session_id":"s"}' | bash "$POST" >/dev/null 2>&1 || true
after=$(grep -c 'ModelSwitch' "$LOG" 2>/dev/null || echo 0)
[ "$before" = "$after" ] && ok "post hook ignores a same-model resume" \
  || bad "post dedupe" "wrote a row for a no-op switch"

echo "passed=$pass failed=$fail"
[ "$fail" -eq 0 ]
