#!/usr/bin/env bash
# cc-version-floor.test.sh — the supported-version floor, at its boundary.
#
# core 3.0 handed work back to the harness (native read-before-edit, the Agent
# matcher, dynamic workflow routing), so an older Claude Code silently loses
# those guarantees rather than erroring. session-start.sh warns; it never
# blocks.
#
# plugin.json CANNOT express this: the manifest has a fixed field list and
# Claude Code "ignores top-level fields it does not recognize", so a
# requiredMinimumVersion there would look like a constraint and enforce
# nothing — the permissionMode failure, a third time. Hence a test on the
# comparison itself.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
HOOK="$ROOT/plugins/core/hooks/session-start.sh"

pass=0; fail=0
ok()  { echo "ok   $1"; pass=$((pass + 1)); }
bad() { echo "FAIL $1: $2"; fail=$((fail + 1)); }

FLOOR=$(grep -m1 'CORE_MIN_CC=' "$HOOK" | sed 's/.*"\(.*\)".*/\1/')
[ -n "$FLOOR" ] || { echo "FAIL: no CORE_MIN_CC in session-start.sh"; exit 1; }
ok "floor declared in the hook: $FLOOR"

# Mirror of the hook's comparison, asserted independently of the hook's I/O.
warns() {
  local v="$1" older
  older=$(printf '%s\n%s\n' "$FLOOR" "$v" | sort -V | head -1)
  [ "$v" != "$FLOOR" ] && [ "$older" = "$v" ]
}

check() { # check <version> <expect warn|quiet>
  if warns "$1"; then got=warn; else got=quiet; fi
  [ "$got" = "$2" ] && ok "$1 -> $2" || bad "$1" "got $got, want $2"
}

check "$FLOOR" quiet
check "2.1.161" quiet
check "2.2.0"   quiet
check "3.0.0"   quiet
check "2.1.159" warn
check "2.0.999" warn
# The case a lexical compare gets backwards: "2.1.16" > "2.1.160" as strings.
check "2.1.16"  warn

# The field that does not exist must never appear.
if grep -rq 'requiredMinimumVersion\|minClaudeCodeVersion' "$ROOT/plugins" 2>/dev/null; then
  bad "manifest field" "a minimum-version field was added to a plugin.json — \
Claude Code ignores unknown top-level fields, so it enforces nothing"
else
  ok "no phantom minimum-version field in any plugin.json"
fi

echo "passed=$pass failed=$fail"
[ "$fail" -eq 0 ]
