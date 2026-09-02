#!/bin/bash
# post-tool-observe.sh — the single unmatched PostToolUse hook.
#
# Replaces the former activity-tracker.sh + session-budget.sh pair: with no
# matcher this slot runs on EVERY tool call (500-2000 per session), so it must
# be one process, not two, and do the minimum. Two jobs:
#
#   1. Append one compact JSONL line per tool call to the shared session file
#      (/tmp/claude-session-YYYYMMDD.jsonl) — the substrate the statusline,
#      /dashboard, session-summary.sh, notify-completion.sh and the subagent
#      hooks all read. Agent dispatches also record requested-vs-observed
#      model (a mismatch logs a ModelFallback event for the routing report).
#      The former per-call colored activity box is gone deliberately: it cost
#      a dozen jq/grep processes per tool call and nobody parsed it.
#
#   2. Session budget: once per session, when context occupancy crosses
#      SWARMERY_SESSION_BUDGET_TOKENS (default 240000 — 80% of the advisor's
#      R9 fat-session line; scripts/tests/post-tool-observe.test.sh pins the
#      relation), emit a systemMessage telling the model to close the session
#      with a report and hand off. Cost in a long session is quadratic in its
#      length; the warning must land while the session can still act on it.
#
# Contract: always exits 0 and, when it prints anything, prints valid JSON —
# observation and advice never block a tool call.
set -u

SESSION_FILE="${SWARMERY_SESSION_FILE:-/tmp/claude-session-$(date +%Y%m%d).jsonl}"
BUDGET_TOKENS="${SWARMERY_SESSION_BUDGET_TOKENS:-240000}"

input=$(cat)

pass() { echo '{"continue": true}'; exit 0; }

command -v jq >/dev/null 2>&1 || pass
printf '%s' "$input" | jq -e . >/dev/null 2>&1 || pass

# ── 1. Activity log ─────────────────────────────────────────────────────────
# One jq invocation extracts everything the log line needs.
# \x1f (unit separator) as the field delimiter: unlike TAB it is not IFS
# whitespace, so empty fields survive the read instead of collapsing.
IFS=$'\x1f' read -r tool_name file_path command_str model_requested model_observed transcript session_id <<EOF
$(printf '%s' "$input" | jq -r '[
    (.tool_name // "unknown"),
    (.tool_input.file_path // .tool_input.path // ""),
    (.tool_input.command // ""),
    (if (.tool_name // "") == "Agent" then (.tool_input.model // "") else "" end),
    (if (.tool_name // "") == "Agent" then (.tool_response.model // .tool_response.usage.model // "") else "" end),
    (.transcript_path // ""),
    (.session_id // "")
  ] | map(tostring | gsub("\u001f"; " ") | gsub("\n"; " ")) | join("\u001f")' 2>/dev/null)
EOF
[ -n "${tool_name:-}" ] || pass

{
  jq -c -n \
    --arg ts "$(date -u +"%Y-%m-%dT%H:%M:%SZ")" \
    --arg tool "$tool_name" \
    --arg file "$file_path" \
    --arg cmd "$command_str" \
    --arg model_requested "$model_requested" \
    --arg model_observed "$model_observed" \
    '{ts: $ts, tool: $tool, file: $file, cmd: $cmd}
     + (if $model_requested != "" then {model_requested: $model_requested} else {} end)
     + (if $model_observed != "" then {model_observed: $model_observed} else {} end)'
  if [ -n "$model_requested" ] && [ -n "$model_observed" ] && [ "$model_requested" != "$model_observed" ]; then
    jq -c -n \
      --arg ts "$(date -u +"%Y-%m-%dT%H:%M:%SZ")" \
      --arg cmd "fallback:${model_requested}->${model_observed}" \
      '{ts: $ts, tool: "ModelFallback", file: "", cmd: $cmd}'
  fi
} >> "$SESSION_FILE" 2>/dev/null || true

# ── 2. Session budget (once per session) ────────────────────────────────────
[ -n "$transcript" ] && [ -f "$transcript" ] || pass
# No session id → cannot tell a first crossing from the hundredth → stay silent
# rather than becoming the noise this check exists to avoid.
[ -n "$session_id" ] || pass
safe_session=$(printf '%s' "$session_id" | tr -c 'A-Za-z0-9_.-' '_')
marker="${TMPDIR:-/tmp}/swarmery-session-budget/${safe_session}"
[ -f "$marker" ] && pass

# Context occupancy = the newest assistant turn's input + cache-read +
# cache-creation tokens — the same three fields the statusline and the
# advisor's R9 query sum, so all three surfaces mean one thing by "context".
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
