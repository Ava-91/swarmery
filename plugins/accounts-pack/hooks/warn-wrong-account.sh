#!/usr/bin/env bash
# SessionStart hook — warn when a session is running under an account other than
# the one this project is bound to.
#
# Two rules kept from the skeleton (Phase 5), unconditionally:
#   - FAIL-OPEN. No `set -e`; exit 0 on every path, including malformed input,
#     missing jq, and an unparseable settings file. A hook that can fail is a
#     hook that can block a session.
#   - DRAIN STDIN. Claude Code writes the hook payload to stdin; leaving it
#     unread risks the writer blocking on a full pipe buffer.
set -uo pipefail

INPUT="$(cat 2>/dev/null || true)"

# ── same bash port as plugins/core/statusline/statusline.sh (Д1) ──────────────
account_key_from_config_dir() {
  local dir="${1:-}" name
  [ -z "$dir" ] && { printf 'default'; return; }
  name="$(basename "$dir")"
  case "$name" in
    '.'|'/') printf 'default'; return ;;
  esac
  name="${name#.claude}"
  while :; do
    case "$name" in
      -*) name="${name#-}" ;;
      .*) name="${name#.}" ;;
      *) break ;;
    esac
  done
  printf '%s' "${name:-default}"
}

# Intentionally STRICTER than claudeacct.ValidKey (internal/claudeacct/
# claudeacct.go:158-180) for the characters that matter here — this is NOT
# a mirror of it, despite what an earlier version of this comment claimed.
# This only allows [A-Za-z0-9._-]; ValidKey's own rules (reject "", ".",
# "..", a leading dot, "/", "\", ".." as a substring, whitespace, and
# non-printable runes) still leave it accepting "wörk", "a$b", "a;b", a
# bare backtick, or a double quote. Diverging by rejecting MORE than
# ValidKey does is the safe direction: every extra character this refuses
# is exactly the class — whitespace, quotes, backticks, non-ASCII — that
# turns a config-dir name into something that reads like an instruction
# once it lands in additionalContext below. The failure mode is silence,
# not injection: a real key Go would accept can be turned down here, the
# same fail-open outcome as Go's Binding() returning "" for a key that
# fails ValidKey — never the other way around for these characters. Cost:
# an operator whose account key contains a non-ASCII character gets no
# mismatch warning at all.
valid_account_key() {
  case "${1:-}" in
    ''|'.'|'..')          return 1 ;;
    .*)                   return 1 ;;
    *[!A-Za-z0-9._-]*)    return 1 ;;
  esac
  return 0
}

# ${HOME:-} — not $HOME: `set -u` turns an unset HOME into a fatal error, and a
# hook that exits non-zero is a hook that can block a session (FAIL-OPEN).
ACTUAL="$(account_key_from_config_dir "${CLAUDE_CONFIG_DIR:-${HOME:-}/.claude}")"

# ACTUAL and BOUND both flow verbatim into additionalContext below, which
# Claude Code injects as trusted context. Validating only BOUND (below) and
# leaving ACTUAL unchecked WAS the defect: one sink, two inputs — both must
# pass the same gate, or an attacker just targets whichever one was left
# unchecked. Fail-open on failure, same as an invalid BOUND: silence.
valid_account_key "$ACTUAL" || exit 0

# cwd comes from the hook's own stdin JSON (verified shape: internal/hookshim/
# shim_test.go:203 — session_id, cwd, hook_event_name; NO transcript_path).
PROJECT_DIR="$(printf '%s' "$INPUT" | jq -r '.cwd // empty' 2>/dev/null)"
[ -z "$PROJECT_DIR" ] && PROJECT_DIR="${CLAUDE_PROJECT_DIR:-}"
[ -z "$PROJECT_DIR" ] && exit 0

# Binding lives at <project>/.claude/settings.local.json -> .swarmery.claudeAccount
# (internal/claudeacct/binding.go:20-29). Any jq failure here — file missing,
# unparseable JSON, jq absent from PATH — leaves BOUND empty and falls through
# to silence below; this is the SAME command for all three degradations.
BOUND="$(jq -r '.swarmery.claudeAccount // empty' "$PROJECT_DIR/.claude/settings.local.json" 2>/dev/null)"

[ -z "$BOUND" ] && exit 0
valid_account_key "$BOUND" || exit 0     # Go says unbound ⇒ so do we
[ "$BOUND" = "$ACTUAL" ] && exit 0

CTX="Session started under Claude account '${ACTUAL}', but this project is bound to '${BOUND}' (see .claude/settings.local.json). Quota may be spent on the wrong subscription."
printf '{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":%s}}\n' \
  "$(printf '%s' "$CTX" | jq -Rs .)"
exit 0
