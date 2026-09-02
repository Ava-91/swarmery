#!/bin/bash
# PreModelSwitch hook: refuse to move onto a model nobody has validated.
#
# This is the ONE hook in core allowed to block (exit 2), and it blocks on a
# definite negative only. Everything ambiguous — daemon down, no network,
# malformed payload, timeout — allows the switch with a warning. A laptop with
# the daemon stopped must not become unusable, and a gate that fires on its own
# infrastructure failing is a gate people delete.
#
#   verdict pass            → allow
#   verdict fail            → BLOCK (exit 2)
#   no verdict recorded     → BLOCK (exit 2) — unknown is not the same as fine;
#                             catching exactly this is the point
#   verdict inconclusive    → BLOCK (exit 2) — too little evidence to move on
#   daemon unreachable      → allow + warn
#   malformed / no to_model → allow (nothing to judge)
#   SWARMERY_ALLOW_UNVALIDATED_MODEL=1 → allow + log
#
# The override is not a loophole, it is the validation path: trajectories on a
# new model cannot exist until somebody runs on it, so the operator switches
# deliberately, works, and the next `swarmery modeleval` scores those runs and
# opens the gate on its own.
set -u

input=$(cat 2>/dev/null || true)

# Nothing parseable → nothing to judge.
printf '%s' "$input" | jq -e . >/dev/null 2>&1 || exit 0

to_model=$(printf '%s' "$input" | jq -r '.to_model // empty' 2>/dev/null || true)
from_model=$(printf '%s' "$input" | jq -r '.from_model // empty' 2>/dev/null || true)
[ -n "$to_model" ] || exit 0

# A no-op switch (resume restoring the same model) is not a switch.
[ "$to_model" = "$from_model" ] && exit 0

if [ "${SWARMERY_ALLOW_UNVALIDATED_MODEL:-0}" = "1" ]; then
  echo "⚠️  model-switch gate overridden for ${to_model} (SWARMERY_ALLOW_UNVALIDATED_MODEL=1)" >&2
  exit 0
fi

port="${SWARMERY_PORT:-7777}"
resp=$(curl -fsS --max-time 2 "http://127.0.0.1:${port}/api/models/${to_model}/validation" 2>/dev/null)
rc=$?

# 22 = HTTP error (404 included); anything else non-zero is an infrastructure
# problem, and infrastructure problems must not block a human's model switch.
if [ $rc -ne 0 ] && [ $rc -ne 22 ]; then
  echo "⚠️  model-switch gate: swarmery daemon unreachable on :${port} — allowing ${to_model} unchecked" >&2
  exit 0
fi

verdict=""
detail=""
if [ $rc -eq 0 ]; then
  verdict=$(printf '%s' "$resp" | jq -r '.verdict // empty' 2>/dev/null || true)
  detail=$(printf '%s' "$resp" | jq -r '.detail // empty' 2>/dev/null || true)
fi

if [ "$verdict" = "pass" ]; then
  exit 0
fi

case "$verdict" in
  fail)         reason="failed the golden set: ${detail}" ;;
  inconclusive) reason="not enough evidence yet: ${detail}" ;;
  *)            reason="has no recorded validation" ;;
esac

cat >&2 <<MSG
🚫 model switch blocked: ${to_model} ${reason}

   Validate it:   swarmery modeleval --model ${to_model}
   Override once: SWARMERY_ALLOW_UNVALIDATED_MODEL=1 claude …

   The override is the intended path for a brand-new model: run on it
   deliberately, then re-run modeleval — those runs are the evidence.
MSG
exit 2
