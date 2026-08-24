#!/bin/bash
# Behavioral tests for plugins/core/hooks/bash-shape-guard.sh.
#
# Framework-free, same style as protect-sensitive-files.test.sh: feed a hook
# JSON payload on stdin and assert the outcome.
#
# These assert the DECISION, not the exit code. The guard ships in warn mode
# (exit 0 + a WARN line) and flips to exit 2 later; a suite pinned to the exit
# code would go red on the flip and get "fixed" by loosening it. A refusal is
# therefore recognised by its rule tag on stderr, which is identical in both
# modes — so this file needs no edit when the gate hardens.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
HOOK="$ROOT/plugins/core/hooks/bash-shape-guard.sh"

pass=0
fail=0

# decision <json-payload> — echoes "BLOCK <rule>" or "ALLOW".
decision() {
  local payload="$1" err rc
  err=$(printf '%s' "$payload" | bash "$HOOK" 2>&1 >/dev/null)
  rc=$?
  if printf '%s' "$err" | grep -Eq '^(⚠️  WARN \(enforce from [0-9-]+\)|🚫 BLOCKED): \[[a-z-]+\]'; then
    printf 'BLOCK %s' "$(printf '%s' "$err" | sed -nE 's/^.*\[([a-z-]+)\].*$/\1/p' | head -1)"
    return 0
  fi
  # A non-zero exit with no recognisable refusal line is a crash, not a verdict.
  if [ "$rc" -ne 0 ]; then
    printf 'ERROR(rc=%s)' "$rc"
    return 0
  fi
  printf 'ALLOW'
}

# jc <command> — minimal Bash hook payload. jq builds it so quoting in the
# command under test cannot corrupt the JSON.
jc() { jq -nc --arg c "$1" '{tool_input:{command:$c}}'; }

# expect <expected-decision> <description> <command>
expect() {
  local expected="$1" desc="$2" cmd="$3" actual
  actual=$(decision "$(jc "$cmd")")
  if [ "$actual" = "$expected" ]; then
    pass=$((pass + 1))
  else
    fail=$((fail + 1))
    printf '  ✗ %s\n      command:  %s\n      expected: %s\n      got:      %s\n' \
      "$desc" "$cmd" "$expected" "$actual"
  fi
}

# stderr_contains <needle> <description> <command> — case-insensitive, so a
# sentence-cased message still satisfies a lowercase contract phrase.
stderr_contains() {
  local needle="$1" desc="$2" cmd="$3" err
  err=$(printf '%s' "$(jc "$cmd")" | bash "$HOOK" 2>&1 >/dev/null)
  if printf '%s' "$err" | grep -qiF "$needle"; then
    pass=$((pass + 1))
  else
    fail=$((fail + 1))
    printf '  ✗ %s (stderr does not mention %s)\n' "$desc" "$needle"
  fi
}

# ── rule: heredoc (SC-1) ──────────────────────────────────────────
expect "BLOCK heredoc" "unquoted heredoc"        "cat <<EOF > f.txt"
expect "BLOCK heredoc" "quoted heredoc"          "cat <<'EOF' > f.txt"
expect "BLOCK heredoc" "dash heredoc"            $'cat <<-EOF > f.txt\n  body\nEOF'
expect "BLOCK heredoc" "custom delimiter"        "cat <<SHEOF > f.txt"
stderr_contains "Write tool" "heredoc names the alternative" "cat <<EOF > f.txt"
# A herestring is not a heredoc.
expect "ALLOW" "herestring is not a heredoc"     "grep foo <<< \"\$var\""

# ── rule: multi-mutation (SC-2) ───────────────────────────────────
expect "BLOCK multi-mutation" "git add && git commit"  "git add -A && git commit -m x"
expect "BLOCK multi-mutation" "mkdir && touch && chmod" "mkdir -p a && touch a/b && chmod +x a/b"
expect "BLOCK multi-mutation" "rm ; mv"                "rm -f old.txt ; mv new.txt old.txt"
expect "BLOCK multi-mutation" "two redirections"       "echo a > x.txt && echo b > y.txt"
expect "BLOCK multi-mutation" "install then commit"    "npm install && git commit -am deps"
stderr_contains "one operation per call" "names the rule" "git add -A && git commit -m x"
stderr_contains "git add -A"             "names the offending segment" "git add -A && git commit -m x"

# ── canonical commands must NOT fire (SC-3) ───────────────────────
# Every one of these is documented in this repo's own CLAUDE.md. A guard that
# breaks them gets switched off rather than used.
expect "ALLOW" "cd + make test"        "cd tools/swarmery && make test"
expect "ALLOW" "find | xargs shellcheck" "find plugins scripts -name '*.sh' -print0 | xargs -0 shellcheck -S error"
expect "ALLOW" "git log | head"        "git log --oneline | head -20"
expect "ALLOW" "grep | head"           "grep -rn foo . | head"
expect "ALLOW" "npm run build"         "npm run build"
expect "ALLOW" "cd + npm run build"    "cd web && npm run build"
expect "ALLOW" "vet then test"         "go vet ./... && go test ./..."
expect "ALLOW" "single mutation"       "git commit -m 'one thing'"
expect "ALLOW" "cd + single mutation"  "cd tools/swarmery && make install"
expect "ALLOW" "read chain, three parts" "cat a.txt && wc -l b.txt && ls -la"
expect "ALLOW" "redirect to /dev/null" "make test >/dev/null && echo done"

# ── rule: sleep-before-read (SC-4) ────────────────────────────────
expect "BLOCK sleep-before-read" "sleep && tail"  "sleep 5 && tail -n 50 /tmp/run.log"
expect "BLOCK sleep-before-read" "sleep ; cat"    "sleep 60 ; cat /tmp/out.log"
expect "BLOCK sleep-before-read" "sleep ; grep"   "sleep 2 ; grep ERROR /tmp/run.log"
stderr_contains "separate call" "sleep names the fix" "sleep 5 && tail -n 50 /tmp/run.log"
# A sleep on its own is fine, and so is a read that precedes one.
expect "ALLOW" "bare sleep"            "sleep 30"
expect "ALLOW" "read then sleep"       "cat /tmp/run.log && sleep 5"

# ── payload edge cases ────────────────────────────────────────────
expect "ALLOW" "empty command"         ""
if printf '%s' '{"tool_input":{}}' | bash "$HOOK" >/dev/null 2>&1; then
  pass=$((pass + 1))
else
  fail=$((fail + 1))
  printf '  ✗ payload with no command must exit 0\n'
fi
if printf '%s' 'not json at all' | bash "$HOOK" >/dev/null 2>&1; then
  pass=$((pass + 1))
else
  fail=$((fail + 1))
  printf '  ✗ non-JSON payload must exit 0 rather than crash\n'
fi

# ── the gate itself ───────────────────────────────────────────────
# The burn-in comment must name a real flip date and the gate must stay a
# single variable, so hardening is a one-line change and not a rewrite.
if grep -Eq '^BLOCK_EXIT=[02] *(#.*)?$' "$HOOK" && grep -Eq '^ENFORCE_FROM="[0-9]{4}-[0-9]{2}-[0-9]{2}"' "$HOOK"; then
  pass=$((pass + 1))
else
  fail=$((fail + 1))
  printf '  ✗ the gate is no longer a single BLOCK_EXIT variable with a dated ENFORCE_FROM\n'
fi

printf 'bash-shape-guard: %d passed, %d failed\n' "$pass" "$fail"
[ "$fail" -eq 0 ]
