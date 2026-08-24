#!/bin/bash
# guard-hits.sh — summarise the bash-shape-guard burn-in log.
#
# plugins/core/hooks/bash-shape-guard.sh appends one JSON record per decision
# it makes. This is the reader for that log, and it exists for exactly one
# question: before a rule's exit code is raised from warn to block, how many
# times did that rule fire, and across how many sessions? A flip argued from a
# date instead of from these counts is the failure mode the guard's own header
# warns about.
#
# Default output is the per-rule summary table — that is what the operator
# reads before filling in docs/GATE-HARDENING.md. Raw records are opt-in.
#
# Usage:
#   scripts/guard-hits.sh                        # summary over the whole log
#   scripts/guard-hits.sh --from 2026-08-24      # inclusive lower bound (date or ISO ts)
#   scripts/guard-hits.sh --to 2026-09-30        # inclusive upper bound (whole day)
#   scripts/guard-hits.sh --rule multi-mutation  # restrict to one rule
#   scripts/guard-hits.sh --raw                  # matching records, one per line
#   scripts/guard-hits.sh --log PATH             # read a specific log file
set -uo pipefail

LOG_BASENAME="bash-shape-guard.jsonl"

usage() {
  sed -n '2,/^set -uo/p' "$0" | sed -e 's/^# \{0,1\}//' -e '/^set -uo/d'
}

# Resolution order kept deliberately identical to the hook's guard_log_file():
# if the two ever disagree, this script silently reports zero hits for a rule
# that is firing, and the flip decision gets made from an empty table.
resolve_log() {
  if [ -n "${BASH_SHAPE_GUARD_LOG:-}" ]; then
    printf '%s' "$BASH_SHAPE_GUARD_LOG"
  elif [ -n "${AGENT_PROJECT:-}" ]; then
    printf '%s/%s/workspace/metrics/%s' \
      "${AGENT_WORKSPACE_ROOT:-$HOME/swarmery-workspace}" "$AGENT_PROJECT" "$LOG_BASENAME"
  elif [ -n "${CLAUDE_PROJECT_DIR:-}" ]; then
    printf '%s/.claude-workspace/metrics/%s' "${CLAUDE_PROJECT_DIR%/}" "$LOG_BASENAME"
  else
    printf '%s/swarmery-guard/%s' "${TMPDIR:-/tmp}" "$LOG_BASENAME"
  fi
}

log_file=""
from=""
to=""
rule_filter=""
raw=0

while [ $# -gt 0 ]; do
  case "$1" in
    --from) from="${2:-}"; shift 2 ;;
    --to)   to="${2:-}"; shift 2 ;;
    --rule) rule_filter="${2:-}"; shift 2 ;;
    --log)  log_file="${2:-}"; shift 2 ;;
    --raw)  raw=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) printf 'unknown argument: %s\n' "$1" >&2; usage >&2; exit 64 ;;
  esac
done

[ -n "$log_file" ] || log_file=$(resolve_log)

if [ ! -f "$log_file" ]; then
  printf 'no burn-in log at %s\n' "$log_file"
  printf 'The guard writes it on its first hit; an empty log means no rule has fired yet.\n'
  exit 0
fi

command -v jq >/dev/null 2>&1 || { printf 'jq is required\n' >&2; exit 70; }

# A bare date bounds the whole day: --to 2026-09-30 must include that day's
# 23:59 records, so the upper bound is extended rather than compared as-is.
[ -n "$from" ] && [ "${#from}" -eq 10 ] && from="${from}T00:00:00Z"
[ -n "$to" ] && [ "${#to}" -eq 10 ] && to="${to}T23:59:59Z"

filtered=$(jq -c \
  --arg from "$from" --arg to "$to" --arg rule "$rule_filter" '
  select(($from == "" or .ts >= $from)
     and ($to   == "" or .ts <= $to)
     and ($rule == "" or .rule == $rule))
' "$log_file" 2>/dev/null)

if [ "$raw" -eq 1 ]; then
  printf '%s\n' "$filtered"
  exit 0
fi

printf 'burn-in log: %s\n' "$log_file"
if [ -n "$from" ] || [ -n "$to" ]; then
  printf 'window:      %s … %s\n' "${from:-(start)}" "${to:-(now)}"
fi
printf '\n'

if [ -z "$filtered" ]; then
  printf 'no decisions recorded in this window\n'
  exit 0
fi

printf '%s\n' "$filtered" | jq -rs '
  group_by(.rule)
  | map({
      rule: .[0].rule,
      hits: length,
      warn: map(select(.decision == "warn")) | length,
      block: map(select(.decision == "block")) | length,
      sessions: (map(.session) | map(select(. != "")) | unique | length),
      first: (map(.ts) | min),
      last: (map(.ts) | max)
    })
  | sort_by(-.hits)
  | (["RULE","HITS","WARN","BLOCK","SESSIONS","FIRST","LAST"] | @tsv),
    (.[] | [.rule, .hits, .warn, .block, .sessions, .first, .last] | @tsv),
    "",
    ("total\t\(map(.hits) | add) hits")
' | column -t -s "$(printf '\t')"
