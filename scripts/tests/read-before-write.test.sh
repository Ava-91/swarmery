#!/bin/bash
# Behavioral tests for plugins/core/hooks/read-before-write.sh.
#
# Framework-free, same style as protect-sensitive-files.test.sh — but this hook
# is STATEFUL (it remembers which files a session has been shown), so every case
# runs against real temp files under a scratch TMPDIR that is torn down at exit.
# A suite that shared the developer's real TMPDIR would pass once and then fail
# on the second run, which is worse than failing outright.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
HOOK="$ROOT/plugins/core/hooks/read-before-write.sh"
PROTECT="$ROOT/plugins/core/hooks/protect-sensitive-files.sh"

SCRATCH="$(mktemp -d)"
trap 'rm -rf "$SCRATCH"' EXIT
export TMPDIR="$SCRATCH/tmp"
mkdir -p "$TMPDIR"
WORK="$SCRATCH/work"
mkdir -p "$WORK"

pass=0
fail=0

ok()  { pass=$((pass + 1)); }
bad() { fail=$((fail + 1)); printf '  ✗ %s\n' "$1"; }

# payload <session-id> <file-path>
payload() { jq -nc --arg s "$1" --arg f "$2" '{session_id:$s, tool_input:{file_path:$f}}'; }

# run_hook <session-id> <file-path> — sets RC and ERR.
run_hook() {
  ERR=$(payload "$1" "$2" | bash "$HOOK" 2>&1 >/dev/null)
  RC=$?
}

# ── first attempt blocks and hands back the file (SC-5) ───────────
echo "line-one-marker" > "$WORK/app.ts"
echo "line-two-marker" >> "$WORK/app.ts"
run_hook "sess-A" "$WORK/app.ts"
[ "$RC" -eq 2 ] && ok || bad "first Edit against an unread file must exit 2 (got $RC)"
printf '%s' "$ERR" | grep -q "line-one-marker" && ok || bad "stderr must carry the file's actual content"
printf '%s' "$ERR" | grep -q "line-two-marker" && ok || bad "stderr must carry the whole small file"
printf '%s' "$ERR" | grep -qi "retry is allowed" && ok || bad "stderr must tell the agent the retry will pass"

# ── the immediate retry passes ────────────────────────────────────
run_hook "sess-A" "$WORK/app.ts"
[ "$RC" -eq 0 ] && ok || bad "the immediate retry must exit 0 (got $RC)"

# ── state is per session ──────────────────────────────────────────
run_hook "sess-B" "$WORK/app.ts"
[ "$RC" -eq 2 ] && ok || bad "a different session must block again (got $RC)"

# ── creating a new file is never blocked ──────────────────────────
run_hook "sess-C" "$WORK/does-not-exist.ts"
[ "$RC" -eq 0 ] && ok || bad "Write to a non-existent path must exit 0 (got $RC)"

# ── the cap, with an explicit truncation notice ───────────────────
seq 1 900 > "$WORK/big.log"
run_hook "sess-D" "$WORK/big.log"
[ "$RC" -eq 2 ] && ok || bad "a large unread file must still block (got $RC)"
printf '%s' "$ERR" | grep -q "TRUNCATED" && ok || bad "a truncated echo must say so"
printf '%s' "$ERR" | grep -qi "offset" && ok || bad "the truncation notice must say how to get the rest"
# The cap has to actually cap: 900 lines in, at most ~400 out.
echoed=$(printf '%s' "$ERR" | wc -l | tr -d ' ')
[ "$echoed" -le 420 ] && ok || bad "the echo is not capped (emitted $echoed lines for a 900-line file)"

# ── credential material: reason, never contents ───────────────────
# The path is what makes it sensitive, so the content marker must NOT appear.
for secret in ".env" "server.key" "id_rsa" "credentials.json" "prod.tfvars" "settings.local.json"; do
  printf 'SECRET-CONTENT-MARKER\n' > "$WORK/$secret"
  run_hook "sess-E-$secret" "$WORK/$secret"
  [ "$RC" -eq 2 ] && ok || bad "$secret must block (got $RC)"
  if printf '%s' "$ERR" | grep -q "SECRET-CONTENT-MARKER"; then
    bad "$secret LEAKED its contents to stderr"
  else
    ok
  fi
done
# …and a path under .ssh/ is sensitive whatever it is called.
mkdir -p "$WORK/.ssh"
printf 'SECRET-CONTENT-MARKER\n' > "$WORK/.ssh/anything"
run_hook "sess-E-ssh" "$WORK/.ssh/anything"
printf '%s' "$ERR" | grep -q "SECRET-CONTENT-MARKER" && bad "a file under .ssh/ leaked its contents" || ok

# ── ordering: the protection hook still wins (SC-6) ───────────────
# Registered first in the Edit|Write array, so a protected path is refused AS
# PROTECTED. If this ever inverts, .env would be answered with its own contents.
printf 'SECRET-CONTENT-MARKER\n' > "$WORK/.env"
perr=$(payload "sess-F" "$WORK/.env" | bash "$PROTECT" 2>&1 >/dev/null)
prc=$?
[ "$prc" -eq 2 ] && ok || bad "protect-sensitive-files must still block .env (got $prc)"
printf '%s' "$perr" | grep -q "BLOCKED" && ok || bad "the protection reason must be the one reported"
printf '%s' "$perr" | grep -q "SECRET-CONTENT-MARKER" && bad "the protection hook echoed contents" || ok

# The registration order itself is the contract — assert it in the manifest.
order=$(jq -r '.hooks.PreToolUse[] | select(.matcher=="Edit|Write") | [.hooks[].command] | map(sub(".*/";"")) | join(",")' \
  "$ROOT/plugins/core/hooks/hooks.json")
[ "$order" = "protect-sensitive-files.sh,read-before-write.sh" ] && ok || \
  bad "Edit|Write hook order is '$order', want protect-sensitive-files.sh first"

# ── payload edge cases: fail OPEN, never wedge an edit ────────────
printf '%s' '{"tool_input":{}}' | bash "$HOOK" >/dev/null 2>&1 && ok || bad "no file_path must exit 0"
printf '%s' 'not json' | bash "$HOOK" >/dev/null 2>&1 && ok || bad "non-JSON must exit 0"
# No session id means no way to tell a first attempt from a retry — blocking
# there would make every edit in such a session unrecoverable.
jq -nc --arg f "$WORK/app.ts" '{tool_input:{file_path:$f}}' | bash "$HOOK" >/dev/null 2>&1 && ok || \
  bad "a payload without session_id must fail open"

printf 'read-before-write: %d passed, %d failed\n' "$pass" "$fail"
[ "$fail" -eq 0 ]
