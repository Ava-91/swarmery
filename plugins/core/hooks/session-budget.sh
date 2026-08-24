#!/bin/bash
# session-budget.sh — PostToolUse hook. One instruction, once per session: close
# this session and hand off.
#
# WHY THIS EXISTS. Cost in a long session is quadratic in its length, because
# every continuation re-reads the whole accumulated context. One retro window
# held sessions at $661.60, $412.28 and $258.39, and contexts of 874k and 733k
# tokens — a session that reaches 874k has been paying for that context on every
# turn since it got large. Capping length is worth more than any per-turn
# optimisation.
#
# The daemon's advisor already detects this (rule R9, "fat session") and its own
# detail string already prescribes the fix: "Split the work into shorter
# sessions, /compact when the window fills, or move recurring monitoring to a
# routine that reads state with a fresh small context." It has said so for an
# entire window, to nobody: a retro finding is read after the session is over,
# which is exactly too late. This hook says the same sentence while the session
# is still running and can still act on it.
#
# WHY A HOOK RATHER THAN SURFACED STATE. The statusline already computes a live
# context figure — but the statusline is for the human, and the orchestrator
# never reads it. A PostToolUse hook's systemMessage reaches the model itself,
# needs no new plumbing, and the once-per-session marker pattern is already
# established here (post_bash_index_check.sh). Surfaced state the orchestrator's
# policy is "required to check" would be a rule with nothing enforcing it — the
# same shape as the prose that failed for R9.
#
# ONCE PER CROSSING, NOT ONCE PER TURN. A warning repeated on every tool call
# after the threshold is a context tax that trains the reader to skip it — and it
# would grow the very context it is complaining about. The marker file makes the
# instruction arrive exactly once per session.
#
# Contract: ALWAYS emits valid JSON and exits 0. It never blocks a tool call —
# a budget is advice about how to spend the session, not a reason to fail work
# mid-flight.

# The threshold, in context tokens. Deliberately BELOW the advisor's R9
# fat-session line (R9ContextTokens = 300_000 in
# tools/swarmery/internal/advisor/rules.go): the advisor names a session that has
# ALREADY become expensive, and an instruction that arrives at the same moment
# arrives after the money is spent. 240k is 80% of that line — late enough that
# ordinary work is never interrupted, early enough that the handoff happens
# before the session is what the advisor would flag. The two must never
# contradict each other; scripts/tests/session-budget.test.sh pins the relation.
BUDGET_TOKENS="${SWARMERY_SESSION_BUDGET_TOKENS:-240000}"

input=$(cat)

pass() { echo '{"continue": true}'; exit 0; }

command -v jq >/dev/null 2>&1 || pass

transcript=$(printf '%s' "$input" | jq -r '.transcript_path // empty' 2>/dev/null)
session_id=$(printf '%s' "$input" | jq -r '.session_id // empty' 2>/dev/null)

[ -n "$transcript" ] && [ -f "$transcript" ] || pass

# Already said it in this session — stay silent. No session id means no way to
# tell a first crossing from the hundredth, and a hook that cannot tell must not
# repeat itself every turn: it stays silent rather than becoming the noise it
# exists to avoid.
[ -n "$session_id" ] || pass
safe_session=$(printf '%s' "$session_id" | tr -c 'A-Za-z0-9_.-' '_')
marker="${TMPDIR:-/tmp}/swarmery-session-budget/${safe_session}"
[ -f "$marker" ] && pass

# Context occupancy = the newest assistant turn's input + cache-read +
# cache-creation tokens. The SAME three fields the statusline sums and the
# advisor's R9 query sums, so all three surfaces mean one thing by "context".
used=$(jq -rs '
  [ .[] | select(.message.usage != null) ] | last
  | (.message.usage.input_tokens // 0)
    + (.message.usage.cache_read_input_tokens // 0)
    + (.message.usage.cache_creation_input_tokens // 0)
' "$transcript" 2>/dev/null)
[[ "$used" =~ ^[0-9]+$ ]] || pass
[ "$used" -ge "$BUDGET_TOKENS" ] || pass

mkdir -p "$(dirname "$marker")" 2>/dev/null || true
: > "$marker" 2>/dev/null || true

printf '{"continue": true, "systemMessage": "SESSION BUDGET REACHED (~%dk context tokens, budget %dk). Every further turn re-reads this whole context, so cost from here grows with the square of the work, not with it. Do two things before continuing: (1) CLOSE this session with a report — write what shipped, what is in flight, and the exact next step, into the task doc or your Completion Report, not only into the reply; (2) START a new session carrying a SHORT state summary — the goal, the files in play, the next step — and nothing else. If what remains is recurring monitoring rather than work, move it to a routine instead: a routine reads state with a fresh small context every run."}\n' \
  "$((used / 1000))" "$((BUDGET_TOKENS / 1000))"
exit 0
